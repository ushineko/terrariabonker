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

import json
import os
import struct
from dataclasses import dataclass

from terrariabonker.locate import find_players

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
    # ResetEffects: blockRange reset (mov [edi+9F8],0), fld1, pickSpeed reset
    # (fstp [edi+8D8]) sit adjacent — one anchor covers reach + mining.
    "reset_block": _pat("C7 87 F8 09 00 00 00 00 00 00 D9 E8 D9 9F D8 08 00 00"),
    # ApplyItemTime(Item,float): the fmulp … cvttsd2si … max(edi,1) tail.
    "place": _pat("DE C9 DD 5D F0 F2 0F 10 45 F0 F2 0F 2C C8 8B F9 85 C0 7E 0A "
                  "B8 01 00 00 00 3B F8 0F 4C F8"),
    # TileReachCheckSettings.GetRanges(this, out x, out y). Prologue + mono type-init
    # + the tileRangeX read and first imul/store, with the ASLR'd immediates
    # (type-init thunk, static addr) wildcarded to make it unique. Starts at the method
    # base so the injection offset (0xCA) is measured from here.
    "getranges": _pat("55 8B EC 53 57 56 83 EC 1C 8B 5D 08 8B 75 0C 8B 7D 10 "
                      "B8 ?? ?? ?? ?? F7 00 01 00 00 00 74 05 E8 ?? ?? ?? ?? "
                      "8B 05 ?? ?? ?? ?? 8B 0B 0F AF C1 89 06"),
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
}


@dataclass(frozen=True)
class Injection:
    """A code-cave cheat: some cheats can't be done in place because they need to
    write more bytes than the site has room for. We anchor a method, overwrite a few
    bytes at an injection point with a jump to a code cave (a run of executable
    padding), run our stub there, then jump back. Used for ``tool_reach`` — forcing
    the two outputs of ``TileReachCheckSettings.GetRanges`` (mining + interaction)."""
    name: str
    label: str
    anchor: str          # key into ANCHORS (the method prologue)
    inject_off: int      # offset from the anchor to the injection point
    overwrite: bytes     # the original bytes there (re-run inside the stub)
    note: str = ""


INJECTIONS: dict[str, Injection] = {
    # Inject just before GetRanges' epilogue, where esi=out_x ptr, edi=out_y ptr are
    # still live. Overwrite `lea esp,[ebp-0C]; pop esi; pop edi` (5 bytes) with a jump
    # to a cave that does `mov [esi],N; mov [edi],N`, re-runs those 5 bytes, and jumps
    # back to `pop ebx`. Forces the tile-reach output past the game's clamp.
    "tool_reach": Injection(
        "tool_reach", "Tool + interaction reach (GetRanges)", "getranges",
        0xCA, _b("8D 65 F4 5E 5F"),
        note="forces the GetRanges output so mining/tool use and chest/sign reach "
             "extend (code cave); a game restart clears it"),
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
        self._load_state()

    # --- state persistence -------------------------------------------------
    def _load_state(self):
        try:
            with open(_STATE) as f:
                s = json.load(f)
            if s.get("pid") == self.mem.pid:     # same process -> reuse
                self._sites = {k: int(v) for k, v in s.get("sites", {}).items()}
                self._enabled = set(s.get("enabled", []))
                self._inj = {k: {kk: int(vv) for kk, vv in v.items()}
                             for k, v in s.get("inj", {}).items()}
        except (OSError, ValueError):
            pass

    def _save_state(self):
        os.makedirs(os.path.dirname(_STATE), exist_ok=True)
        with open(_STATE, "w") as f:
            json.dump({"pid": self.mem.pid, "sites": self._sites,
                       "enabled": sorted(self._enabled), "inj": self._inj}, f)

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

    # --- code cave / injection --------------------------------------------
    def _find_cave(self, size: int) -> int:
        """Find ``size`` bytes of executable padding for a stub. mono's JIT leaves
        runs of int3 (0xCC) or zero between methods; those are safe to borrow."""
        want = size + 4
        for pad in (b"\xcc", b"\x00"):
            needle = pad * want
            for start, end in self._exec_regions():
                buf = self.mem.read(start, end - start)
                i = buf.find(needle)
                if i != -1:
                    return start + i + 2        # small margin into the run
        raise PatchError("no code cave found for the reach injection")

    @staticmethod
    def _rel32(src_after: int, target: int) -> bytes:
        """Encode a rel32 for a 5-byte jmp at ``src_after-5`` to ``target``. Packed as
        unsigned two's complement so it is correct regardless of jump direction."""
        return struct.pack("<I", (target - src_after) & 0xFFFFFFFF)

    def _enable_injection(self, inj: Injection, value: int) -> None:
        base = self._resolve(inj.anchor)
        inject = base + inj.inject_off
        back = inject + len(inj.overwrite)              # lands on the byte after ours
        n = struct.pack("<i", int(value))
        body = b"\xc7\x06" + n + b"\xc7\x07" + n + inj.overwrite   # mov[esi]/[edi]; orig
        stub_len = len(body) + 5                        # + jmp back (rel32)
        prev = self._inj.get(inj.name)
        cave = prev["cave"] if prev else self._find_cave(stub_len)
        stub = body + b"\xe9" + self._rel32(cave + stub_len, back)
        self.mem.write(cave, stub)
        self.mem.write(inject, b"\xe9" + self._rel32(inject + 5, cave))
        self._inj[inj.name] = {"inject": inject, "cave": cave, "stub_len": stub_len}
        self._save_state()

    def _disable_injection(self, inj: Injection) -> None:
        rec = self._inj.get(inj.name)
        inject = rec["inject"] if rec else self._resolve(inj.anchor) + inj.inject_off
        self.mem.write(inject, inj.overwrite)           # restore original bytes
        if rec and rec.get("cave"):
            self.mem.write(rec["cave"], b"\xcc" * rec.get("stub_len", 0))  # scrub
        self._inj.pop(inj.name, None)
        self._save_state()

    def _injection_enabled(self, inj: Injection) -> bool:
        rec = self._inj.get(inj.name)
        inject = rec["inject"] if rec else (
            self._sites.get(inj.anchor, 0) + inj.inject_off
            if inj.anchor in self._sites else 0)
        return bool(inject) and self.mem.read(inject, 1) == b"\xe9"

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
        for an injection it is the forced range."""
        if name in INJECTIONS:
            spec = _VALUE_SPECS.get(name)
            v = int(value) if value is not None else int(spec.default if spec else 30)
            self._enable_injection(INJECTIONS[name], v)
            self._enabled.add(name)
            self._save_state()
            return
        cheat = CHEATS[name]
        site = self._resolve(cheat.anchor) + cheat.patch_off
        self.mem.write(site, cheat.patched)
        self._set_value(cheat, on=True, override=value)
        self._enabled.add(name)
        self._save_state()

    def disable(self, name: str) -> None:
        if name in INJECTIONS:
            self._disable_injection(INJECTIONS[name])
            self._enabled.discard(name)
            self._save_state()
            return
        cheat = CHEATS[name]
        site = self._resolve(cheat.anchor) + cheat.patch_off
        self.mem.write(site, cheat.orig)          # restore original code
        self._set_value(cheat, on=False)
        self._enabled.discard(name)
        self._save_state()

    def is_enabled(self, name: str) -> bool:
        """Ground truth: read the bytes at the patch site."""
        if name in INJECTIONS:
            return self._injection_enabled(INJECTIONS[name])
        cheat = CHEATS[name]
        if cheat.anchor not in self._sites:
            return False
        site = self._sites[cheat.anchor] + cheat.patch_off
        return self.mem.read(site, len(cheat.patched)) == cheat.patched

    def status(self) -> dict[str, bool]:
        return {name: self.is_enabled(name) for name in PATCH_CATALOG}
