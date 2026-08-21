"""Terraria Player field map and a handle for reading/writing one copy.

All offsets are relative to the located ``statLife`` address (the locator's
anchor), so the whole map survives GC relocation as a unit. Only the life/mana
block and the name pointer are mapped so far; more fields (defense, pickSpeed,
inventory) get added as they are derived. See docs/discovery.md.

Offsets are for Terraria 1.4.5.7.
"""

from __future__ import annotations

# int32 field offsets relative to statLife.
OFF_STAT_LIFE_MAX2 = -0x08     # permanent max HP (what the save stores)
OFF_STAT_LIFE_MAX = -0x04      # current max HP cap (max2 + temporary bonuses)
OFF_STAT_LIFE = 0x00           # current HP
OFF_STAT_MANA = 0x04           # current mana
OFF_STAT_MANA_MAX = 0x08       # current max mana cap
OFF_STAT_MANA_MAX2 = 0x0C      # permanent max mana
OFF_NAME_PTR = -0x6C0          # Player.name, a mono String*


class Player:
    """A handle to one player copy in memory, addressed by its statLife location."""

    def __init__(self, mem, life_addr: int):
        self.mem = mem
        self.life = life_addr

    # --- reads -------------------------------------------------------------
    @property
    def stat_life(self) -> int | None:
        return self.mem.read_i32(self.life + OFF_STAT_LIFE)

    @property
    def stat_life_max(self) -> int | None:
        return self.mem.read_i32(self.life + OFF_STAT_LIFE_MAX)

    @property
    def stat_life_max2(self) -> int | None:
        return self.mem.read_i32(self.life + OFF_STAT_LIFE_MAX2)

    @property
    def stat_mana(self) -> int | None:
        return self.mem.read_i32(self.life + OFF_STAT_MANA)

    @property
    def stat_mana_max(self) -> int | None:
        return self.mem.read_i32(self.life + OFF_STAT_MANA_MAX)

    @property
    def stat_mana_max2(self) -> int | None:
        return self.mem.read_i32(self.life + OFF_STAT_MANA_MAX2)

    # --- writes ------------------------------------------------------------
    def set_life(self, value: int) -> bool:
        return self.mem.write_i32(self.life + OFF_STAT_LIFE, value)

    def set_mana(self, value: int) -> bool:
        return self.mem.write_i32(self.life + OFF_STAT_MANA, value)

    def set_max_life(self, value: int) -> bool:
        """Raise permanent max HP.

        Writes both the permanent (max2) and current-cap (max) fields so the
        change shows immediately; the game recomputes ``max`` from ``max2`` plus
        temporary bonuses each frame, so writing max2 is what persists.
        """
        ok = self.mem.write_i32(self.life + OFF_STAT_LIFE_MAX2, value)
        ok = self.mem.write_i32(self.life + OFF_STAT_LIFE_MAX, value) and ok
        return ok

    def set_max_mana(self, value: int) -> bool:
        ok = self.mem.write_i32(self.life + OFF_STAT_MANA_MAX2, value)
        ok = self.mem.write_i32(self.life + OFF_STAT_MANA_MAX, value) and ok
        return ok

    def heal_full(self) -> bool:
        mx = self.stat_life_max
        return self.set_life(mx) if mx is not None else False

    def mana_full(self) -> bool:
        mx = self.stat_mana_max
        return self.set_mana(mx) if mx is not None else False
