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
ANCHORS: dict[str, Pattern] = {
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
        note="NOPs the per-frame pickSpeed reset so a low value holds"),
    "reach": Cheat(
        "reach", "Placement reach (blockRange)", "reset_block", 0,
        _b("C7 87 F8 09 00 00 00 00 00 00"), _b("90 90 90 90 90 90 90 90 90 90"),
        value_off=0x2C0, value_kind="i32", on_value=20, off_value=0,
        note="NOPs the blockRange reset; item-independent extended reach"),
    "fast_place": Cheat(
        "fast_place", "Fast placement (ApplyItemTime)", "place", 20,
        _b("B8 01 00 00 00 3B F8 0F 4C F8"), _b("BF 04 00 00 00 90 90 90 90 90"),
        value_off=None,
        note="forces itemTime=4 at the placement-timing read site"),
    # Rewrite ResetEffects' `maxMinions = 1` reset to `maxMinions = N`, so N minion slots
    # hold each frame (accessory bonuses still stack on top). patch_off=6 is the 4-byte
    # immediate; make_patched packs N there. Ported in spirit from ReGrind-style stat caps.
    "max_minions": Cheat(
        "max_minions", "Minion cap (maxMinions)", "reset_minions", 6,
        _b("01 00 00 00"), b"",                     # patched is built from the value
        make_patched=lambda n: struct.pack("<i", max(1, int(n))),
        note="raises the minion (summon) cap; a game restart clears it"),
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


INJECTIONS: dict[str, Injection] = {
    # Inject just before GetRanges' epilogue, where esi=out_x ptr, edi=out_y ptr are
    # still live. Overwrite `lea esp,[ebp-0C]; pop esi; pop edi` (5 bytes) with a jump
    # to a cave that does `mov [esi],N; mov [edi],N`, re-runs those 5 bytes, and jumps
    # back to `pop ebx`. Forces the tile-reach output past the game's clamp.
    "tool_reach": Injection(
        "tool_reach", "Tool + interaction reach (GetRanges)", "getranges",
        0xCA, _b("8D 65 F4 5E 5F"), _force_xy,
        note="extends mining, tool use, and chest/sign reach together; "
             "a game restart clears it"),
    # Player.GrabItems: a call returns the grab range in eax, then `mov [ebp-54],eax`
    # stores it. Inject `imul eax,N` before that store to scale the pickup radius.
    # Overwrite `mov [ebp-54],eax; lea eax,[ebp-50]` (6 bytes), re-run in the stub.
    "pickup": Injection(
        "pickup", "Item pickup range (GrabItems)", "grabitems",
        0x0, _b("89 45 AC 8D 45 B0"), _imul_eax,
        note="scales the item pickup range by N; a game restart clears it"),
    # GetSpawnRate epilogue (esi=out spawnRate, edi=out maxSpawns still live). Overwrite
    # `lea esp,[ebp-0C]; pop esi; pop edi` like GetRanges; force the two outputs.
    "spawn_rate": Injection(
        "spawn_rate", "Spawn rate (GetSpawnRate)", "get_spawn_rate",
        0x1EAA, _b("8D 65 F4 5E 5F"), _force_spawn,
        note="caps active enemies at N (0 = peaceful); a game restart clears it"),
    # CommonDrop.TryDroppingItem +0x26: cap the chance denominator so drops have a
    # minimum chance (100% = guaranteed). Ported from the FearLess ReGrind table.
    "loot": Injection(
        "loot", "Drop chance floor (CommonDrop)", "trydrop",
        -7, _b("8B 4E 10 89 4C 24 04"), _cap_drop_denom,   # anchor at +0x2D -> site at +0x26
        note="floors the drop chance of common enemy/grab-bag drops at N% "
             "(100 = guaranteed); a game restart clears it",
        rerun_overwrite=False, multi=True),
    # Map-ping teleport (ported from the FearLess ReGrind table). Hook Main.TriggerPing
    # at +0x2D; the stub calls Player.Teleport(this, pingX, pingY, 0, 0), warping the
    # local player to any fullscreen-map ping. No tunable value (on/off). The call target
    # is resolved via the player_teleport anchor (entry = match - 0x32). A game restart
    # clears it; re-toggle after a world/character reload (the player object base is
    # baked into the stub at enable time).
    "teleport": Injection(
        "teleport", "Map-ping teleport (TriggerPing)", "trigger_ping",
        0x0, _b("8B 4D 08 89 4C 24 04"), None,
        note="drop a fullscreen-map ping to teleport there; a game restart clears it "
             "(re-toggle after a world/character reload)",
        rerun_overwrite=False, call_anchor="player_teleport", call_target_off=0x32),
}


@dataclass(frozen=True)
class ValueSpec:
    kind: str            # "f32" | "i32"
    default: float | int
    lo: float | int
    hi: float | int
    unit: str = ""


@dataclass(frozen=True)
class PatchInfo:
    """A view-neutral description of one patch for the CLI/GUI (merges the value
    cheats and the injections into one ordered catalog)."""
    name: str
    label: str
    note: str
    value: ValueSpec | None
    kind: str            # "cheat" | "injection"


_VALUE_SPECS: dict[str, ValueSpec] = {
    "mining": ValueSpec("f32", 0.2, 0.05, 2.0, "pickSpeed · lower = faster"),
    "reach": ValueSpec("i32", 20, 0, 100, "extra tiles"),
    "tool_reach": ValueSpec("i32", 30, 1, 200, "tiles · mining & interaction"),
    "pickup": ValueSpec("i32", 50, 2, 500, "× grab range"),
    "spawn_rate": ValueSpec("i32", 15, 0, 200, "max active enemies · 0 = peaceful"),
    "loot": ValueSpec("i32", 100, 1, 100, "% min drop chance · 100 = guaranteed"),
    "max_minions": ValueSpec("i32", 10, 1, 255, "minion slots (base; +accessories)"),
}


def _build_catalog() -> dict[str, PatchInfo]:
    out: dict[str, PatchInfo] = {}
    for n, c in CHEATS.items():
        out[n] = PatchInfo(n, c.label, c.note, _VALUE_SPECS.get(n), "cheat")
    for n, inj in INJECTIONS.items():
        out[n] = PatchInfo(n, inj.label, inj.note, _VALUE_SPECS.get(n), "injection")
    return out


PATCH_CATALOG: dict[str, PatchInfo] = _build_catalog()


class PatchError(RuntimeError):
    pass


class Patcher:
    """Applies/toggles the code-patch cheats on one game process."""

    def __init__(self, mem):
        self.mem = mem
        self._sites: dict[str, int] = {}      # anchor key -> resolved address
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
                self._sites = {k: int(v) for k, v in s.get("sites", {}).items()}
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

    def _resolve(self, anchor_key: str) -> int:
        """Find the unique anchor address (cached per session). Scans on the pattern's
        longest fixed run, then verifies the full (possibly wildcarded) pattern."""
        if anchor_key in self._sites:
            return self._sites[anchor_key]
        pat = ANCHORS[anchor_key]
        seed_off, seed = pat.seed()
        found = None
        for start, end in self._exec_regions():
            buf = self.mem.read(start, end - start)
            i = buf.find(seed)
            while i != -1:
                pos = i - seed_off
                if pat.matches(buf, pos):
                    if found is not None:
                        raise PatchError(f"anchor {anchor_key!r} is not unique "
                                         "(game updated? re-derive AOBs with CE)")
                    found = start + pos
                i = buf.find(seed, i + 1)
        if found is None:
            raise PatchError(f"anchor {anchor_key!r} not found "
                             "(game updated? re-derive AOBs with CE)")
        self._sites[anchor_key] = found
        return found

    def _resolve_all(self, anchor_key: str) -> list[int]:
        """Like ``_resolve`` but returns EVERY match, for anchors that intentionally
        match structural twins (see ``Injection.multi``). Not cached — one scan per
        enable is cheap and the site set can differ across re-JITs."""
        pat = ANCHORS[anchor_key]
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
        if not hits:
            raise PatchError(f"anchor {anchor_key!r} not found "
                             "(game updated? re-derive AOBs with CE)")
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
        if inj.call_anchor is not None:                    # managed-call injection
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
        self._inj[inj.name] = {"sites": sites, "stub_len": stub_len}
        self._save_state()

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
        self._inj.pop(inj.name, None)
        self._save_state()

    def _injection_enabled(self, inj: Injection) -> bool:
        rec = self._inj.get(inj.name)
        if rec and rec.get("sites"):
            inject = rec["sites"][0]["inject"]
        elif inj.anchor in self._sites:
            inject = self._sites[inj.anchor] + inj.inject_off
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
                return
            cheat = CHEATS[name]
            site = self._resolve(cheat.anchor) + cheat.patch_off
            if cheat.make_patched is not None:        # tunable in-place patch (value -> bytes)
                spec = _VALUE_SPECS.get(name)
                v = value if value is not None else (spec.default if spec else 1)
                self.mem.write(site, cheat.make_patched(int(v)))
                self._record_value(name, v)
            else:
                self.mem.write(site, cheat.patched)
                self._set_value(cheat, on=True, override=value)
                self._record_value(name, value)
            self._enabled.add(name)
            self._save_state()

    def disable(self, name: str) -> None:
        with self._locked():
            if name in INJECTIONS:
                self._disable_injection(INJECTIONS[name])
                self._enabled.discard(name)
                self._save_state()
                return
            cheat = CHEATS[name]
            site = self._resolve(cheat.anchor) + cheat.patch_off
            self.mem.write(site, cheat.orig)          # restore original code / immediate
            if cheat.make_patched is None:
                self._set_value(cheat, on=False)
            self._enabled.discard(name)
            self._save_state()

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
            base = self._resolve(cheat.anchor)
        except PatchError:
            return False
        site = base + cheat.patch_off
        if cheat.make_patched is not None:           # tunable: enabled when != original
            return self.mem.read(site, len(cheat.orig)) != cheat.orig
        return self.mem.read(site, len(cheat.patched)) == cheat.patched

    def status(self) -> dict[str, bool]:
        return {name: self.is_enabled(name) for name in PATCH_CATALOG}

    def values(self) -> dict[str, float]:
        """Last applied value per valued cheat (persisted across GUI restarts,
        as long as the game process — and thus the state file's pid — is the same).
        Falls back to each cheat's default when nothing has been applied yet."""
        return {name: self._values.get(name, spec.default)
                for name, spec in _VALUE_SPECS.items()}
