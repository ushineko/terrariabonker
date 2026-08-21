"""The freeze engine: hold player values against the game overwriting them.

Terraria recomputes many player fields every frame (statLife on damage/regen,
derived stats from equipment). A one-shot write reverts, so cheats that must
hold a value are implemented as a high-frequency rewrite loop that beats the
game's ~60 Hz frame. Writes go to every matched player copy; the live one takes
effect, the inert snapshots ignore them.

If the world is reloaded the player addresses go stale and reads start failing;
the loop notices and re-locates rather than dying.
"""

from __future__ import annotations

import time

from terrariabonker.locate import find_players
from terrariabonker.player import Player

DEFAULT_HZ = 200


class Freezer:
    """Runs the freeze loop for a chosen set of levers until stopped."""

    def __init__(self, mem, godmode: bool = False, infinite_mana: bool = False,
                 hz: int = DEFAULT_HZ):
        self.mem = mem
        self.godmode = godmode
        self.infinite_mana = infinite_mana
        self.period = 1.0 / hz
        self.players: list[Player] = []
        self.saves = 0

    def _relocate(self) -> int:
        blocks = find_players(self.mem)
        self.players = [Player(self.mem, b.life_addr) for b in blocks]
        return len(self.players)

    def _tick(self) -> bool:
        """One pass over every copy. Returns False if all reads failed (relocate)."""
        any_ok = False
        for p in self.players:
            if self.godmode:
                mx = p.stat_life_max
                if mx is not None:
                    any_ok = True
                    if p.stat_life != mx:
                        p.set_life(mx)
                        self.saves += 1
            if self.infinite_mana:
                mx = p.stat_mana_max
                if mx is not None:
                    any_ok = True
                    if p.stat_mana != mx:
                        p.set_mana(mx)
        return any_ok

    def run(self, seconds: float | None = None, on_start=None) -> None:
        """Freeze until ``seconds`` elapse, or forever until KeyboardInterrupt."""
        if not self.players and self._relocate() == 0:
            raise RuntimeError("no player found to freeze")
        if on_start:
            on_start(self.players)
        t0 = time.time()
        stale = 0
        while seconds is None or time.time() - t0 < seconds:
            if not self._tick():
                stale += 1
                if stale >= 3:                 # world reload: addresses died
                    if self._relocate() == 0:
                        time.sleep(0.5)         # game gone/loading; wait and retry
                    stale = 0
            else:
                stale = 0
            time.sleep(self.period)
