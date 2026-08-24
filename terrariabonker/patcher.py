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
    """
    pattern: Pattern
    verified: frozenset[str] = frozenset()
    unique: bool = False


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
    "trigger_ping": _pat("8B 4D 08 89 4C 24 04 8B 4D 0C 89 4C 24 08 89 04 24 39 00 "
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
_VERIFIED_BUILDS: tuple[str, ...] = (
    ver.KNOWN_BUILD_KEY,        # 1.4.5.8+24893155 — see the 2026-08-23 note below
    # The build these AOBs were originally derived against.
    "1.4.5.7+24825745",
    # 2026-08-23, and a key to distrust: this one is a *mix*. Steam downloaded 1.4.5.8 at
    # 12:19 while a 1.4.5.7 process kept running, so the version came from the running
    # game and the buildid from the already-updated manifest, describing a build that
    # never existed. It is kept because the panel really did record verifications under
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
    # 2026-08-24: derived on 1.4.5.8 but not yet watched working, so it claims nothing.
    "pick_tile": (),
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


# The extractor's slot, laid out at the tail of its own stub: a flag the game side
# clears when it consumes a tile, then the tile's x and y. One tile at a time on purpose —
# a cave is *borrowed* padding (see _find_cave), so its size is the risk, and a queue big
# enough for a vein would be a far bigger borrow than a 12-byte slot.
ORE_SLOT_BYTES = 12


def _ore_extract_body(patcher, inj) -> bytes:
    """Mine one queued tile per ``PickTile`` call, from inside ``PickTile`` itself.

    The unprivileged side does the thinking — read the tile map, flood-fill the vein,
    decide what may be taken — and writes a single coordinate here. This stub does one
    dumb thing with it: call ``Player.PickTile(this, x, y, 100, -1)``, which is the same
    call the game makes when you swing a pickaxe, so drops, framing, lighting and the
    ``CanKillTile`` check all happen exactly as they normally would.

    **Why hook PickTile rather than a per-frame method.** The call has to happen on the
    game thread, and PickTile already runs there — while you are mining, which is the only
    time this cheat should be doing anything. Nothing happens when you put the pickaxe
    away, and no new hot path is patched.

    **Re-entrancy is handled by an explicit in-flight state, not by clearing a flag.**
    Calling PickTile from inside PickTile re-enters this stub, so the guard has to hold
    for the whole nested call -- not just until the queued tile has been read. Clearing a
    boolean flag on entry is *not* enough: the unprivileged side polls that flag to decide
    when to queue the next tile, so it re-arms within a poll interval while the nested
    call is still running (dust, drops, sound, net take milliseconds; the poll is 10ms).
    The nested entry then finds the flag armed again and calls PickTile once more, and so
    on -- recursion as deep as the vein is long, which overflows the game thread's stack
    and kills the process with no managed exception to show for it.

    So the slot holds a state, not a flag: 0 idle, 1 armed, 2 in flight. The stub only
    acts on 1, and its first act is to make the state 2, which it holds until the nested
    call returns. A re-entered stub sees 2, fails the ``== 1`` test and falls straight
    through to the displaced bytes. Depth is one by construction, whatever the other side
    does. State 2 also reads as "still pending" to ``ore_pending``, so the queueing loop
    waits for the tile to actually be mined rather than for it to be dequeued.

    **Stack alignment.** Mono's x86 JIT builds 16-byte-aligned frames assuming esp is
    12 (mod 16) at entry -- PickTile's own prologue is proof: 4 pushes + ``sub esp,0x7C``
    is 140 bytes, which lands the frame on 16 only from that start. The pushad and the
    args do not preserve that, so esp is realigned before the pushes and restored from
    ebx afterwards.

    The stub finds its own data with ``call/pop``: the cave address is not known when the
    body is built (the cave is chosen from the body's length), so a baked absolute address
    is not available.
    """
    from terrariabonker.locate import find_localplayer_anchor

    pick_tile = patcher._resolve(inj.anchor)          # the anchor sits at the entry
    tail = find_localplayer_anchor(patcher.mem)
    if tail is None:
        raise PatchError("could not locate Main.player / Main.myPlayer")
    player_arr = patcher.mem.read_u32(tail - 0xA)
    my_player = patcher.mem.read_u32(tail - 4)
    if not (player_arr and my_player):
        raise PatchError("Main.player / Main.myPlayer are not readable")

    # Built in two passes: the data lives after the code, so the displacement from the
    # call/pop anchor is only known once the code's length is.
    def emit(delta: int) -> bytes:
        # The slot is a three-state machine, not a boolean: 0 idle, 1 armed (the
        # unprivileged side queued a tile), 2 in flight (we are inside the nested call).
        # State 2 is what makes the re-entrancy guard real -- see the docstring.
        assert 0 <= delta + 8 < 128, "slot no longer reachable with a disp8"
        # The work, so the guard's jump distance is measured rather than counted by hand.
        guarded = (b"\xff\x46" + bytes([delta])       # inc dword [esi+delta]  (1 -> 2)
                   + b"\x8b\xdc"                       # mov ebx,esp
                   + b"\x83\xe4\xf0"                   # and esp,-16   \ mono wants esp==12
                   + b"\x83\xec\x0c"                   # sub esp,12    / (mod 16) at entry
                   + b"\xa1" + _u32(player_arr)        # mov eax,[Main.player]
                   + b"\x8b\x0d" + _u32(my_player)     # mov ecx,[Main.myPlayer]
                   + b"\x8b\x44\x88\x10"               # mov eax,[eax+ecx*4+0x10]
                   + b"\x6a\xff"                       # push -1    (cap)
                   + b"\x6a\x64"                       # push 100   (pickPower)
                   + b"\xff\x76" + bytes([delta + 8])  # push [esi+delta+8]   (y)
                   + b"\xff\x76" + bytes([delta + 4])  # push [esi+delta+4]   (x)
                   + b"\x50"                            # push eax   (this)
                   + b"\xb8" + _u32(pick_tile)         # mov eax,PickTile
                   + b"\xff\xd0"                       # call eax
                   + b"\x8b\xe3"                       # mov esp,ebx (either convention)
                   + b"\x83\x66" + bytes([delta]) + b"\x00")   # and [esi+delta],0 (idle)
        assert len(guarded) < 128, "guard jump no longer fits in a short branch"
        return (b"\x60"                                     # pushad
                + b"\xe8\x00\x00\x00\x00"                  # call $+5  (push next addr)
                + b"\x5e"                                    # pop esi   (esi = here)
                + b"\x83\x7e" + bytes([delta]) + b"\x01"     # cmp dword [esi+delta],1
                + b"\x75" + bytes([len(guarded)])            # jne skip  (idle, or in flight)
                + guarded
                + b"\x61"                                    # popad  <- skip lands here
                + b"\xeb" + bytes([ORE_SLOT_BYTES])          # jmp over the slot
                + b"\x00" * ORE_SLOT_BYTES                   # state, x, y
                + inj.overwrite)                             # the displaced prologue

    probe = emit(0)
    # delta is measured from the pop's *next* instruction, which is where esi points.
    esi_at = 6                                             # pushad(1) + call(5)
    slot_at = len(probe) - ORE_SLOT_BYTES - len(inj.overwrite)
    body = emit(slot_at - esi_at)
    assert len(body) == len(probe), "two-pass emit changed length"
    return body


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


INJECTIONS: dict[str, Injection] = {
    # Inject just before GetRanges' epilogue, where esi=out_x ptr, edi=out_y ptr are
    # still live. Overwrite `lea esp,[ebp-0C]; pop esi; pop edi` (5 bytes) with a jump
    # to a cave that does `mov [esi],N; mov [edi],N`, re-runs those 5 bytes, and jumps
    # back to `pop ebx`. Forces the tile-reach output past the game's clamp.
    "tool_reach": Injection(
        "tool_reach", "Tool + interaction reach (GetRanges)", "getranges",
        0xCA, _b("8D 65 F4 5E 5F"), _force_xy,
        note="Extends mining, tool use, chests, signs and crafting stations together."),
    # Player.GrabItems: a call returns the grab range in eax, then `mov [ebp-54],eax`
    # stores it. Inject `imul eax,N` before that store to scale the pickup radius.
    # Overwrite `mov [ebp-54],eax; lea eax,[ebp-50]` (6 bytes), re-run in the stub.
    "pickup": Injection(
        "pickup", "Item pickup range (GrabItems)", "grabitems",
        0x0, _b("89 45 AC 8D 45 B0"), _imul_eax,
        note="Scales the item pickup radius."),
    # GetSpawnRate epilogue (esi=out spawnRate, edi=out maxSpawns still live). Overwrite
    # `lea esp,[ebp-0C]; pop esi; pop edi` like GetRanges; force the two outputs.
    "spawn_rate": Injection(
        "spawn_rate", "Spawn rate (GetSpawnRate)", "get_spawn_rate",
        0x1EAA, _b("8D 65 F4 5E 5F"), _force_spawn,
        note="Caps active enemies. 0 is peaceful."),
    # CommonDrop.TryDroppingItem +0x26: cap the chance denominator so drops have a
    # minimum chance (100% = guaranteed). Ported from the FearLess ReGrind table.
    "loot": Injection(
        "loot", "Drop chance floor (CommonDrop)", "trydrop",
        -7, _b("8B 4E 10 89 4C 24 04"), _cap_drop_denom,   # anchor at +0x2D -> site at +0x26
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
        note="Vanity accessory slots grant full effects, doubling your usable "
             "accessories.",
        edits=(Edit("equip_apply", 27, b"\x0a", b"\x14"),        # ApplyEquipFunctional loop
               Edit("equip_benefits", 48, b"\x0a", b"\x14"))),   # prefix/armour benefits
    # Smart-cursor search radius (spec 034). Keeps placement/tool reach at whatever the
    # user set while stopping the smart cursor from scanning a box that size every frame.
    "smart_cursor": Injection(
        "smart_cursor", "Smart cursor search radius", "smart_cursor",
        69, _b("89 46 3C 85 DB"), _shrink_smart_cursor,
        note="Don't set this too high or the game will lag.",
        rerun_overwrite=False),
    # Accessories take effect from the INVENTORY (spec 033). UpdateEquips' first loop
    # already walks all 58 slots every frame; this hooks the point where the Item* is in
    # eax and runs the accessory machinery on the ones that are accessories.
    "inventory_accs": Injection(
        "inventory_accs", "Accessories work from inventory", "inventory_scan",
        22, _b("8B 00 8B 40 6C"), None,
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
        "ore_extract", "Ore extractor (vein mining)", "pick_tile",
        0x0, _b("55 8B EC 53 57"), None,
        build_body=_ore_extract_body, rerun_overwrite=False,
        note="Mines the rest of an ore vein while you mine it. Whitelisted ores only."),
    "teleport": Injection(
        "teleport", "Map-ping teleport (TriggerPing)", "trigger_ping",
        0x0, _b("8B 4D 08 89 4C 24 04"), None,
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
        except (OSError, ValueError):
            pass

    def _save_state(self):
        os.makedirs(os.path.dirname(_STATE), exist_ok=True)
        tmp = _STATE + f".{os.getpid()}.tmp"          # atomic write: no torn reads for status
        with open(tmp, "w") as f:
            json.dump({"pid": self.mem.pid, "sites": self._sites,
                       "enabled": sorted(self._enabled), "inj": self._inj,
                       "values": self._values}, f)
        os.replace(tmp, _STATE)

    def _record_value(self, name: str, value: float | int | None) -> None:
        """Remember the value last applied for a cheat so the UI can restore it."""
        spec = _VALUE_SPECS.get(name)
        if spec is not None:
            self._values[name] = value if value is not None else spec.default

    # --- anchor resolution -------------------------------------------------
    def _exec_regions(self):
        out = []
        with open(f"/proc/{self.mem.pid}/maps") as f:
            for line in f:
                p = line.split()
                if "x" not in p[1]:
                    continue
                if (p[5] if len(p) > 5 else "").startswith("/dev/"):
                    continue
                a, b = p[0].split("-")
                out.append((int(a, 16), int(b, 16)))
        return out

    def _scan(self, anchor_key: str) -> list[int]:
        """Every address matching the anchor. Scans on the pattern's longest fixed run,
        then verifies the full (possibly wildcarded) pattern."""
        pat = ANCHORS[anchor_key].pattern
        seed_off, seed = pat.seed()
        hits = []
        for start, end in self._exec_regions():
            buf = self.mem.read(start, end - start)
            i = buf.find(seed)
            while i != -1:
                pos = i - seed_off
                if pat.matches(buf, pos):
                    hits.append(start + pos)
                i = buf.find(seed, i + 1)
        return hits

    def resolution(self, anchor_key: str, build: str | None = None) -> Resolution:
        """Resolve an anchor into a Resolution, with a reason that states what was
        observed and nothing more.

        Several matches is normal, not an error: mono can JIT one method into more than
        one arena, and the copies are identical where we patch. Only an anchor declared
        ``unique`` treats that as a failure.
        """
        anchor = ANCHORS[anchor_key]
        verified = build is not None and build in anchor.verified
        sites = self._sites.get(anchor_key) or self._scan(anchor_key)
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
    def _find_cave(self, size: int, claimed: list[int] | None = None) -> int:
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
            for start, end in self._exec_regions():
                buf = self.mem.read(start, end - start)
                i = buf.find(needle)
                while i != -1:
                    cave = start + i + 2       # small margin into the run
                    # skip a run already handed out this pass (multi-site injections
                    # allocate one cave per site and must not collide — each stub has a
                    # distinct jmp-back target).
                    if not any(cave < c + size and c < cave + size for c in claimed):
                        return cave
                    i = buf.find(needle, i + 1)
        raise PatchError("no code cave found for the injection stub")

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
            for _ in injects:
                caves.append(self._find_cave(stub_len, caves))   # distinct cave per site
        sites = []
        for inject, cave in zip(injects, caves):
            back = inject + len(inj.overwrite)          # lands on the byte after ours
            stub = body + b"\xe9" + self._rel32(cave + stub_len, back)
            self.mem.write(cave, stub)
            self.mem.write(inject, b"\xe9" + self._rel32(inject + 5, cave))
            sites.append({"inject": inject, "cave": cave})
        self._apply_edits(inj.edits, on=True)
        self._inj[inj.name] = {"sites": sites, "stub_len": stub_len}
        self._save_state()

    def ore_slot(self) -> int | None:
        """Address of the extractor's slot, or None when the cheat is off.

        The slot sits at the tail of the stub, before the displaced prologue and the jump
        back: ``stub_len`` counts the body plus that 5-byte jump, and the body ends with
        the slot, the 5 displaced bytes. Derived rather than remembered so it cannot drift
        away from what ``_ore_extract_body`` actually emits — a test pins the two together.
        """
        rec = self._inj.get("ore_extract") or {}
        sites = rec.get("sites") or []
        if not sites or not rec.get("stub_len"):
            return None
        overwrite = len(INJECTIONS["ore_extract"].overwrite)
        return sites[0]["cave"] + rec["stub_len"] - 5 - overwrite - ORE_SLOT_BYTES

    def ore_state(self) -> int:
        """The stub's slot state: 0 idle, 1 armed, 2 in flight. -1 when the cheat is off.

        See ``_ore_extract_body``: 2 is held for the whole nested ``PickTile`` call, which
        is what stops the caller re-arming mid-call and recursing into the stub.
        """
        slot = self.ore_slot()
        return -1 if slot is None else self.mem.read_i32(slot)

    def ore_pending(self) -> bool:
        """True until the queued tile has actually been mined.

        Both 1 (armed) and 2 (in flight) count as pending. Treating only 1 as pending is
        the bug that overflowed the game's stack: it reports "done" the moment the stub
        dequeues the tile, milliseconds before the mining it triggers has finished.
        """
        return self.ore_state() > 0

    def ore_queue(self, x: int, y: int) -> bool:
        """Hand one tile to the stub, if and only if it is idle.

        The state is written **last**, so the game can never see a coordinate that is only
        half written; and it is only written from 0, so a tile is never queued on top of
        one still in flight.
        """
        slot = self.ore_slot()
        if slot is None or self.mem.read_i32(slot) != 0:
            return False
        self.mem.write(slot + 4, struct.pack("<ii", int(x), int(y)))
        self.mem.write(slot, struct.pack("<i", 1))
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
        rec = self._inj.get(inj.name)
        if rec and rec.get("sites"):
            inject = rec["sites"][0]["inject"]
        elif self._sites.get(inj.anchor):
            inject = self._sites[inj.anchor][0] + inj.inject_off
        else:
            return False
        return self.mem.read(inject, 1) == b"\xe9"

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
