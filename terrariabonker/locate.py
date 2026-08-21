"""Find the Terraria Player object in memory, from scratch, no fixed address.

The Player object is GC-allocated, so its address changes between world loads.
wine-mono uses a non-moving GC, so within one world the address is stable, but a
fresh locate is needed after a reload. Rather than a pointer chain through
mono statics, this scans for the object by a signature and validates it.

Signature: six consecutive int32 that are the player's life/mana block

    statLifeMax2, statLifeMax, statLife, statMana, statManaMax, statManaMax2

confirmed by a real mono String pointer at ``statLife - 0x6C0`` (the
``Player.name`` field). The invariants below make the match specific enough that
scanning ~1.6 GB yields only the player's own copies.

Offsets are for Terraria 1.4.5.7. A game update can move them; see docs/discovery.md.
"""

from __future__ import annotations

import struct
from dataclasses import dataclass

import numpy as np

NAME_OFF = -0x6C0        # Player.name (mono String*) relative to statLife
BLOCK_LEN = 6            # ints in the life/mana signature


@dataclass
class PlayerBlock:
    """One matched player copy: the statLife address, the block, and the name."""

    life_addr: int
    stat_life_max2: int
    stat_life_max: int
    stat_life: int
    stat_mana: int
    stat_mana_max: int
    stat_mana_max2: int
    name: str

    @property
    def block(self) -> list[int]:
        return [self.stat_life_max2, self.stat_life_max, self.stat_life,
                self.stat_mana, self.stat_mana_max, self.stat_mana_max2]


def read_mono_string(mem, ptr: int) -> str | None:
    """Decode a 32-bit mono String at ``ptr`` (+8 int length, +0xC UTF-16), or None.

    Only strings of a name-plausible length with printable ASCII are accepted,
    which is what makes this a reliable validator rather than a coincidence sink.
    """
    hdr = mem.read(ptr, 12)
    if len(hdr) < 12:
        return None
    length = struct.unpack("<i", hdr[8:12])[0]
    if not (1 <= length <= 64):
        return None
    raw = mem.read(ptr + 12, length * 2)
    if len(raw) < length * 2:
        return None
    try:
        s = raw.decode("utf-16-le")
    except ValueError:
        return None
    return s if all(32 <= ord(c) < 127 for c in s) else None


def valid_block(v: list[int]) -> bool:
    """True if six ints look like a Terraria life/mana block.

    Terraria invariants do the filtering: life max is 100-500 with the permanent
    copy equal to the current cap, and mana is always a multiple of 20 up to 400
    with its permanent copy equal to the cap. Random memory rarely satisfies all.
    """
    if len(v) < BLOCK_LEN:
        return False
    m2, m, life, mana, mmax, mmax2 = v[:BLOCK_LEN]
    return (
        100 <= m <= 500 and m2 == m and 1 <= life <= m and
        20 <= mmax <= 400 and mmax % 20 == 0 and mmax2 == mmax and 0 <= mana <= mmax
    )


def find_players(mem) -> list[PlayerBlock]:
    """Scan writable memory and return every validated player copy.

    Typically returns the live ``Main.player[myPlayer]`` plus one or two inert
    load-time snapshots that share the character name. Callers that only write or
    freeze can safely act on all of them; the snapshots ignore the writes.
    """
    found: list[PlayerBlock] = []
    for start, end in mem.regions():
        buf = mem.read(start, end - start)
        n = len(buf) // 4
        if n < BLOCK_LEN:
            continue
        arr = np.frombuffer(buf[: n * 4], dtype=np.int32)
        # statLife sits at block index 2; prefilter cheaply on the max/life pair.
        maxv, life = arr[1:-2], arr[2:-1]
        cand = np.where((maxv >= 100) & (maxv <= 500) & (life >= 1) & (life <= maxv))[0]
        for i in cand.tolist():
            base = i + 2                       # index of statLife within arr
            block = arr[base - 2: base + 4].tolist()
            if not valid_block(block):
                continue
            life_addr = start + base * 4
            namep = mem.read(life_addr + NAME_OFF, 4)
            if len(namep) < 4:
                continue
            name = read_mono_string(mem, struct.unpack("<I", namep)[0])
            if name is None:
                continue
            found.append(PlayerBlock(life_addr, *block, name=name))
    return found


def pick_live(mem, players: list[PlayerBlock], samples: int = 6, dt: float = 0.08):
    """Best-effort guess at which copy is the live player.

    The live player has fields that tick (regen, buff/breath timers) while the
    snapshots are frozen. Sample statLife over a short window and prefer the one
    that moves; if nothing moves (game paused, or player idle at full HP) fall
    back to the single copy whose HP is below max, else give up and return None.
    Returns the guessed ``PlayerBlock`` or None. Freezing does not depend on this.
    """
    import time

    if not players:
        return None
    if len(players) == 1:
        return players[0]
    series = {p.life_addr: [] for p in players}
    for _ in range(samples):
        for p in players:
            series[p.life_addr].append(mem.read_i32(p.life_addr))
        time.sleep(dt)
    movers = [p for p in players if len(set(series[p.life_addr])) > 1]
    if len(movers) == 1:
        return movers[0]
    below = [p for p in players if p.stat_life < p.stat_life_max]
    if len(below) == 1:
        return below[0]
    return None
