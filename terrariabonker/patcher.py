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


# Unique AOB anchors in executable memory (verified single-match on 1.4.5.7).
ANCHORS: dict[str, bytes] = {
    # ResetEffects: blockRange reset (mov [edi+9F8],0), fld1, pickSpeed reset
    # (fstp [edi+8D8]) sit adjacent — one anchor covers reach + mining.
    "reset_block": _b("C7 87 F8 09 00 00 00 00 00 00 D9 E8 D9 9F D8 08 00 00"),
    # ApplyItemTime(Item,float): the fmulp … cvttsd2si … max(edi,1) tail.
    "place": _b("DE C9 DD 5D F0 F2 0F 10 45 F0 F2 0F 2C C8 8B F9 85 C0 7E 0A "
                "B8 01 00 00 00 3B F8 0F 4C F8"),
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


class PatchError(RuntimeError):
    pass


class Patcher:
    """Applies/toggles the code-patch cheats on one game process."""

    def __init__(self, mem):
        self.mem = mem
        self._sites: dict[str, int] = {}      # anchor key -> resolved address
        self._enabled: set[str] = set()
        self._load_state()

    # --- state persistence -------------------------------------------------
    def _load_state(self):
        try:
            with open(_STATE) as f:
                s = json.load(f)
            if s.get("pid") == self.mem.pid:     # same process -> reuse
                self._sites = {k: int(v) for k, v in s.get("sites", {}).items()}
                self._enabled = set(s.get("enabled", []))
        except (OSError, ValueError):
            pass

    def _save_state(self):
        os.makedirs(os.path.dirname(_STATE), exist_ok=True)
        with open(_STATE, "w") as f:
            json.dump({"pid": self.mem.pid, "sites": self._sites,
                       "enabled": sorted(self._enabled)}, f)

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
        """Find the unique anchor address (cached per session)."""
        if anchor_key in self._sites:
            return self._sites[anchor_key]
        seq = ANCHORS[anchor_key]
        found = None
        for start, end in self._exec_regions():
            buf = self.mem.read(start, end - start)
            i = buf.find(seq)
            while i != -1:
                if found is not None:
                    raise PatchError(f"anchor {anchor_key!r} is not unique "
                                     "(game updated? re-derive AOBs with CE)")
                found = start + i
                i = buf.find(seq, i + 1)
        if found is None:
            raise PatchError(f"anchor {anchor_key!r} not found "
                             "(game updated? re-derive AOBs with CE)")
        self._sites[anchor_key] = found
        return found

    # --- apply / toggle ----------------------------------------------------
    def _players(self):
        blocks = find_players(self.mem)
        if not blocks:
            raise PatchError("no player found")
        return blocks

    def _set_value(self, cheat: Cheat, on: bool):
        if cheat.value_off is None:
            return
        val = cheat.on_value if on else cheat.off_value
        raw = struct.pack("<f", float(val)) if cheat.value_kind == "f32" \
            else struct.pack("<i", int(val))
        for b in self._players():
            self.mem.write(b.life_addr + cheat.value_off, raw)

    def enable(self, name: str) -> None:
        cheat = CHEATS[name]
        site = self._resolve(cheat.anchor) + cheat.patch_off
        self.mem.write(site, cheat.patched)
        self._set_value(cheat, on=True)
        self._enabled.add(name)
        self._save_state()

    def disable(self, name: str) -> None:
        cheat = CHEATS[name]
        site = self._resolve(cheat.anchor) + cheat.patch_off
        self.mem.write(site, cheat.orig)          # restore original code
        self._set_value(cheat, on=False)
        self._enabled.discard(name)
        self._save_state()

    def is_enabled(self, name: str) -> bool:
        """Ground truth: read the bytes at the patch site."""
        cheat = CHEATS[name]
        if cheat.anchor not in self._sites:
            return False
        site = self._sites[cheat.anchor] + cheat.patch_off
        return self.mem.read(site, len(cheat.patched)) == cheat.patched

    def status(self) -> dict[str, bool]:
        return {name: self.is_enabled(name) for name in CHEATS}
