"""Code-patch cheats — the frame-reset half of the hybrid, applied via /proc.

Some cheats can't be held by writing a value (the game recomputes it every frame in
``Player.ResetEffects`` and reads it same-frame). The fix is a *code* patch: remove
the reset (single-reset fields) or force a constant at the read site (entangled
fields). The patch sites and offsets were derived with Cheat Engine's mono dissector
(see ``ce/README.md``); applying them is a byte-write, so this runs entirely through
``/proc`` — no CE at runtime. CE is only needed to re-derive the AOBs after a game
update.

Each cheat is located by a unique AOB anchor in the JIT'd executable memory (the JIT
address moves per game run, so we scan rather than hardcode). Resolved anchor sites
are cached in a per-pid state file so CLI toggles persist across invocations; a new
game process (new pid) starts with everything off (a restart un-patches the code).

Offsets are for Terraria 1.4.5.7.
"""

from __future__ import annotations

import fcntl
import json
import os
import struct
from collections.abc import Callable
from contextlib import contextmanager
from dataclasses import dataclass

from terrariabonker import profile
from terrariabonker import version as ver
from terrariabonker.locate import (STATLIFE_FROM_OBJ, find_players,
                                   resolve_local_player)

_STATE = os.path.expanduser("~/.config/terrariabonker/patches.json")


def pe_exports(mem, base: int) -> tuple[str | None, dict]:
    """``(module_name, {export: address})`` for a PE image mapped at ``base``.

    wine maps its DLLs anonymously, so /proc/pid/maps cannot name them -- the module
    name has to come out of the image's own export directory. Returns ``(None, None)``
    for anything that is not a PE with exports.
    """
    head = mem.read(base, 0x40)
    if head[:2] != b"MZ":
        return None, None
    lfa = struct.unpack_from("<I", head, 0x3C)[0]
    if lfa > 0x400:
        return None, None
    pe = mem.read(base + lfa, 0x100)
    if pe[:4] != b"PE\0\0":
        return None, None
    exp_rva = struct.unpack_from("<I", pe, 0x78)[0]     # DataDirectory[0] = exports
    if not exp_rva:
        return None, None
    d = mem.read(base + exp_rva, 0x28)
    nm_rva = struct.unpack_from("<I", d, 0x0C)[0]
    n_names = struct.unpack_from("<I", d, 0x18)[0]
    func_rva = struct.unpack_from("<I", d, 0x1C)[0]
    names_rva = struct.unpack_from("<I", d, 0x20)[0]
    ord_rva = struct.unpack_from("<I", d, 0x24)[0]
    mod = mem.read(base + nm_rva, 64).split(b"\0")[0].decode("latin1")
    if not n_names or n_names > 20000:
        return mod, {}
    names = struct.unpack("<%dI" % n_names, mem.read(base + names_rva, 4 * n_names))
    ords = struct.unpack("<%dH" % n_names, mem.read(base + ord_rva, 2 * n_names))
    blob = mem.read(base + func_rva, 4 * (max(ords) + 1))
    return mod, {mem.read(base + r, 96).split(b"\0")[0].decode("latin1"):
                 base + struct.unpack_from("<I", blob, 4 * o)[0]
                 for r, o in zip(names, ords)}


def resolve_export(mem, module: str, fn: str) -> int:
    """Address of ``module!fn`` in the live process, found by scanning for PE images."""
    with open(f"/proc/{mem.pid}/maps") as f:
        bases = sorted({int(q[0].split("-")[0], 16) for q in (ln.split() for ln in f)
                        if q[1].startswith("r") and int(q[0].split("-")[0], 16) < 2 ** 32})
    for b in bases:
        try:
            mod, exp = pe_exports(mem, b)
        except Exception:
            continue
        if mod and module.lower() in mod.lower() and exp and fn in exp:
            return exp[fn]
    raise PatchError(f"{module}!{fn} not found in the running process")


def _b(hexstr: str) -> bytes:
    return bytes(int(x, 16) for x in hexstr.split())


@dataclass(frozen=True)
class Pattern:
    """An AOB with optional wildcards. ``raw`` holds the bytes (0 where wildcarded),
    ``mask`` is 1 for fixed positions and 0 for wildcards. Needed for anchors that
    span ASLR'd immediates (absolute addresses, mono type-init thunks)."""
    raw: bytes
    mask: bytes

    def seed(self) -> tuple[int, bytes]:
        """(offset, bytes) of the longest fixed run — used as the fast buf.find key."""
        best_off = best_len = cur_off = cur_len = 0
        for i, mk in enumerate(self.mask):
            if mk:
                cur_off = i if cur_len == 0 else cur_off
                cur_len += 1
                if cur_len > best_len:
                    best_len, best_off = cur_len, cur_off
            else:
                cur_len = 0
        return best_off, self.raw[best_off:best_off + best_len]

    def matches(self, buf: bytes, pos: int) -> bool:
        if pos < 0 or pos + len(self.raw) > len(buf):
            return False
        return all(buf[pos + j] == self.raw[j] for j, mk in enumerate(self.mask) if mk)


def _pat(hexstr: str) -> Pattern:
    """Parse an AOB where ``??`` marks a wildcard byte."""
    toks = hexstr.split()
    raw = bytes(0 if t == "??" else int(t, 16) for t in toks)
    mask = bytes(0 if t == "??" else 1 for t in toks)
    return Pattern(raw, mask)


# AOB anchors in executable memory (verified single-match on 1.4.5.7).
@dataclass(frozen=True)
class Anchor:
    """A pattern plus provenance: the game builds this AOB was verified on.

    Verification is a **ledger, not a gate**. An anchor that still matches on an
    unverified build is used and the UI marks it as unproven, because gating on the build
    id would disable working cheats: the 24825745 -> 24893155 rebuild left 7 of 9 anchors
    matching with identical field displacements.

    ``unique`` marks the rare anchor where patching a structural twin would be harmful;
    everything else patches every copy it finds (mono can JIT one method into more than
    one arena, which is what broke reach/mining/max_minions).

    **Support accumulates.** When the game moves the code a cheat patches, the re-derived
    bytes go in as a ``variants`` entry beside the existing pattern, never over it, so the
    older build keeps working — replacing would silently break it while ``verified`` still
    claimed it. Builds seen so far have shared these bytes (1.4.5.7 -> 1.4.5.8 left every
    pattern matching), so there are no variants yet; the mechanism exists so that adding
    one is the obvious move rather than a refactor.
    """
    pattern: Pattern
    verified: frozenset[str] = frozenset()
    unique: bool = False
    # Builds where the game's code moved and this cheat needed *different* bytes:
    # ``((build_key, Pattern), ...)``. Support accumulates rather than being replaced --
    # adding a variant never removes the pattern an older build matches, so re-deriving a
    # cheat for a new release keeps the previous release working.
    variants: tuple[tuple[str, Pattern], ...] = ()

    def candidates(self, build: str | None = None) -> tuple[Pattern, ...]:
        """Patterns to try, best first.

        The running build's own variant leads when there is one, but every other pattern
        is still tried afterwards: an unverified build usually matches a pattern derived
        for a different one, and gating on the build id would disable cheats that work.
        Same reason ``verified`` is a ledger and not a gate.
        """
        by_build = dict(self.variants)
        out: list[Pattern] = []
        if build is not None and build in by_build:
            out.append(by_build[build])
        if self.pattern not in out:
            out.append(self.pattern)
        out.extend(p for _, p in self.variants if p not in out)
        return tuple(out)


@dataclass(frozen=True)
class Edit:
    """One byte edit inside an anchor match, applied under some cheat's toggle.

    Lets a single toggle change more than one site: making the vanity accessory slots
    functional needs two loop bounds widened, in different parts of UpdateEquips, and
    they must go on and off together or the cheat is half-applied.
    """
    anchor: str
    off: int
    orig: bytes
    patched: bytes


@dataclass(frozen=True)
class Resolution:
    """What resolving one anchor produced, for status and for honest error text."""
    sites: tuple[int, ...]
    available: bool
    reason: str = ""
    verified: bool = False


_RAW_ANCHORS: dict[str, Pattern] = {
    # ResetEffects: blockRange reset (mov [edi+9F8],0 @ +0), fld1 (+10), pickSpeed reset
    # (fstp [edi+8D8] @ +12) sit adjacent — one anchor covers reach (patch_off 0) and
    # mining (patch_off 12). The two reset instructions are WILDCARDED because those are
    # exactly the bytes reach/mining overwrite: if they were fixed, the anchor would stop
    # matching once either cheat is applied (a cold-cache re-resolve then failed with
    # "anchor not found"). Uniqueness comes from the invariant fld1 + the downstream
    # field-clear run (mov byte [edi+866],0; [edi+870],0; [edi+871],1) which no cheat touches.
    "reset_block": _pat("?? ?? ?? ?? ?? ?? ?? ?? ?? ?? D9 E8 ?? ?? ?? ?? ?? ?? "
                        "C6 87 66 08 00 00 00 C6 87 70 08 00 00 00 C6 87 71 08 00 00 01"),
    # ApplyItemTime(Item,float): the fmulp … cvttsd2si … max(edi,1) tail. fast_place
    # overwrites the max(edi,1) at +20 (`mov eax,1; cmp edi,eax; cmovl edi,eax` ->
    # `mov edi,4; nop*5`), so those 10 bytes are WILDCARDED — otherwise a cold-cache
    # re-resolve fails once the cheat is applied ("anchor not found"). The invariant
    # prefix (fmulp…mov edi,ecx…jle) plus the downstream store (mov [esp+4],edi;
    # mov eax,[ebp+8]; mov [esp],eax) keep it unique.
    # TETeleportationPylon.PlacementPreviewHook_CheckIfCanPlace — the whole one-pylon-
    # per-biome rule for single-player (spec 037). Its IL is
    #     type = GetPylonTypeFromPylonTileStyle(style)
    #     return Main.PylonSystem.HasPylonOfType(type) ? 1 : 0
    # and it is registered with badReturn: 1, so returning 0 always lifts the limit.
    # GetPylonTypeFromPylonTileStyle is inlined to the two movzx here.
    # The first three bytes are the ones the cheat overwrites, so they are WILDCARDED —
    # otherwise a cold re-resolve fails once it is applied and it could not be turned off
    # again (the trap specs 032-034 each hit). The mono type-init immediate, the init
    # call, Main.PylonSystem's address and the call to HasPylonOfType are ASLR'd.
    # Player.PickTile's entry (spec 040). The first five bytes are the ones the cheat
    # overwrites with its jump, so they are WILDCARDED — the trap specs 032-034 and 037
    # each hit. The prologue alone is far from unique (190 methods share it), so the
    # pattern runs on through the argument loads, the mono type-init check and the two
    # zeroed locals to the `mov eax,[Main.tile]`; that is unique.
    # Player.Update's call to GrabItems -- the per-frame site the extractor hooks. The
    # pattern covers the `if (!dead)` check that guards the call (movzx eax,byte
    # [eax+dead]; test; jne) plus the argument setup, so it is anchored to the real call
    # rather than to an incidental byte sequence: the arg-setup tail alone matches 154
    # places. The field displacement is wildcarded. The five bytes at +21 carry no
    # relative address, which is what makes them displaceable -- the call itself could not
    # be, its rel32 differs every session.
    # trailing five wildcarded: the extractor overwrites them (see "grabitems" above)
    # Player.Update's call to BordersMovement (spec 043) -- the auto-use hook. The only
    # call site of that method in Update, unconditional, and ~50 IL bytes before
    # ItemCheckWrapped reads the use control, so a write here lands just before the read.
    # The 13 bytes after the call are the patch site and are WILDCARDED; unlike
    # "grabitems" this anchor stays unique that way, because the trailing fstp of
    # slotsMinions and the Main.netMode compare carry the uniqueness on their own.
    "borders_movement": _pat("E8 ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? "
                             "8B 45 08 D9 EE D9 98 ?? ?? ?? ?? 8B 05 ?? ?? ?? ?? "
                             "3D 02 00 00 00"),
    "grabitems_call": _pat("0F B6 80 ?? ?? ?? ?? 85 C0 75 14 8B 45 08 8B 4D 0C "
                           "89 4C 24 04 ?? ?? ?? ?? ??"),
    "pick_tile": _pat("?? ?? ?? ?? ?? 56 83 EC 7C 8B 7D 08 8B 5D 0C B8 ?? ?? ?? ?? "
                      "F7 00 01 00 00 00 74 08 8D 6D 00 E8 ?? ?? ?? ?? "
                      "C7 45 E4 00 00 00 00 C7 45 E0 00 00 00 00 8B 05 ?? ?? ?? ??"),
    "pylon_place": _pat("?? ?? ?? 83 EC 18 B8 ?? ?? ?? ?? F7 00 01 00 00 00 74 05 "
                        "E8 ?? ?? ?? ?? 8B 45 14 0F B6 C0 0F B6 C8 8B 05 ?? ?? ?? ?? "
                        "89 4C 24 04 89 04 24 39 00 8D 6D 00 E8 ?? ?? ?? ??"),
    "place": _pat("DE C9 DD 5D F0 F2 0F 10 45 F0 F2 0F 2C C8 8B F9 85 C0 7E 0A "
                  "?? ?? ?? ?? ?? ?? ?? ?? ?? ?? 89 7C 24 04 8B 45 08 89 04 24"),
    # TileReachCheckSettings.GetRanges(this, out x, out y). Prologue + mono type-init
    # + the tileRangeX read and first imul/store, with the ASLR'd immediates
    # (type-init thunk, static addr) wildcarded to make it unique. Starts at the method
    # base so the injection offset (0xCA) is measured from here.
    "getranges": _pat("55 8B EC 53 57 56 83 EC 1C 8B 5D 08 8B 75 0C 8B 7D 10 "
                      "B8 ?? ?? ?? ?? F7 00 01 00 00 00 74 05 E8 ?? ?? ?? ?? "
                      "8B 05 ?? ?? ?? ?? 8B 0B 0F AF C1 89 06"),
    # Player.GrabItems: the grab-range store `mov [ebp-54],eax; lea eax,[ebp-50]; …;
    # cmp [ebx],ebx` followed by the first get_Hitbox call (wildcarded). Starts at the
    # injection point (the store), so the injection offset is 0.
    # KNOWN EXCEPTION to the wildcard-your-patch-site rule (see test_patcher.py). These
    # six bytes are the ones the pickup injection overwrites, but they are also what makes
    # this anchor unique: wildcarding them takes it from 1 live site to 124. Until the
    # pattern is re-cut with enough trailing context to stand on its own, pickup keeps a
    # literal patch site and its cold-cache status stays unreliable. That is the lesser
    # bug -- 124 candidate sites is not a status problem, it is a corrupted game.
    "grabitems": _pat("89 45 AC 8D 45 B0 89 44 24 04 89 1C 24 39 1B E8 ?? ?? ?? ??"),
    # Spawner.GetSpawnRate prologue: loads esi=out spawnRate ([ebp+10]), edi=out
    # maxSpawns ([ebp+14]); then fldz/fstp and the first `mov [esi],[static]`. Same
    # esi/edi-out shape as GetRanges — forced at the epilogue (offset 0x1EAA).
    "get_spawn_rate": _pat("55 8B EC 53 57 56 83 EC 5C 8B 5D 0C 8B 75 10 8B 7D 14 "
                           "B8 ?? ?? ?? ?? F7 00 01 00 00 00 74 05 E8 ?? ?? ?? ?? "
                           "D9 EE D9 5D D4 8B 05 ?? ?? ?? ?? 89 06"),
    # CommonDrop.TryDroppingItem (esi = this = the CommonDrop). The chance roll passes
    # chanceDenominator (this+0x10, `mov ecx,[esi+10]` at method +0x26) as the RNG
    # bound, then a drop happens when the roll < chanceNumerator (this+0x1C). We cap the
    # denominator at +0x26 to floor the drop chance. The anchor is based at +0x2D — the
    # distinctive `mov [esp],eax; ...; mov ecx,[esi+1C]; ... mov eax,[esi+0C]` tail that
    # reads chanceNumerator and itemId — so the pattern sits ENTIRELY DOWNSTREAM of the
    # bytes the jump overwrites (inject_off = -7). That keeps the anchor scannable even
    # after the patch is in place (disable/recover never hit a self-corrupted seed).
    # Call-target immediates are wildcarded, which also makes it match both CommonDrop
    # twins (see Injection.multi).
    "trydrop": _pat("89 04 24 39 00 90 E8 ?? ?? ?? ?? 8B 4E 1C 3B C1 0F 8D ?? ?? ?? ?? "
                    "8B 45 10 89 45 E8 8B 46 0C 89 45 E4"),
    # Main.TriggerPing(Vector2 position): fires when a fullscreen-map ping is placed;
    # [ebp+08]/[ebp+0C] are the ping world X/Y. The inject anchor is the position-
    # independent arg-marshal tail at TriggerPing+0x2D (`mov ecx,[ebp+08]; mov [esp+04],
    # ecx; mov ecx,[ebp+0C]; mov [esp+08],ecx; mov [esp],eax; cmp [eax],eax; lea
    # ebp,[ebp+00]`), with the following call's rel32 wildcarded. inject_off = 0; the
    # overwrite is the first 7 bytes (`mov ecx,[ebp+08]; mov [esp+04],ecx`), reproduced
    # in the stub. Placed here (not at the class-init `mov eax,<imm>` at +0x6) because
    # that instruction embeds an ASLR/JIT address and would not be a stable overwrite.
    # leading seven wildcarded: the teleport injection overwrites them
    "trigger_ping": _pat("?? ?? ?? ?? ?? ?? ?? 8B 4D 0C 89 4C 24 08 89 04 24 39 00 "
                         "8D 6D 00 E8 ?? ?? ?? ??"),
    # Player.Teleport(Vector2 newPos, int Style, int extraInfo): the call target for the
    # map-ping hook. Anchored on the two constant field stores at Teleport+0x32
    # (`mov [ebx+BF4],64; mov [ebx+468],4`) plus the `cmp esi,0A; sete al; movzx eax,al`
    # Style dispatch — all position-independent. The method entry (the absolute call
    # target) is anchor_base - 0x32 (see Injection.call_target_off).
    "player_teleport": _pat("C7 83 F4 0B 00 00 64 00 00 00 C7 83 68 04 00 00 04 00 00 00 "
                            "83 FE 0A 0F 94 C0 0F B6 C0"),
    # Player.ResetEffects: the per-frame `maxMinions = 1` reset (mov [edi+3F8],1). Its
    # immediate (the reset value) is WILDCARDED so the anchor resolves whether or not the
    # cap cheat is applied; uniqueness comes from the adjacent `mov [edi+A60],1` reset that
    # follows it (edi = the player). The cheat rewrites the immediate to the desired cap.
    "reset_minions": _pat("C7 87 F8 03 00 00 ?? ?? ?? ?? C7 87 60 0A 00 00 01 00 00 00"),
    # UpdateEquips' accessory-effect loop: `for (k = 3; k < 10; k++) if
    # (IsItemSlotUnlockedAndUsable(k)) ApplyEquipFunctional(k, GetEffectiveArmor(k))`.
    # Matched from the item argument store through the loop bound, so ONE anchor covers
    # both edits the vanity cheat needs. The slot argument is already in eax when it is
    # stored, which is what lets the clamp stub avoid any frame-layout assumption.
    # Wildcarded: the ebp displacements and call rel32s, the 7 bytes the stub displaces
    # (+7..+13) and the loop bound itself (+27) — a cold re-resolve must still match once
    # the cheat is applied. See ce/ACCESSORY_FINDINGS.md.
    "equip_apply": _pat("89 44 24 08 8B 45 ?? ?? ?? ?? ?? ?? ?? ?? 90 E8 ?? ?? ?? ?? "
                        "83 45 ?? 01 83 7D ?? ?? 7C ??"),
    # SmartCursorHelper.SmartCursorLookup, at the tail where the search box it just got
    # from GetTileRegion is clamped against the world edges: four (load field, clamp to
    # 10..maxTiles-10, store field) blocks writing SmartCursorUsageInfo through esi
    # (reachableStartX +0x30, EndX +0x34, StartY +0x38, EndY +0x3C). Matched from the
    # StartY block through the final EndY store, which is what the cheat displaces (+69,
    # wildcarded) together with the `test ebx,ebx` whose flags the following je needs.
    # See ce/SMARTCURSOR_FINDINGS.md.
    "smart_cursor": _pat("8B 46 38 8B 0D ?? ?? ?? ?? 83 E9 0A 89 4C 24 08 "
                         "C7 44 24 04 0A 00 00 00 89 04 24 90 E8 ?? ?? ?? ?? 89 46 38 "
                         "8B 46 3C 8B 0D ?? ?? ?? ?? 83 E9 0A 89 4C 24 08 "
                         "C7 44 24 04 0A 00 00 00 89 04 24 90 E8 ?? ?? ?? ?? "
                         "?? ?? ?? ?? ?? 74 ??"),
    # UpdateEquips' FIRST loop, which already walks the whole inventory every frame
    # (vanilla uses it to refresh info/mechanical accessories, which is why a Depth Meter
    # works from your bag today). Matched from `mov eax,[edi+D4]` (Player.inventory, the
    # same field inventory.py reaches as statLife-0x664) through the bounds check and the
    # `lea eax,[eax+ecx*4+0x10]` element address, to the point where the Item* is in eax.
    # The five bytes at +22 are the ones the stub displaces, so they are wildcarded — a
    # cold re-resolve has to work while the cheat is applied.
    "inventory_scan": _pat("8B 87 D4 00 00 00 8B 4D ?? 39 48 0C 0F 86 ?? ?? ?? ?? "
                           "8D 44 88 10 ?? ?? ?? ?? ?? 89 45 ??"),
    # UpdateEquips' benefit loop: `if (item.accessory) GrantPrefixBenefits(item);
    # GrantArmorBenefits(item)` and its `i < 10` bound (+48, wildcarded because we patch
    # it). The `movzx eax,[eax+7D]` accessory test is what makes this specific — the bare
    # increment/compare tail matches unrelated code elsewhere in the process.
    "equip_benefits": _pat("0F B6 40 7D 85 C0 74 ?? 8B 45 ?? 89 44 24 04 89 3C 24 8B C0 "
                           "E8 ?? ?? ?? ?? 8B 45 ?? 89 44 24 04 89 3C 24 90 E8 ?? ?? ?? ?? "
                           "83 45 ?? 01 83 7D ?? ??"),
}

# Builds every anchor here has been confirmed on, newest last. "Verified" means the AOB
# resolved AND the cheat was seen working in-game on that build — not merely that it matched.
# What a build key does NOT say: which .NET runtime was executing the game. These patterns
# match machine code wine-mono's JIT emitted, so a Proton update can break a cheat with
# Terraria untouched. The runtime is tracked beside the key (see version.detect_runtime and
# builds.remember) rather than folded into it -- every entry below predates that tracking,
# and backfilling a runtime we never recorded would be a guess dressed as provenance.
_VERIFIED_BUILDS: tuple[str, ...] = (
    ver.KNOWN_BUILD_KEY,        # 1.4.5.8+24893155 — see the 2026-08-23 note below
    # The build these AOBs were originally derived against.
    "1.4.5.7+24825745",
    # 2026-08-23, and a key to distrust: this one is a *mix*. The version came from the
    # frequency vote in detect_version -- which returns a stale 1.4.5.7 even on 1.4.5.8 --
    # while the buildid came from Steam's already-updated manifest, so the key describes a
    # build that never existed. It is kept because the panel really did record verifications under
    # it — those were confirmed on 1.4.5.7 — and dropping it would silently un-verify
    # them. The detector that produced it has since been fixed to read the version out of
    # the exe the process maps.
    "1.4.5.7+24893155",
    # 2026-08-23, after the update was actually loaded: every anchor resolved on 1.4.5.8
    # and the maintainer confirmed all twelve cheats still working in-game. The update did
    # not touch the code any of them patch.
    "1.4.5.8+24893155",
)

# Per-anchor divergence, for a build that breaks only some anchors: {anchor: (build, ...)}.
_ALSO_VERIFIED: dict[str, tuple[str, ...]] = {}

# Anchors with a different history to _VERIFIED_BUILDS — derived later, so never seen on
# the original build. An empty tuple would mean "resolves, but not yet confirmed in-game
# anywhere", which the panel reports as unproven rather than hiding.
_VERIFIED_INSTEAD: dict[str, tuple[str, ...]] = {
    # 2026-08-23: derived on this build and confirmed in-game with the whole vanity column
    # occupied — wings, Shield of Cthulhu, balloon and Hermes Boots all took effect, their
    # Warding prefixes contributed defense (the equip_benefits edit), the vanity armour in
    # 10..12 stayed inert, and the info accessories that already worked there (Depth Meter,
    # Tungsten Watch) were unchanged.
    "equip_apply": ("1.4.5.7+24893155", "1.4.5.8+24893155"),
    "equip_benefits": ("1.4.5.7+24893155", "1.4.5.8+24893155"),
    # 2026-08-23: confirmed in-game — accessories carried in the inventory granted their
    # effects without being equipped, their Warding prefixes kept contributing defense when
    # moved out of a vanity slot into the bag (the GrantPrefixBenefits call), and disabling
    # restored the displaced bytes and stopped the effects with the game still running.
    "inventory_scan": ("1.4.5.7+24893155", "1.4.5.8+24893155"),
    # 2026-08-23: confirmed in-game with reach/tool_reach at 75 — the stutter (triggerable
    # by merely holding Shift, i.e. the per-frame search, not the placing) is gone, while
    # manual placement reach and tool/interaction reach are unchanged.
    "smart_cursor": ("1.4.5.7+24893155", "1.4.5.8+24893155"),
    # 2026-08-23: confirmed in-game on 1.4.5.8 — a second Cavern pylon was placed with a
    # Cavern pylon already in the world, and both appear on the map wired into the pylon
    # network. Nothing downstream dedupes by type, as the recon predicted.
    "pylon_place": ("1.4.5.8+24893155",),
    # 2026-08-24: confirmed in-game on 1.4.5.8 — the extractor calls PickTile through this
    # for every tile it takes, and whole veins came out (45 tiles in two batches) with the
    # game healthy. Only ever derived on 1.4.5.8, so it claims nothing about 1.4.5.7.
    "pick_tile": ("1.4.5.8+24893155",),
    # 2026-08-24: Player.Update's per-frame call to GrabItems, where the extractor hooks.
    # Derived and confirmed on 1.4.5.8 only. It must be listed here even though it is
    # verified on the *current* build: the default is every build in _VERIFIED_BUILDS, so
    # an anchor that says nothing silently claims two 1.4.5.7 builds it has never run on.
    "grabitems_call": ("1.4.5.8+24893155",),
    # 2026-08-26: Player.Update's call to BordersMovement, where auto-use hooks. Cut on
    # 1.4.5.8 and verified unique there, but the stub has never been watched running in
    # the game, so it claims NO build at all. An AOB that resolves is not an anchor that
    # works; the empty set is the honest statement until a frame has gone through it.
    "borders_movement": (),
}

ANCHORS: dict[str, Anchor] = {
    key: Anchor(pat, verified=frozenset(
        _VERIFIED_INSTEAD.get(key, (*_VERIFIED_BUILDS, *_ALSO_VERIFIED.get(key, ())))))
    for key, pat in _RAW_ANCHORS.items()
}


@dataclass(frozen=True)
class Cheat:
    name: str
    label: str
    anchor: str          # key into ANCHORS
    patch_off: int       # offset within the anchor match to patch
    orig: bytes          # original bytes (== anchor[patch_off:patch_off+len])
    patched: bytes       # replacement (same length)
    value_off: int | None = None    # field offset from statLife to set, or None
    value_kind: str = "f32"         # "f32" | "i32"
    on_value: float | int = 0
    off_value: float | int = 0
    note: str = ""
    # Tunable in-place patch: when set, the patched bytes are BUILT from the value
    # (e.g. an immediate inside the instruction) instead of being fixed. ``patched`` is
    # then unused; enable writes ``make_patched(value)`` at the site, disable restores
    # ``orig``, and is_enabled is true when the site differs from ``orig``.
    make_patched: Callable[[int], bytes] | None = None


CHEATS: dict[str, Cheat] = {
    "mining": Cheat(
        "mining", "Global mining speed (pickSpeed)", "reset_block", 12,
        _b("D9 9F D8 08 00 00"), _b("DD D8 90 90 90 90"),
        value_off=0x1A0, value_kind="f32", on_value=0.2, off_value=1.0,
        note="Global mining speed. Lower is faster."),
    "reach": Cheat(
        "reach", "Placement reach (blockRange)", "reset_block", 0,
        _b("C7 87 F8 09 00 00 00 00 00 00"), _b("90 90 90 90 90 90 90 90 90 90"),
        value_off=0x2C0, value_kind="i32", on_value=20, off_value=0,
        note="Extended placement reach for every item."),
    # Force a low itemTime at the placement-timing read site: `mov edi,N; nop*5`. N is the
    # itemTime (lower = faster), tunable via presets (Fast=4 / Faster=2 / Hyper=1).
    "pylons": Cheat(
        "pylons", "Multiple pylons per biome", "pylon_place", 0,
        _b("55 8B EC"), _b("31 C0 C3"),
        note="Place more than one pylon of the same type. Needs one pylon placed "
             "first, so the game compiles the check."),
    "fast_place": Cheat(
        "fast_place", "Fast placement (ApplyItemTime)", "place", 20,
        _b("B8 01 00 00 00 3B F8 0F 4C F8"), b"",
        make_patched=lambda n: b"\xbf" + struct.pack("<i", max(1, int(n))) + b"\x90" * 5,
        note="Near-instant block placement."),
    # Rewrite ResetEffects' `maxMinions = 1` reset to `maxMinions = N`, so N minion slots
    # hold each frame (accessory bonuses still stack on top). patch_off=6 is the 4-byte
    # immediate; make_patched packs N there. Ported in spirit from ReGrind-style stat caps.
    "max_minions": Cheat(
        "max_minions", "Minion cap (maxMinions)", "reset_minions", 6,
        _b("01 00 00 00"), b"",                     # patched is built from the value
        make_patched=lambda n: struct.pack("<i", max(1, int(n))),
        note="Raises the minion (summon) cap."),
}


def _i32(n: int) -> bytes:
    return struct.pack("<i", int(n))


def _u32(n: int) -> bytes:
    """Pack an absolute 32-bit address (unsigned; a high user-space address in a 32-bit
    process would overflow the signed pack)."""
    return struct.pack("<I", int(n) & 0xFFFFFFFF)


def _force_xy(n: int) -> bytes:
    """mov dword [esi], N ; mov dword [edi], N — force the two GetRanges outputs."""
    return b"\xc7\x06" + _i32(n) + b"\xc7\x07" + _i32(n)


def _imul_eax(n: int) -> bytes:
    """imul eax, eax, N — scale the value in eax (e.g. a grab range) by N."""
    n = int(n)
    if -128 <= n <= 127:
        return b"\x6b\xc0" + struct.pack("<b", n)      # imul eax,eax,imm8
    return b"\x69\xc0" + struct.pack("<i", n)          # imul eax,eax,imm32


def _force_spawn(n: int) -> bytes:
    """mov [esi], 6 ; mov [edi], N — force GetSpawnRate's outputs: a low spawnRate
    (frequent) and maxSpawns = N (active-enemy cap; 0 = no spawns / peaceful)."""
    return b"\xc7\x06" + _i32(6) + b"\xc7\x07" + _i32(n)


def _cap_drop_denom(pct: int) -> bytes:
    """Rewrite `mov ecx,[esi+10]; mov [esp+04],ecx` (the chanceDenominator load feeding
    the drop roll) so the denominator is clamped to ``cap = 100 // pct``. A drop rolls
    < chanceDenominator and succeeds when the roll < chanceNumerator, so a smaller
    denominator only ever *raises* the chance: pct=100 -> cap=1 -> roll is always 0 ->
    guaranteed; pct=50 -> cap=2 -> at least a 1-in-2 floor; existing better drops are
    untouched (min, never max). ``rerun_overwrite=False`` — this replaces both
    displaced instructions."""
    cap = max(1, 100 // int(pct))
    return (b"\x8b\x4e\x10"                 # mov ecx,[esi+10]   (chanceDenominator)
            + b"\x81\xf9" + _i32(cap)       # cmp ecx, cap
            + b"\x7e\x05"                   # jle +5  (already <= cap: keep it)
            + b"\xb9" + _i32(cap)           # mov ecx, cap
            + b"\x89\x4c\x24\x04")          # mov [esp+04],ecx  (reproduced store)


# float ×16 by adding 4 to the IEEE-754 exponent field (bits 23-30): 4 << 23. Valid for
# normalized positive floats (the ping tile coords), no FPU / no memory constant needed.
_F32_TIMES16 = 0x02000000


def _teleport_body(player_base: int, call_target: int) -> bytes:
    """Managed-call stub for the map-ping teleport, injected at Main.TriggerPing+0x2D
    (ebp frame valid; [ebp+08]/[ebp+0C] = the ping position). Calls
    ``Player.Teleport(this=player_base, newPos=(X,Y), Style=0, extraInfo=0)`` then
    reproduces the two displaced instructions so the ping itself still proceeds. The
    final jmp back is appended by the caller.

    ``TriggerPing`` delivers the ping in **tile** coordinates (float), but
    ``Player.Teleport`` (which sets ``Player.position``) works in **world pixels**
    (1 tile = 16 px). The stub converts tile→pixel by adding ``_F32_TIMES16`` to each
    coord's float bits (exponent += 4 == ×16) — verified against live data
    (tile 3501.84 → 56029.4 px, landing on the pinged spot).

    32-bit mono passes all args on the stack, right-to-left (this, X, Y, Style,
    extraInfo). Rather than assume caller- vs callee-cleanup (mono emits ``ret N`` for
    some methods), esp is saved before the pushes and restored after the call via ebx —
    a callee-saved register Teleport preserves — so the stub is correct either way.
    Style 0 avoids Teleport's 4/9/10 special branches (plain teleport, no side effects).
    ``pushad``/``popad`` protect the registers the original code still needs (notably
    eax, loaded upstream and consumed as the ping-list arg just after the inject site)."""
    return (b"\x60"                             # pushad
            + b"\x8b\xdc"                       # mov ebx,esp   (restore point, survives call)
            + b"\x8b\x45\x08"                   # mov eax,[ebp+08]   (ping tile X)
            + b"\x05" + _i32(_F32_TIMES16)      # add eax, 0x02000000   (×16 -> px X)
            + b"\x8b\x4d\x0c"                   # mov ecx,[ebp+0C]   (ping tile Y)
            + b"\x81\xc1" + _i32(_F32_TIMES16)  # add ecx, 0x02000000   (×16 -> px Y)
            + b"\x6a\x00"                       # push 0             (extraInfo)
            + b"\x6a\x00"                       # push 0             (Style)
            + b"\x51"                           # push ecx           (newPos.Y px)
            + b"\x50"                           # push eax           (newPos.X px)
            + b"\x68" + _u32(player_base)       # push player_base   (this)
            + b"\xb8" + _u32(call_target)       # mov eax, Teleport entry
            + b"\xff\xd0"                       # call eax
            + b"\x8b\xe3"                       # mov esp,ebx        (restore esp, either conv)
            + b"\x61"                           # popad
            + b"\x8b\x4d\x08"                   # mov ecx,[ebp+08]   \ reproduce the two
            + b"\x89\x4c\x24\x04")              # mov [esp+04],ecx   / displaced instructions


# The extractor's queue, in our own arena rather than at the tail of a borrowed cave:
# a count, then that many (x, y) pairs. 32 per swing matches what VeinMiner caps at, and
# is the number that turns "one block per swing" into "the vein just goes" -- a typical
# vein is 10-30 tiles. Draining a 400-tile vein in a single call would run 400 PickTiles
# in one frame, each spawning dust, drops and light updates, and would visibly hitch.
ORE_MAX_BATCH = 32
ORE_QUEUE_OFF = 0x400            # queue sits well clear of the ~100-byte stub
ORE_QUEUE_BYTES = 4 + ORE_MAX_BATCH * 8
# The pick power handed to PickTile. It was 100, and 100 is not enough: a tile breaks on
# accumulated damage, and the game credits a short list of tile types -- hellstone among
# them -- at a reduced rate, so those took the hit and survived it. In game that looks
# exactly like what was reported: the block poofs, stays put, and still needs mining one
# swing at a time. 250 leaves room for that reduction and also clears the stiffest pick
# requirement in the game (lihzahrd brick, 210), which 100 never met either.
ORE_PICK_POWER = 250


# Auto-use (spec 043). Two dwords in the arena's reserved region, clear of every stub:
# the arm flag the trainer sets, and a counter the stub bumps on each press so a test can
# prove "armed N times -> pressed N times" without watching the game.
AUTO_USE_ARMED_OFF = 0x500
AUTO_USE_COUNT_OFF = 0x504
USE_ITEM_OFF = 0x672             # Player.controlUseItem, from the Player object base


def _ore_extract_body(patcher, inj) -> bytes:
    """Mine a batch of queued tiles **every frame**, from Player.Update.

    The unprivileged side does the thinking -- read the tile map, flood-fill the vein,
    decide what may be taken -- and writes a queue here. This stub walks it and calls
    ``Player.PickTile(this, x, y, ORE_PICK_POWER, -1)`` for each, the same call the game
    makes when
    you swing, so drops, framing, lighting and the ``CanKillTile`` check all happen
    exactly as they normally would.

    **Why Player.Update and not PickTile.** Hooking PickTile itself only ran the stub when
    the player swung, and the queue is armed *after* a swing has broken a tile -- so a
    vein sat armed until the player happened to swing again. Breaking one block and
    stopping did nothing at all. Hooking the per-frame call to GrabItems (guarded only by
    ``if (!dead)``) means an armed queue drains on the next frame, which is what "break one
    block and the vein goes" actually requires. It also removes re-entrancy completely:
    PickTile is no longer hooked, so calling it cannot come back through here, and the cap
    argument is plain -1 like the game's own callers pass.

    **It lives in our own arena, not in a cave.** Earlier versions borrowed padding and hit
    every limit of it: a stub too big for a gap, a stub that wrote to its cave and faulted
    (that cave was a code section of CUESDK_2015.dll -- read-execute), and a queue that
    fits in no gap at all. See ``Patcher.arena``.

    **The count is consumed before the work.** Reading it into edi and zeroing it
    immediately means a batch is mined once rather than re-mined on every frame at 60fps,
    and the queue cannot be drained twice if anything ever does re-enter.

    **The loop counter is a register.** ``edi`` counts down and ``esi`` walks the queue;
    both are callee-saved, so PickTile hands them back. The count is clamped to
    :data:`ORE_MAX_BATCH` in the stub as well as by the caller, because a corrupted count
    would not crash -- it would mine coordinates nobody asked for, which damages a world.

    **Stack alignment.** Mono's x86 JIT builds 16-byte frames assuming esp is 12 (mod 16)
    at entry; PickTile's own prologue proves it (4 pushes + ``sub esp,0x7C`` = 140 bytes).
    ``ebp`` holds the aligned base and each iteration restores esp from it, so every call
    in the batch gets the alignment mono expects however PickTile cleans up.
    """
    from terrariabonker.locate import find_localplayer_anchor

    pick_tile = patcher._resolve("pick_tile")     # the call target, not the hook site
    tail = find_localplayer_anchor(patcher.mem)
    if tail is None:
        raise PatchError("could not locate Main.player / Main.myPlayer")
    player_arr = patcher.mem.read_u32(tail - 0xA)
    my_player = patcher.mem.read_u32(tail - 4)
    if not (player_arr and my_player):
        raise PatchError("Main.player / Main.myPlayer are not readable")
    queue = patcher.arena() + ORE_QUEUE_OFF

    body = (b"\x8b\xe5"                              # mov esp,ebp   <- each iteration
            + b"\xa1" + _u32(player_arr)             # mov eax,[Main.player]
            + b"\x8b\x0d" + _u32(my_player)          # mov ecx,[Main.myPlayer]
            + b"\x8b\x44\x88\x10"                    # mov eax,[eax+ecx*4+0x10]
            + b"\x6a\xff"                            # push -1        (cap, as the game does)
            + b"\x68" + _u32(ORE_PICK_POWER)         # push power     (pickPower)
            + b"\xff\x76\x04"                        # push [esi+4]   (y)
            + b"\xff\x36"                            # push [esi]     (x)
            + b"\x50"                                 # push eax       (this)
            + b"\xb8" + _u32(pick_tile)              # mov eax,PickTile
            + b"\xff\xd0"                            # call eax
            + b"\x83\xc6\x08"                        # add esi,8      (next pair)
            + b"\x4f")                                # dec edi
    if len(body) + 2 > 128:                           # jnz back is a rel8
        raise PatchError(f"extractor loop body grew to {len(body)} bytes — too far to "
                         "jump back in one byte")
    loop = body + b"\x75" + bytes([(256 - (len(body) + 2)) & 0xFF])     # jnz loop
    tail_code = loop + b"\x8b\xe3"                    # mov esp,ebx (restore either conv)
    setup = (b"\x83\xff" + bytes([ORE_MAX_BATCH])    # cmp edi,MAX
             + b"\x76\x05"                           # jbe +5
             + b"\xbf" + _u32(ORE_MAX_BATCH)         # mov edi,MAX   (clamp a bad count)
             + b"\xbe" + _u32(queue + 4)             # mov esi,&pairs
             + b"\x8b\xdc"                           # mov ebx,esp
             + b"\x83\xe4\xf0"                       # and esp,-16   \ mono wants esp==12
             + b"\x83\xec\x0c"                       # sub esp,12    / (mod 16) at entry
             + b"\x8b\xec")                          # mov ebp,esp   (aligned base)
    empty = (b"\x8b\x3d" + _u32(queue)              # mov edi,[count]
             + b"\x85\xff"                           # test edi,edi
             + b"\x74" + bytes([7 + len(setup) + len(tail_code)])   # je skip (nothing queued)
             + b"\x83\x25" + _u32(queue) + b"\x00")  # and [count],0  (consume it)
    assert len(setup) + len(tail_code) + 7 < 128, "skip jump no longer fits a short branch"
    return (b"\x60"                                   # pushad
            + empty + setup + tail_code
            + b"\x61"                                 # popad  <- skip lands here
            + inj.overwrite)                           # the displaced arg stores


def _auto_use_body(patcher, inj) -> bytes:
    """Press the use button once, on the next frame, when the trainer arms it.

    The poller that proved this cheat works (spec 042) wins by volume: ~400,000 writes a
    second, covering every frame many times over. That takes a fish, but it cannot promise
    *one* of anything -- a 20 ms burst took the water from one bobber to three, catching
    the fish and re-casting twice. A stub that runs once per frame is correct by
    construction instead.

    **Consume before acting.** The flag is cleared before the byte is written, the ordering
    the extractor already uses: a stub that dies between the two presses nothing, where the
    other order would press forever.

    **`this` is free here.** The site's first displaced instruction is ``mov eax,[ebp+8]``
    -- Player.Update's own argument -- so the Player pointer costs nothing and cannot be
    stale. Update is never entered on a null ``this``, which is why there is no null guard
    beyond the cheap test kept below.

    **No calls, so no ABI.** Every crash in spec 040 came from a call: argument order,
    frame alignment, or the cave the stub lived in. This one calls nothing, touches no
    stack beyond pushad/pushfd, and writes two dwords of its own arena.

    The counter exists for the first test: arm it N times, read N back, without any
    claim about what the game did with the presses.
    """
    armed = patcher.arena() + AUTO_USE_ARMED_OFF
    count = patcher.arena() + AUTO_USE_COUNT_OFF
    press = (b"\x83\x25" + _u32(armed) + b"\x00"      # and [armed],0   (consume first)
             + b"\x8b\x45\x08"                        # mov eax,[ebp+8] (this)
             + b"\x85\xc0"                             # test eax,eax
             + b"\x74\x0d"                             # je skip (never taken; cheap)
             + b"\xc6\x80" + _u32(USE_ITEM_OFF) + b"\x01"   # mov byte [eax+0x672],1
             + b"\xff\x05" + _u32(count))              # inc [count]
    return (b"\x60\x9c"                                # pushad ; pushfd
            + b"\x83\x3d" + _u32(armed) + b"\x00"     # cmp [armed],0
            + b"\x74" + bytes([len(press)])             # je skip
            + press
            + b"\x9d\x61"                              # popfd ; popad   <- skip lands here
            + inj.overwrite)                             # the displaced this-load and store


def _clamp_vanity_slot(_value: int = 0) -> bytes:
    """Map a vanity accessory slot onto its functional mirror before the call.

    ApplyEquipFunctional uses its slot argument for exactly one thing —
    hideVisibleAccessory[slot] — and that array is bool[10], so passing 13..19 straight
    through would throw IndexOutOfRange every frame. The slot is already in eax at the
    injection point, so this needs nothing from the stack frame:

        cmp eax,0xa      ; a vanity slot?
        jl  +3
        sub eax,0xa      ; 13..19 -> 3..9, the mirror whose hide-visual flag it follows
    """
    return _b("83 F8 0A 7C 03 83 E8 0A")


def _shrink_smart_cursor(n: int) -> bytes:
    """Shrink the smart cursor's search box to the player-to-cursor span plus n tiles.

    SmartCursorLookup sizes that box from GetTileRegion, which calls the GetRanges that
    `tool_reach` forces and then adds `blockRange` — so both reach cheats inflate it, and
    it is an AREA: 121 tiles a frame at vanilla-ish reach, 22,801 at 75, roughly 90,000
    with both stacked. That is the auto-place stutter.

    Two earlier shapes were wrong, both found in play:

    * Centred on the player, the box stopped containing the cursor once it moved past n.
      The same four fields are also the "is the target in reach" test right after this
      point, so smart placement dropped out entirely ("moving the mouse starts placing
      without smart enabled").
    * Centred on the cursor, the cursor stayed inside but the span back toward the player
      was cut out, and the search works outward from the player — so it failed again as
      soon as the two were more than n apart.

    So cover both ends: take the original box's midpoint as the player tile (GetTileRegion
    built the box around the player, so the midpoint is where they are), take the cursor
    from screenTargetX/Y, and keep min-n .. max+n of the pair, intersected with the
    original box so it can only ever shrink. The area becomes the on-screen separation
    plus a margin instead of the reach squared.

    The clamp has to be here rather than in GetTileRegion, which has nine callers
    including IsInTileInteractionRange and AdjTiles — the ones tool_reach exists to
    extend. esi holds the SmartCursorUsageInfo; eax/ecx are dead (the following code
    reloads both). The displaced `test ebx,ebx` is reproduced LAST so the je after our
    jump back sees the flags it expects.
    """
    n = max(1, int(n))

    def axis(target_off: int, start_off: int, end_off: int) -> bytes:
        S, E, T = bytes([start_off]), bytes([end_off]), bytes([target_off])
        return (b"\x8b\x46" + S            # mov eax,[esi+start]
                + b"\x03\x46" + E          # add eax,[esi+end]
                + b"\xd1\xf8"              # sar eax,1        -> player tile (midpoint)
                + b"\x8b\x4e" + T          # mov ecx,[esi+target]   cursor tile
                + b"\x3b\xc1"              # cmp eax,ecx
                + b"\x7e\x01"              # jle +1
                + b"\x91"                   # xchg eax,ecx     -> eax=min, ecx=max
                + b"\x2d" + _i32(n)         # sub eax,n        -> lo
                + b"\x81\xc1" + _i32(n)    # add ecx,n        -> hi
                + b"\x3b\x46" + S          # cmp eax,[esi+start]
                + b"\x7e\x03"              # jle keep         (start = max(start, lo))
                + b"\x89\x46" + S          # mov [esi+start],eax
                + b"\x3b\x4e" + E          # cmp ecx,[esi+end]
                + b"\x7d\x03"              # jge keep         (end = min(end, hi))
                + b"\x89\x4e" + E)         # mov [esi+end],ecx

    return (b"\x89\x46\x3c"          # mov [esi+3C],eax   (displaced store, first)
            + axis(0x28, 0x30, 0x34)   # X: screenTargetX vs reachableStartX/EndX
            + axis(0x2c, 0x38, 0x3c)   # Y: screenTargetY vs reachableStartY/EndY
            + b"\x85\xdb")            # test ebx,ebx       (displaced, last: flags)


def _inventory_accs_body(patcher, _inj) -> bytes:
    """Stub for "accessories work from the inventory", injected in UpdateEquips' first
    loop where the Item* is already in eax.

    Vanilla walks the inventory there every frame but only refreshes info and mechanical
    accessories. This calls the three methods an equipped accessory goes through —
    ApplyEquipFunctional (the effects), GrantPrefixBenefits (Menacing/Warding/...) and
    GrantArmorBenefits (per-item extras) — for the items that are actually accessories.

    ``item.accessory`` (+0x7D) is tested first: the loop runs 58 times a frame and
    ApplyEquipFunctional is an 11.6 KB method, so calling it for every stack of dirt would
    be pure waste. Typically only a handful of items pass.

    Slot 0 is passed because the slot argument is used for one thing only —
    ``hideVisibleAccessory[slot]``, a bool[10] (see spec 032's clamp).

    Register discipline follows the teleport stub: pushad/popad around everything, the
    Item* parked in esi, and esp restored from ebx after each call so the stub is correct
    whether mono cleaned the arguments or not.
    """
    def call(target: int) -> bytes:
        return (b"\xb8" + _u32(target)      # mov eax, <method entry>
                + b"\xff\xd0"               # call eax
                + b"\x8b\xe3")               # mov esp,ebx   (restore, either convention)

    apply_fn = patcher._call_target("equip_apply", 15)       # ApplyEquipFunctional
    prefix_fn = patcher._call_target("equip_benefits", 20)   # GrantPrefixBenefits
    armor_fn = patcher._call_target("equip_benefits", 36)    # GrantArmorBenefits

    guarded = (b"\x60"                       # pushad
               + b"\x8b\xf0"                 # mov esi,eax        (Item*)
               + b"\x8b\xdc"                 # mov ebx,esp
               # ApplyEquipFunctional(this, slot=0, item) - args pushed right to left
               + b"\x56" + b"\x6a\x00" + b"\x57" + call(apply_fn)
               # GrantPrefixBenefits(this, item)
               + b"\x56" + b"\x57" + call(prefix_fn)
               # GrantArmorBenefits(this, item)
               + b"\x56" + b"\x57" + call(armor_fn)
               + b"\x61")                     # popad  (eax is the Item* again)

    return (b"\x8b\x00"                      # mov eax,[eax]      (displaced) -> Item*
            + b"\x80\x78\x7d\x00"            # cmp byte [eax+7D],0  item.accessory
            + b"\x74" + bytes([len(guarded)])  # je skip
            + guarded
            + b"\x8b\x40\x6c")               # mov eax,[eax+6C]   (displaced) item.type


def _norm_inj(v: dict) -> dict:
    """Normalize a persisted injection record to the multi-site shape
    ``{"sites": [{"inject", "cave"}], "stub_len"}`` — accepting the legacy flat
    ``{"inject", "cave", "stub_len"}`` written before multi-site support."""
    stub_len = int(v.get("stub_len", 0))
    if "sites" in v:
        sites = [{"inject": int(s["inject"]), "cave": int(s["cave"])} for s in v["sites"]]
    else:
        sites = [{"inject": int(v["inject"]), "cave": int(v["cave"])}]
    return {"sites": sites, "stub_len": stub_len}


@dataclass(frozen=True)
class Injection:
    """A code-cave cheat: some cheats can't be done in place because the code we want
    to add is longer than the site has room for. We anchor a method, overwrite a few
    bytes at an injection point with a jump to a code cave (a run of executable
    padding), run our stub there, then jump back. ``make_body(value)`` builds the
    injected instructions; the stub is ``make_body(value) + overwrite + jmp back``."""
    name: str
    label: str
    anchor: str          # key into ANCHORS
    inject_off: int      # offset from the anchor to the injection point
    overwrite: bytes     # the original bytes there
    # Injected instructions before `overwrite`. None for a managed-call injection, whose
    # body is built from resolved runtime addresses instead (see call_anchor).
    make_body: Callable[[int], bytes] | None
    note: str = ""
    # If True (default), the stub re-runs ``overwrite`` after ``make_body`` (force a
    # value, then continue the displaced original). If False, ``make_body`` fully
    # replaces those instructions (it reproduces whatever of them must still happen)
    # and they are NOT re-run — used when the displaced bytes are the very computation
    # being overridden (e.g. the loot cheat rewrites the denominator load in place).
    rerun_overwrite: bool = True
    # If True, the anchor is expected to match multiple sites (structural twins whose
    # bodies are identical bar their call targets, e.g. CommonDrop and its luck-scaling
    # sibling) and the stub is installed at EVERY match rather than requiring a unique
    # anchor.
    multi: bool = False
    # Managed-call injection: when set, the stub CALLS a managed method (the project's
    # first). ``call_anchor`` names the anchor for the call target; the method entry is
    # ``resolve(call_anchor) - call_target_off``. The body is built by ``_teleport_body``
    # from that target and the local player object base, not from ``make_body``. Such
    # injections are single-site, carry no tunable value, and reproduce their displaced
    # bytes themselves (``rerun_overwrite=False``).
    call_anchor: str | None = None
    call_target_off: int = 0
    # Byte edits applied and reverted with this injection, for a cheat that is a cave
    # plus a couple of in-place changes elsewhere (see Edit).
    edits: tuple = ()
    # Builds the stub body from live state (resolved call targets and the like) rather
    # than from a value: ``build_body(patcher, injection) -> bytes``. Such a stub
    # reproduces its own displaced bytes, so ``rerun_overwrite`` does not apply.
    build_body: Callable | None = None
    # True if the stub's own code writes anywhere inside its cave (self-modifying data,
    # a scratch slot, a counter). A cave is borrowed padding inside somebody else's
    # mapping and those are typically **read-execute**, so such a stub faults on its first
    # run -- while installing it works fine, because /proc/pid/mem bypasses page
    # protection. Setting this makes _find_cave demand a writable page instead of letting
    # the mismatch surface as an access violation mid-game. See _ore_extract_body.
    writes_cave: bool = False
    # True if the stub belongs in memory we allocate rather than borrowed padding. Set it
    # when the stub is too big for a gap, needs to write, or carries a buffer -- see
    # Patcher.arena. The 5-byte site jump still reaches it (rel32 spans +-2GB).
    arena: bool = False


INJECTIONS: dict[str, Injection] = {
    # Inject just before GetRanges' epilogue, where esi=out_x ptr, edi=out_y ptr are
    # still live. Overwrite `lea esp,[ebp-0C]; pop esi; pop edi` (5 bytes) with a jump
    # to a cave that does `mov [esi],N; mov [edi],N`, re-runs those 5 bytes, and jumps
    # back to `pop ebx`. Forces the tile-reach output past the game's clamp.
    "tool_reach": Injection(
        "tool_reach", "Tool + interaction reach (GetRanges)", "getranges",
        0xCA, _b("8D 65 F4 5E 5F"), _force_xy,
        arena=True,
        note="Extends mining, tool use, chests, signs and crafting stations together."),
    # Player.GrabItems: a call returns the grab range in eax, then `mov [ebp-54],eax`
    # stores it. Inject `imul eax,N` before that store to scale the pickup radius.
    # Overwrite `mov [ebp-54],eax; lea eax,[ebp-50]` (6 bytes), re-run in the stub.
    "pickup": Injection(
        "pickup", "Item pickup range (GrabItems)", "grabitems",
        0x0, _b("89 45 AC 8D 45 B0"), _imul_eax,
        arena=True,
        note="Scales the item pickup radius."),
    # GetSpawnRate epilogue (esi=out spawnRate, edi=out maxSpawns still live). Overwrite
    # `lea esp,[ebp-0C]; pop esi; pop edi` like GetRanges; force the two outputs.
    "spawn_rate": Injection(
        "spawn_rate", "Spawn rate (GetSpawnRate)", "get_spawn_rate",
        0x1EAA, _b("8D 65 F4 5E 5F"), _force_spawn,
        arena=True,
        note="Caps active enemies. 0 is peaceful."),
    # CommonDrop.TryDroppingItem +0x26: cap the chance denominator so drops have a
    # minimum chance (100% = guaranteed). Ported from the FearLess ReGrind table.
    "loot": Injection(
        "loot", "Drop chance floor (CommonDrop)", "trydrop",
        -7, _b("8B 4E 10 89 4C 24 04"), _cap_drop_denom,   # anchor at +0x2D -> site at +0x26
        arena=True,
        note="Minimum drop chance for common drops. 100 is guaranteed.",
        rerun_overwrite=False, multi=True),
    # Make the seven VANITY accessory slots functional (spec 032). Vanilla already runs
    # ApplyEquipVanity for 13..19 — which is why info accessories like the Depth Meter
    # already work there — but never ApplyEquipFunctional, so boots, wings, defense and
    # damage do nothing. Widen both UpdateEquips loop bounds from 10 to 20 and clamp the
    # slot before the call. No UI or save-format change: those slots already exist, are
    # already drawn, and already hold only accessories.
    "vanity_accs": Injection(
        "vanity_accs", "Vanity accessories work (slots 13-19)", "equip_apply",
        7, _b("89 44 24 04 89 3C 24"), _clamp_vanity_slot,
        arena=True,
        note="Vanity accessory slots grant full effects, doubling your usable "
             "accessories.",
        edits=(Edit("equip_apply", 27, b"\x0a", b"\x14"),        # ApplyEquipFunctional loop
               Edit("equip_benefits", 48, b"\x0a", b"\x14"))),   # prefix/armour benefits
    # Smart-cursor search radius (spec 034). Keeps placement/tool reach at whatever the
    # user set while stopping the smart cursor from scanning a box that size every frame.
    "smart_cursor": Injection(
        "smart_cursor", "Smart cursor search radius", "smart_cursor",
        69, _b("89 46 3C 85 DB"), _shrink_smart_cursor,
        arena=True,
        note="Don't set this too high or the game will lag.",
        rerun_overwrite=False),
    # Accessories take effect from the INVENTORY (spec 033). UpdateEquips' first loop
    # already walks all 58 slots every frame; this hooks the point where the Item* is in
    # eax and runs the accessory machinery on the ones that are accessories.
    "inventory_accs": Injection(
        "inventory_accs", "Accessories work from inventory", "inventory_scan",
        22, _b("8B 00 8B 40 6C"), None,
        arena=True,
        note="Accessories work from your inventory, without being equipped.",
        rerun_overwrite=False, build_body=_inventory_accs_body),
    # Map-ping teleport (ported from the FearLess ReGrind table). Hook Main.TriggerPing
    # at +0x2D; the stub calls Player.Teleport(this, pingX, pingY, 0, 0), warping the
    # local player to any fullscreen-map ping. No tunable value (on/off). The call target
    # is resolved via the player_teleport anchor (entry = match - 0x32). A game restart
    # clears it; re-toggle after a world/character reload (the player object base is
    # baked into the stub at enable time).
    # Ore extractor (spec 040). The stub is deliberately tiny: the unprivileged side
    # reads the tile map, floods the vein and decides what may be taken, then writes one
    # coordinate into the stub's own slot. See _ore_extract_body.
    "ore_extract": Injection(
        "ore_extract", "Ore extractor (vein mining)", "grabitems_call",
        0x15, _b("89 04 24 8B C0"), None,
        build_body=_ore_extract_body, rerun_overwrite=False, arena=True,
        note="Mines the rest of an ore vein while you mine it. Whitelisted ores only."),
    # Auto-use (spec 043): press the use button once per arm, from inside the frame.
    # Hooked just after Player.Update's call to BordersMovement, which is ~50 IL bytes
    # before ItemCheckWrapped reads the control -- so the write lands between the game
    # taking real input and the game acting on it, which is the window a poller cannot aim
    # at. 13 displaced bytes, none of them relative.
    "auto_use": Injection(
        "auto_use", "Auto-use (press the use button)", "borders_movement",
        0x5, _b("8B 45 08 C7 80 FC 03 00 00 00 00 00 00"), None,
        build_body=_auto_use_body, rerun_overwrite=False, arena=True, writes_cave=True,
        note="Lets a cheat press your use button. Ships off; nothing presses it on its "
             "own."),
    "teleport": Injection(
        "teleport", "Map-ping teleport (TriggerPing)", "trigger_ping",
        0x0, _b("8B 4D 08 89 4C 24 04"), None,
        arena=True,
        note="Double-click the fullscreen map to warp there. Re-toggle after a world "
             "reload.",
        rerun_overwrite=False, call_anchor="player_teleport", call_target_off=0x32),
}


@dataclass(frozen=True)
class ValueSpec:
    kind: str            # "f32" | "i32"
    default: float | int
    lo: float | int
    hi: float | int
    unit: str = ""
    # Named discrete choices (label, value). When set, the GUI shows a dropdown of these
    # labels instead of a numeric spinbox (e.g. fast_place: Fast/Faster/Hyper).
    presets: tuple | None = None


@dataclass(frozen=True)
class PatchInfo:
    """A view-neutral description of one patch for the CLI/GUI (merges the value
    cheats and the injections into one ordered catalog)."""
    name: str
    label: str
    note: str
    value: ValueSpec | None
    kind: str            # "cheat" | "injection"
    section: str = "Misc"   # grouping for the UI (see SECTIONS)


_VALUE_SPECS: dict[str, ValueSpec] = {
    "mining": ValueSpec("f32", 0.2, 0.05, 2.0, "pickSpeed · lower = faster"),
    "reach": ValueSpec("i32", 20, 0, 100, "extra tiles"),
    "tool_reach": ValueSpec("i32", 30, 1, 200, "tiles · mining & interaction"),
    "pickup": ValueSpec("i32", 50, 2, 500, "× grab range"),
    "spawn_rate": ValueSpec("i32", 15, 0, 200, "max active enemies · 0 = peaceful"),
    "loot": ValueSpec("i32", 100, 1, 100, "% min drop chance · 100 = guaranteed"),
    "max_minions": ValueSpec("i32", 10, 1, 255, "minion slots (base; +accessories)"),
    "smart_cursor": ValueSpec("i32", 20, 3, 200, "tiles"),
    # itemTime presets (lower = faster placement). "Fast" is the original behaviour.
    "fast_place": ValueSpec("i32", 4, 1, 4, "placement speed",
                            presets=(("Fast", 4), ("Faster", 2), ("Hyper", 1))),
    # Not a value patched into the game -- the extractor's stub is built by build_body,
    # which ignores it. It rides the same plumbing so the choice gets a widget, is saved
    # with the profile and comes back on auto-restore; the watcher reads it to decide
    # whether to sweep gems. Without this, `--gems` existed on the CLI and was simply
    # unreachable from the panel.
    "ore_extract": ValueSpec("i32", 0, 0, 1, "what to sweep",
                             presets=(("Ores only", 0), ("Ores + gems", 1))),
}


# How the patches are grouped in the UI, in display order. A cheat missing from here
# falls into the last section, so adding one never hides it.
SECTIONS: tuple[tuple[str, tuple[str, ...]], ...] = (
    ("Build", ("mining", "reach", "fast_place", "tool_reach", "smart_cursor",
               "pylons", "ore_extract")),
    ("Combat", ("max_minions", "spawn_rate", "loot")),
    ("Accessories", ("vanity_accs", "inventory_accs")),
    ("Misc", ("pickup", "teleport")),
)


def _section_of(name: str) -> str:
    for section, names in SECTIONS:
        if name in names:
            return section
    return SECTIONS[-1][0]


def _build_catalog() -> dict[str, PatchInfo]:
    out: dict[str, PatchInfo] = {}
    for n, c in CHEATS.items():
        out[n] = PatchInfo(n, c.label, c.note, _VALUE_SPECS.get(n), "cheat", _section_of(n))
    for n, inj in INJECTIONS.items():
        out[n] = PatchInfo(n, inj.label, inj.note, _VALUE_SPECS.get(n), "injection",
                           _section_of(n))
    # Present them grouped, so the CLI listing and the panel agree on order.
    order = {name: i for i, (_s, names) in enumerate(SECTIONS) for name in names}
    return dict(sorted(out.items(), key=lambda kv: order.get(kv[0], len(order))))


PATCH_CATALOG: dict[str, PatchInfo] = _build_catalog()


class PatchError(RuntimeError):
    pass


class Patcher:
    """Applies/toggles the code-patch cheats on one game process."""

    def __init__(self, mem):
        self.mem = mem
        self._sites: dict[str, list[int]] = {}   # anchor key -> every resolved site
        self._enabled: set[str] = set()
        self._inj: dict[str, dict] = {}       # injection name -> {inject, cave, stub_len}
        self._values: dict[str, float] = {}   # cheat name -> last applied value
        self._arena: int | None = None        # base of memory we allocated (see arena())
        self._load_state()

    # --- state persistence -------------------------------------------------
    @contextmanager
    def _locked(self):
        """Serialize a mutating operation across processes: hold an exclusive lock on the
        state file and re-load the latest state under it, so two concurrent CLI
        invocations (e.g. the GUI toggling several cheats at once) can't clobber each
        other's records. The lock lives in the common layer, so it protects any caller."""
        os.makedirs(os.path.dirname(_STATE), exist_ok=True)
        lock = open(_STATE + ".lock", "a+")
        try:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
            self._load_state()                       # refresh under the lock, then modify
            yield
        finally:
            fcntl.flock(lock.fileno(), fcntl.LOCK_UN)
            lock.close()

    def _load_state(self):
        try:
            with open(_STATE) as f:
                s = json.load(f)
            if s.get("pid") == self.mem.pid:     # same process -> reuse
                # Older state stored one address per anchor; accept both shapes so an
                # upgrade in place does not throw away a live game's patch state.
                self._sites = {k: ([int(v)] if isinstance(v, int) else [int(x) for x in v])
                               for k, v in s.get("sites", {}).items()}
                self._enabled = set(s.get("enabled", []))
                self._inj = {k: _norm_inj(v) for k, v in s.get("inj", {}).items()}
                self._values = {k: v for k, v in s.get("values", {}).items()
                                if k in _VALUE_SPECS}
                self._arena = s.get("arena")
        except (OSError, ValueError):
            pass

    def _save_state(self):
        os.makedirs(os.path.dirname(_STATE), exist_ok=True)
        tmp = _STATE + f".{os.getpid()}.tmp"          # atomic write: no torn reads for status
        with open(tmp, "w") as f:
            json.dump({"pid": self.mem.pid, "sites": self._sites,
                       "enabled": sorted(self._enabled), "inj": self._inj,
                       "values": self._values, "arena": self._arena}, f)
        os.replace(tmp, _STATE)

    # --- our own memory ----------------------------------------------------
    # 64KB, which is VirtualAlloc's reservation granularity, so a smaller request would
    # reserve this much anyway. Laid out so every stub has an address decided by *which*
    # injection it belongs to rather than by searching for space:
    #
    #   0x0000..0x0FFF   reserved. The extractor's queue lives at ORE_QUEUE_OFF.
    #   0x1000..         one ARENA_SLOT per injection site, indexed, never searched for.
    #   ARENA_SIZE-16    the stamp that lets a later run find this arena again.
    #
    # Searching is what went wrong: a scan for cold bytes cannot tell free space from a
    # stub that has been disabled and scrubbed, so it handed one injection a slice of
    # another. An index cannot collide.
    ARENA_SIZE = 0x10000
    ARENA_STUBS_OFF = 0x1000            # first stub slot
    ARENA_SLOT = 256                    # per site; the largest stub today is 96 bytes
    ARENA_MAX_SITES = 8                 # per injection (the loot hook has four twins)
    # Stamped at the arena's tail so we can find it again. An arena outlives the process
    # that asked for it -- VirtualAlloc'd memory belongs to the game, not to us -- so
    # losing the state file must not cost the player another swing to re-bootstrap.
    ARENA_MAGIC = b"TBARENA1"
    ARENA_MAGIC_OFF = 0x10000 - 16

    def _free_base(self, need: int) -> int:
        """A free 64KB-aligned address (VirtualAlloc's reservation granularity).

        Chosen per session, never hardcoded: the map differs every launch and the obvious
        round numbers sit inside mono's big RWX arenas -- 0x30000000 turned out to be
        33MB into one.
        """
        rs = []
        with open(f"/proc/{self.mem.pid}/maps") as f:
            for ln in f:
                a, b = (int(v, 16) for v in ln.split()[0].split("-"))
                if a < 2 ** 32:
                    rs.append((a, b))
        rs.sort()
        merged: list[list[int]] = []
        for a, b in rs:
            if merged and a <= merged[-1][1]:
                merged[-1][1] = max(merged[-1][1], b)
            else:
                merged.append([a, b])
        for (_, end), (nxt, _) in zip(merged, merged[1:]):
            base = (end + 0xFFFF) & ~0xFFFF
            if end >= 0x10000000 and nxt - base >= max(need, 0x100000):
                return base
        raise PatchError("no free 64KB-aligned hole for an arena")

    def _mapped(self, addr: int) -> str | None:
        with open(f"/proc/{self.mem.pid}/maps") as f:
            for ln in f:
                a, b = (int(v, 16) for v in ln.split()[0].split("-"))
                if a <= addr < b:
                    return ln.strip()
        return None

    def arena(self, on_wait=None, timeout: float = 20.0) -> int:
        """Memory of our own inside the game: RWX, ours alone, ``ARENA_SIZE`` bytes.

        Code caves are *borrowed* padding inside somebody else's read-execute mapping, and
        this project has now hit every limit of that: a stub too big for a gap, a stub that
        needed to write (which faulted, because the cave was a code section of
        CUESDK_2015.dll), and a queue that will not fit in any gap at all. This is the
        graduation ``_find_cave`` has always pointed at.

        We cannot allocate into another process directly, so the game allocates for itself:
        a ~36-byte springboard in a cave calls ``kernel32!VirtualAlloc`` at a **fixed**
        base. Fixed, because a stub in a read-execute cave has nowhere to report a return
        value *to* -- so instead of reading the result we choose the address and look for
        it in /proc/pid/maps.

        The springboard is hooked on the extractor's own site, which is a per-frame call in
        ``Player.Update``, so it fires within a frame and unhooks itself the moment the
        region appears. It does need the game to be *running*: Terraria pauses in
        single-player when its window loses focus, and a paused game runs no frames.
        ``on_wait`` is called once when we start waiting, so a caller can say so rather
        than appearing to hang.
        """
        import time

        if self._arena and self._arena_ok(self._arena):
            return self._arena
        found = self._find_arena()               # ours from earlier in this process?
        if found is not None:
            self._arena = found
            self._save_state()
            return found

        va = resolve_export(self.mem, "kernel32", "VirtualAlloc")
        base = self._free_base(self.ARENA_SIZE)
        inj = INJECTIONS["ore_extract"]
        # + inject_off, like every other caller. Without it the springboard lands on the
        # anchor's first byte instead of the injection point -- for an anchor whose match
        # starts 0x15 before the site, that is a jump written into the middle of another
        # instruction, and the restore afterwards leaves the displaced bytes there.
        site = self._resolve(inj.anchor) + inj.inject_off
        body = (b"\x60"                                  # pushad
                + b"\x6a\x40"                            # push PAGE_EXECUTE_READWRITE
                + b"\x68" + _u32(0x3000)                 # push MEM_COMMIT|MEM_RESERVE
                + b"\x68" + _u32(self.ARENA_SIZE)        # push size
                + b"\x68" + _u32(base)                   # push lpAddress (fixed)
                + b"\xb8" + _u32(va)                     # mov eax,VirtualAlloc
                + b"\xff\xd0"                            # call eax (stdcall: callee cleans)
                + b"\x61"                                 # popad
                + inj.overwrite)
        stub_len = len(body) + 5
        cave = self._find_cave(stub_len)
        self._check_site(site, inj.overwrite, "arena bootstrap")
        self.mem.write(cave, body + b"\xe9" + self._rel32(cave + len(body), site + 5))
        self.mem.write(site, b"\xe9" + self._rel32(site + 5, cave))
        if on_wait:
            on_wait()
        try:
            deadline = time.time() + timeout
            while time.time() < deadline:
                if self._mapped(base):
                    break
                time.sleep(0.05)
        finally:
            self.mem.write(site, inj.overwrite)      # unhook first, always
            self.mem.write(cave, b"\xcc" * stub_len)
        row = self._mapped(base)
        if not row:
            raise PatchError(
                "VirtualAlloc did not run. The springboard sits on a per-frame path, so "
                "this means the game is not advancing frames — Terraria pauses in "
                "single-player whenever its window loses focus. Focus the game and try "
                "again.")
        if "w" not in row.split()[1] or "x" not in row.split()[1]:
            raise PatchError(f"arena is not RWX: {row}")
        self.mem.write(base + self.ARENA_MAGIC_OFF, self.ARENA_MAGIC)
        self._arena = base
        self._save_state()
        return base

    def slot_for(self, name: str, site: int = 0) -> int:
        """Where a given injection's stub lives in the arena.

        Decided by name and site index, not by searching, so it is the same address every
        time and two injections cannot be given overlapping space. Enabling, disabling and
        re-enabling all land on the same bytes.
        """
        order = sorted(INJECTIONS)
        if name not in order:
            raise PatchError(f"no arena slot for unknown injection {name!r}")
        if not 0 <= site < self.ARENA_MAX_SITES:
            raise PatchError(f"{name}: site {site} is past ARENA_MAX_SITES")
        index = order.index(name) * self.ARENA_MAX_SITES + site
        off = self.ARENA_STUBS_OFF + index * self.ARENA_SLOT
        if off + self.ARENA_SLOT > self.ARENA_MAGIC_OFF:
            raise PatchError(f"{name}: arena slot {index} runs past the arena")
        return self.arena() + off

    def _arena_ok(self, base: int) -> bool:
        """Is `base` still a mapped arena of ours? Checks the stamp, not just the map --
        a plain address could be anything by the time we look again."""
        row = self._mapped(base)
        if not row:
            return False
        try:
            return self.mem.read(base + self.ARENA_MAGIC_OFF,
                                 len(self.ARENA_MAGIC)) == self.ARENA_MAGIC
        except Exception:
            return False

    def _find_arena(self) -> int | None:
        """An arena this process was already given, found by its stamp."""
        with open(f"/proc/{self.mem.pid}/maps") as f:
            for ln in f:
                q = ln.split()
                a, b = (int(v, 16) for v in q[0].split("-"))
                if b - a != self.ARENA_SIZE or "w" not in q[1] or "x" not in q[1]:
                    continue
                if self._arena_ok(a):
                    return a
        return None

    def _record_value(self, name: str, value: float | int | None) -> None:
        """Remember the value last applied for a cheat so the UI can restore it."""
        spec = _VALUE_SPECS.get(name)
        if spec is not None:
            self._values[name] = value if value is not None else spec.default

    # --- anchor resolution -------------------------------------------------
    def _exec_regions(self, writable: bool = False):
        """Executable mappings, as (start, end). ``writable`` keeps only those the CPU may
        also write -- which is almost none of them: a cave is borrowed padding inside
        somebody else's read-execute mapping. See ``Injection.writes_cave``.

        **Our own arena is excluded.** It is RWX, VirtualAlloc hands it back zero-filled,
        and disabling a stub scrubs its cave to 0xCC -- so to a scan looking for cold runs
        it is the most attractive cave in the process. It handed a slice of the extractor's
        own stub to the next injection enabled, one wrote over the other, and the game died
        executing the splice. Memory we placed something in is never padding.
        """
        skip = None
        if self._arena:
            skip = (self._arena, self._arena + self.ARENA_SIZE)
        out = []
        with open(f"/proc/{self.mem.pid}/maps") as f:
            for line in f:
                p = line.split()
                if "x" not in p[1]:
                    continue
                if writable and "w" not in p[1]:
                    continue
                if (p[5] if len(p) > 5 else "").startswith("/dev/"):
                    continue
                a, b = (int(v, 16) for v in p[0].split("-"))
                if skip and a < skip[1] and skip[0] < b:
                    continue
                out.append((a, b))
        return out

    def _scan(self, anchor_key: str, build: str | None = None) -> list[int]:
        """Every address matching the anchor. Scans on the pattern's longest fixed run,
        then verifies the full (possibly wildcarded) pattern.

        An anchor may carry per-build variants; each is tried in turn and the first that
        matches wins. Trying them all is deliberate — see ``Anchor.candidates``.
        """
        regions = None
        for pat in ANCHORS[anchor_key].candidates(build):
            seed_off, seed = pat.seed()
            if regions is None:
                regions = [(start, self.mem.read(start, end - start))
                           for start, end in self._exec_regions()]
            hits = []
            for start, buf in regions:
                i = buf.find(seed)
                while i != -1:
                    pos = i - seed_off
                    if pat.matches(buf, pos):
                        hits.append(start + pos)
                    i = buf.find(seed, i + 1)
            if hits:
                return hits
        return []

    def resolution(self, anchor_key: str, build: str | None = None) -> Resolution:
        """Resolve an anchor into a Resolution, with a reason that states what was
        observed and nothing more.

        Several matches is normal, not an error: mono can JIT one method into more than
        one arena, and the copies are identical where we patch. Only an anchor declared
        ``unique`` treats that as a failure.
        """
        anchor = ANCHORS[anchor_key]
        verified = build is not None and build in anchor.verified
        sites = self._sites.get(anchor_key) or self._scan(anchor_key, build)
        if not sites:
            return Resolution((), False,
                              f"anchor {anchor_key!r} matched nothing — the method may not "
                              "be JIT-compiled yet, or this build has moved it", verified)
        if anchor.unique and len(sites) > 1:
            return Resolution(tuple(sites), False,
                              f"anchor {anchor_key!r} matched {len(sites)} sites but must "
                              "be unique", verified)
        self._sites[anchor_key] = list(sites)
        return Resolution(tuple(sites), True, "", verified)

    def _resolve_sites(self, anchor_key: str) -> list[int]:
        """Every site to patch, or raise with the resolution's own reason."""
        res = self.resolution(anchor_key)
        if not res.available:
            raise PatchError(res.reason)
        return list(res.sites)

    def _resolve(self, anchor_key: str) -> int:
        """First matching site — for call targets and single-site injections. Identical
        JIT copies are interchangeable as call destinations."""
        return self._resolve_sites(anchor_key)[0]

    def _resolve_all(self, anchor_key: str) -> list[int]:
        """Every match, for anchors that intentionally match structural twins (see
        ``Injection.multi``). Deliberately uncached: one scan per enable is cheap and the
        site set can differ across re-JITs."""
        hits = self._scan(anchor_key)
        if not hits:
            raise PatchError(self.resolution(anchor_key).reason)
        return hits

    # --- code cave / injection --------------------------------------------
    def _find_cave(self, size: int, claimed: list[int] | None = None,
                   writable: bool = False) -> int:
        """Find ``size`` bytes of executable padding for a stub — we *borrow* space
        rather than allocate our own.

        We scan the JIT's executable regions for a run of int3 (0xCC, preferred) or
        zero, which is the alignment padding emitted between methods. int3 is chosen
        because it traps if ever executed, so a long 0xCC run is almost certainly
        unreachable filler; 0x00 is the weaker fallback (it decodes as a real
        instruction and a long zero run *could* be live data).

        Why borrowing is safe *enough* here — and where it stops being safe:

        - mono JITs lazily into code-manager chunks with a forward/bump allocator, so
          interstitial alignment padding inside an already-emitted chunk is not put
          back on any free list and won't be handed to a later method. wine-mono is
          the old .NET-Framework runtime: non-tiered, no re-JIT, effectively no code
          unloading. So the borrowed bytes are stable for the process lifetime, and
          JIT churn (front-loaded at first-call) grows the pool forward, not into us.
        - It is still a heuristic, not a guarantee. The failure mode is a clobbered
          stub -> a crash, which is cheaply recoverable: disable restores the site,
          a restart clears everything, and we re-derive by AOB each session.

        ``writable`` is for a stub whose own code writes inside its cave. Almost every
        executable mapping here is read-execute -- the caves this finds land in code
        sections of the game's own DLLs -- so such a stub faults on its first run even
        though installing it succeeded, because ``/proc/pid/mem`` ignores page protection
        and the CPU does not. Asking for a writable cave usually finds nothing, which is
        the honest answer: put the mutable state somewhere else, or design the write out
        (see ``_ore_extract_body``, which moved its re-entrancy guard onto the stack).

        RISK SCALES WITH ``size``. Real alignment gaps are small (a 16-byte-aligned
        gap is <=15 bytes); a large request can only be met by a long cold run, which
        is both rarer AND more likely to be actual data than incidental padding. So
        this is a SMALL-STUB-ONLY technique. When we grow past it, DO NOT grow the
        cave — allocate instead: keep the injected footprint minimal (the 5-byte site
        jump + a tiny springboard) and put the routine in memory we allocate
        (VirtualAllocEx / mmap-via-ptrace). Triggers to graduate to an alloc backend:
        a second/third injection cheat (extract a reusable Hook/Detour with a
        borrow-or-allocate backend chosen by stub size), a stub too big for a gap, or
        a 64-bit port (rel32 can't reach a far allocation -> a 2-stage hook needs a
        small nearby cave anyway). See ce/REACH_FINDINGS.md.
        """
        want = size + 4
        claimed = claimed or []
        for pad in (b"\xcc", b"\x00"):
            needle = pad * want
            for start, end in self._exec_regions(writable=writable):
                buf = self.mem.read(start, end - start)
                i = buf.find(needle)
                while i != -1:
                    cave = start + i + 2       # small margin into the run
                    # Skip anything already occupied. `claimed` covers this pass (a
                    # multi-site injection takes one cave per site); `taken` covers stubs
                    # installed by earlier enables, which `claimed` never saw. Handing out
                    # occupied space writes one stub over another, and what runs afterwards
                    # is the splice -- a corrupted register, then a crash somewhere else
                    # entirely.
                    busy = [(c, size) for c in claimed] + self._installed_caves()
                    if not any(cave < c + n and c < cave + size for c, n in busy):
                        return cave
                    i = buf.find(needle, i + 1)
        raise PatchError("no code cave found for the injection stub")

    def _installed_caves(self) -> list[tuple[int, int]]:
        """``(address, length)`` of every stub currently installed, from our own record.

        A cave is only free if nothing of ours is in it. `_find_cave` looks for cold bytes,
        and a disabled stub's cave is scrubbed to 0xCC -- which is exactly what it hunts
        for -- so without this an enable/disable cycle turns a used cave into bait.
        """
        out = []
        for rec in self._inj.values():
            n = rec.get("stub_len") or 0
            for site in rec.get("sites") or ():
                cave = site.get("cave")
                if cave and n:
                    out.append((cave, n))
        return out

    @staticmethod
    def _rel32(src_after: int, target: int) -> bytes:
        """Encode a rel32 for a 5-byte jmp at ``src_after-5`` to ``target``. Packed as
        unsigned two's complement so it is correct regardless of jump direction."""
        return struct.pack("<I", (target - src_after) & 0xFFFFFFFF)

    def _call_target(self, anchor_key: str, call_off: int) -> int:
        """Entry point of the method invoked by the ``call rel32`` at ``call_off`` within
        an anchor match: ``(site + 5) + rel32``.

        Cheaper and steadier than anchoring each callee's own prologue — these call sites
        are already anchored and verified for other cheats.
        """
        site = self._resolve(anchor_key) + call_off
        rel = struct.unpack("<i", self.mem.read(site + 1, 4))[0]
        return (site + 5 + rel) & 0xFFFFFFFF

    def _teleport_stub_body(self, inj: Injection) -> bytes:
        """Build the managed-call stub body: resolve the call target from ``call_anchor``
        and bake the local player object base. The Player object base (Teleport's ``this``)
        is ``statLife_addr - STATLIFE_FROM_OBJ``. Uses ``resolve_local_player`` — the
        authoritative ``Main.player[myPlayer]`` — because the scan can also return inert
        load-time snapshots; falls back to the scan only if the AOB resolve is unavailable
        (e.g. the synthetic test image)."""
        target = self._resolve(inj.call_anchor) - inj.call_target_off
        blk = resolve_local_player(self.mem) or self._players()[0]
        player_base = blk.life_addr - STATLIFE_FROM_OBJ
        return _teleport_body(player_base, target)

    def _check_site(self, site: int, expect: bytes, what: str) -> None:
        """Refuse to write a jump over bytes that are not what we will put back.

        A 5-byte jump is written over live code and the original is replayed from
        ``overwrite``. If the address is wrong -- an off-by-offset, a stale record from a
        previous process -- both halves are wrong: the jump lands mid-instruction and the
        restore leaves ``overwrite`` somewhere it never belonged. That is not a crash we
        can diagnose afterwards, because the evidence is the corruption itself.

        Checking costs one read. It caught nothing for a year and then caught a jump
        written 0x15 bytes early into ``Player.Update``'s dead-check, which killed the
        game on the next frame.
        """
        found = self.mem.read(site, len(expect))
        if found != expect:
            raise PatchError(
                f"{what}: bytes at 0x{site:08X} are {found.hex(' ')}, expected "
                f"{expect.hex(' ')} — refusing to patch an address that is not the site "
                "it was resolved for")

    def _enable_injection(self, inj: Injection, value: int) -> None:
        if inj.build_body is not None:                     # stub built from live state
            body = inj.build_body(self, inj)
        elif inj.call_anchor is not None:                  # managed-call injection
            body = self._teleport_stub_body(inj)
        else:
            body = inj.make_body(int(value))               # injected code
            if inj.rerun_overwrite:
                body += inj.overwrite                      # then re-run the displaced original
        stub_len = len(body) + 5                        # + jmp back (rel32)
        # Idempotent re-apply: when already installed, reuse the recorded sites/caves and
        # only rewrite the stub (e.g. a live value change). We must NOT re-resolve the
        # anchor here — some injection sites overlap their own anchor bytes (the loot
        # cheat patches inside the pattern), so a pristine scan would find nothing once
        # the jump is in place. A fresh resolve happens only on the first enable.
        prev_sites = self._inj.get(inj.name, {}).get("sites", [])
        if prev_sites:
            injects = [s["inject"] for s in prev_sites]
            caves = [s["cave"] for s in prev_sites]
        else:
            bases = self._resolve_all(inj.anchor) if inj.multi else [self._resolve(inj.anchor)]
            injects = [b + inj.inject_off for b in bases]
            caves = []
            for i in range(len(injects)):
                if inj.arena:
                    if stub_len > self.ARENA_SLOT:
                        raise PatchError(
                            f"{inj.name}: stub is {stub_len} bytes, larger than the "
                            f"{self.ARENA_SLOT}-byte arena slot")
                    caves.append(self.slot_for(inj.name, i))
                else:
                    caves.append(self._find_cave(stub_len, caves,
                                                 writable=inj.writes_cave))
        sites = []
        for inject, cave in zip(injects, caves):
            back = inject + len(inj.overwrite)          # lands on the byte after ours
            stub = body + b"\xe9" + self._rel32(cave + stub_len, back)
            # Only on a first enable: a re-apply is writing over its own jump, and the
            # displaced bytes live in the stub by then, not at the site.
            if not prev_sites:
                self._check_site(inject, inj.overwrite, f"enable {inj.name}")
            self.mem.write(cave, stub)
            self.mem.write(inject, b"\xe9" + self._rel32(inject + 5, cave))
            sites.append({"inject": inject, "cave": cave})
        self._apply_edits(inj.edits, on=True)
        self._inj[inj.name] = {"sites": sites, "stub_len": stub_len}
        self._save_state()

    def ore_queue(self) -> int | None:
        """Address of the extractor's queue, or None when it has no arena yet.

        The queue is at a fixed offset in our own arena rather than at the tail of a
        borrowed cave, so it is simply an address -- no derivation from stub length, and
        no risk of drifting away from what the stub reads.
        """
        return None if not self._arena else self._arena + ORE_QUEUE_OFF

    def ore_armed(self) -> bool:
        """Is anything queued for the stub to mine?

        Only this side writes the queue, so this reports what *we* last set. Whether those
        tiles actually got mined is answered by looking at the tiles.
        """
        q = self.ore_queue()
        return bool(q and self.mem.read_i32(q))

    def ore_arm(self, tiles) -> int:
        """Queue up to :data:`ORE_MAX_BATCH` tiles. Returns how many were taken.

        The count is written **last**, so the game can never see a count that covers
        coordinates which are only half written -- the stub would mine whatever garbage
        happened to be there, and mining the wrong tile cannot be undone.
        """
        q = self.ore_queue()
        if q is None:
            return 0
        batch = list(tiles)[:ORE_MAX_BATCH]
        if not batch:
            return 0
        self.mem.write(q + 4, b"".join(struct.pack("<ii", int(x), int(y))
                                       for x, y in batch))
        self.mem.write(q, struct.pack("<i", len(batch)))
        return len(batch)

    def ore_disarm(self) -> bool:
        """Stop the stub mining. A queue left armed is re-mined on every swing."""
        q = self.ore_queue()
        if q is None:
            return False
        self.mem.write(q, struct.pack("<i", 0))
        return True

    def _disable_injection(self, inj: Injection) -> None:
        rec = self._inj.get(inj.name)
        if rec:
            sites, stub_len = rec["sites"], rec.get("stub_len", 0)
        else:  # no record (rare): fall back to a fresh resolve of every site
            bases = self._resolve_all(inj.anchor) if inj.multi else [self._resolve(inj.anchor)]
            sites = [{"inject": b + inj.inject_off, "cave": 0} for b in bases]
            stub_len = 0
        for s in sites:
            self.mem.write(s["inject"], inj.overwrite)  # restore original bytes
            if s.get("cave"):
                self.mem.write(s["cave"], b"\xcc" * stub_len)   # scrub the stub
        if inj.arena and self.ore_queue():
            self.ore_disarm()          # an armed queue would outlive the stub
        self._apply_edits(inj.edits, on=False)
        self._inj.pop(inj.name, None)
        self._save_state()

    def _apply_edits(self, edits, *, on: bool) -> None:
        """Write every edit at every site its anchor resolves to (a method can be JIT'd
        into more than one arena; see spec 030)."""
        for e in edits:
            for base in self._resolve_sites(e.anchor):
                self.mem.write(base + e.off, e.patched if on else e.orig)

    def _injection_enabled(self, inj: Injection) -> bool:
        """Ground truth, the same as the cheat path: the byte at the site.

        A cold cache is not evidence of anything. This used to return False when neither
        the state file nor the site cache had an entry, so a fresh process reported every
        injection off no matter what was installed in the game -- and the caches are cold
        in exactly the case the answer matters, a CLI run against a game some other
        process patched.
        """
        rec = self._inj.get(inj.name)
        if rec and rec.get("sites"):
            sites = [s["inject"] for s in rec["sites"]]
        elif self._sites.get(inj.anchor):
            sites = [b + inj.inject_off for b in self._sites[inj.anchor]]
        else:
            try:
                sites = [b + inj.inject_off for b in self._resolve_sites(inj.anchor)]
            except PatchError:
                return False                  # not JIT-compiled yet: nothing to be on
        return any(self.mem.read(s, 1) == b"\xe9" for s in sites)

    # --- apply / toggle ----------------------------------------------------
    def _players(self):
        blocks = find_players(self.mem)
        if not blocks:
            raise PatchError("no player found")
        return blocks

    def _set_value(self, cheat: Cheat, on: bool, override: float | int | None = None):
        if cheat.value_off is None:
            return
        if on:
            val = cheat.on_value if override is None else override
        else:
            val = cheat.off_value
        raw = struct.pack("<f", float(val)) if cheat.value_kind == "f32" \
            else struct.pack("<i", int(val))
        for b in self._players():
            self.mem.write(b.life_addr + cheat.value_off, raw)

    def enable(self, name: str, value: float | int | None = None) -> None:
        """Patch the code site and set the field. ``value`` overrides the cheat's
        default ``on_value`` (ignored for cheats that carry no value, e.g. fast_place);
        for an injection it is the forced range. Runs under the state lock so concurrent
        toggles serialize on the shared state file (see ``_locked``)."""
        with self._locked():
            if name in INJECTIONS:
                spec = _VALUE_SPECS.get(name)
                v = int(value) if value is not None else int(spec.default if spec else 30)
                self._enable_injection(INJECTIONS[name], v)
                self._enabled.add(name)
                self._record_value(name, v)
                self._save_state()
                profile.set_cheat(name, True, self._values.get(name))
                return
            cheat = CHEATS[name]
            # Patch EVERY copy: mono can JIT one method into more than one arena and we
            # cannot tell which copy executes. The copies are identical where we patch,
            # so a stale one is inert and the live one takes effect.
            sites = [b + cheat.patch_off for b in self._resolve_sites(cheat.anchor)]
            if cheat.make_patched is not None:        # tunable in-place patch (value -> bytes)
                spec = _VALUE_SPECS.get(name)
                v = value if value is not None else (spec.default if spec else 1)
                blob = cheat.make_patched(int(v))
                for site in sites:
                    self.mem.write(site, blob)
                self._record_value(name, v)
            else:
                for site in sites:
                    self.mem.write(site, cheat.patched)
                self._set_value(cheat, on=True, override=value)
                self._record_value(name, value)
            self._enabled.add(name)
            self._save_state()
            profile.set_cheat(name, True, self._values.get(name))

    def disable(self, name: str) -> None:
        with self._locked():
            if name in INJECTIONS:
                self._disable_injection(INJECTIONS[name])
                self._enabled.discard(name)
                self._save_state()
                profile.set_cheat(name, False)
                return
            cheat = CHEATS[name]
            for base in self._resolve_sites(cheat.anchor):
                # restore original code / immediate at every copy we patched
                self.mem.write(base + cheat.patch_off, cheat.orig)
            if cheat.make_patched is None:
                self._set_value(cheat, on=False)
            self._enabled.discard(name)
            self._save_state()
            profile.set_cheat(name, False)

    def is_enabled(self, name: str) -> bool:
        """Ground truth: read the bytes at the patch site.

        Resolves the anchor (cached; scans once on a cold cache) rather than only trusting
        the cache, so status reflects the ACTUAL memory even after a state-file race dropped
        the entry — the anchors wildcard their patched bytes, so the resolve succeeds
        whether or not the cheat is applied. Returns False if the method isn't JIT-compiled
        yet (anchor absent)."""
        if name in INJECTIONS:
            return self._injection_enabled(INJECTIONS[name])
        cheat = CHEATS[name]
        try:
            bases = self._resolve_sites(cheat.anchor)
        except PatchError:
            return False
        # ANY patched copy counts as on: we cannot tell which copy executes, and disable
        # reverts them all, so "any" and "all" only differ mid-operation.
        for base in bases:
            site = base + cheat.patch_off
            if cheat.make_patched is not None:       # tunable: enabled when != original
                if self.mem.read(site, len(cheat.orig)) != cheat.orig:
                    return True
            elif self.mem.read(site, len(cheat.patched)) == cheat.patched:
                return True
        return False

    def status(self) -> dict[str, bool]:
        return {name: self.is_enabled(name) for name in PATCH_CATALOG}

    def _anchor_key(self, name: str) -> str:
        return (INJECTIONS[name].anchor if name in INJECTIONS else CHEATS[name].anchor)

    def probe(self, build: str | None = None) -> dict[str, dict]:
        """Resolve every cheat against the running build, patching nothing (spec 036).

        Unlike ``details`` this reports what a *scan* finds, not what is currently
        applied, because the question it answers is "would these patterns still match on
        this build" — and an applied injection overwrites its own anchor, which would
        otherwise read as a pass for the wrong reason. Applied cheats are therefore
        reported as resolving, since they demonstrably did.
        """
        out: dict[str, dict] = {}
        for name in PATCH_CATALOG:
            anchor_key = self._anchor_key(name)
            try:
                applied = self.is_enabled(name)
            except PatchError:
                applied = False
            if applied:
                out[name] = {"resolved": True, "applied": True, "sites": len(
                    (self._inj.get(name) or {}).get("sites", ())) or 1, "reason": ""}
                continue
            res = self.resolution(anchor_key, build)
            out[name] = {"resolved": bool(res.available), "applied": False,
                         "sites": len(res.sites), "reason": res.reason}
        return out

    def details(self, build: str | None = None) -> dict[str, dict]:
        """Per-cheat availability for the UI: is it applied, can it be applied here, was
        its AOB ever verified on this build, and if it cannot be applied, why.

        An applied cheat always reports available even though a fresh scan would miss it:
        an injection's anchor is overwritten by its own jump once installed, so the scan
        is only meaningful while the cheat is off.
        """
        out: dict[str, dict] = {}
        for name in PATCH_CATALOG:
            anchor_key = self._anchor_key(name)
            verified = build is not None and build in ANCHORS[anchor_key].verified
            try:
                on = self.is_enabled(name)
            except PatchError:
                on = False
            if on:
                # For an applied cheat the meaningful count is what is installed, not what
                # the anchor scan happens to have cached: an injection applied in an
                # earlier process leaves this one's scan cache empty, which read as
                # "0 sites" for a cheat patched at four of them.
                installed = len((self._inj.get(name) or {}).get("sites", ()))
                out[name] = {"on": True, "available": True, "verified": verified,
                             "reason": "",
                             "sites": installed or len(self._sites.get(anchor_key, ()))}
                continue
            try:
                res = self.resolution(anchor_key, build)
            except PatchError as e:
                out[name] = {"on": False, "available": False, "verified": verified,
                             "reason": str(e), "sites": 0}
                continue
            out[name] = {"on": False, "available": res.available, "verified": res.verified,
                         "reason": res.reason, "sites": len(res.sites)}
        return out

    def values(self) -> dict[str, float]:
        """Value per valued cheat, preferring the live per-pid value, then the saved
        cross-session profile, then the spec default. The profile fallback means the GUI
        shows the user's saved values on a FRESH game (before/independent of restore),
        instead of resetting the spinboxes to defaults."""
        saved = profile.cheats()
        out = {}
        for name, spec in _VALUE_SPECS.items():
            if name in self._values:
                out[name] = self._values[name]
            elif saved.get(name) is not None:
                out[name] = saved[name]
            else:
                out[name] = spec.default
        return out
