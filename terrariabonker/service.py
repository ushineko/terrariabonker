"""View-neutral core: every operation the CLI and GUI expose, in one place.

This layer is deliberately toolkit-free (no argparse, no PyQt) so both shells
consume the same operations and data shapes and cannot drift. It assumes it runs
with the privilege needed to read/write game memory (the CLI self-elevates before
constructing a Service; the GUI reaches these operations across a sudo subprocess
boundary via ``gui.client``). ``tests/test_service_neutrality.py`` enforces the
no-toolkit rule structurally.

All operations return plain dataclasses; ``dataclasses.asdict`` gives the JSON the
subprocess boundary carries.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from terrariabonker import version as ver
from terrariabonker.inventory import INVENTORY_SLOTS, Inventory, Slot
from terrariabonker.locate import find_players, pick_live
from terrariabonker.player import Player
from terrariabonker.proc import Mem, ProcError, find_pid

# Main-inventory slots used for "give" (0-49): skips coin/ammo/equip slots.
GIVE_RANGE = range(0, 50)


class ServiceError(RuntimeError):
    """A user-facing failure (game not found, no player, incompatible build)."""


@dataclass(frozen=True)
class PlayerState:
    name: str
    hp: int
    max_hp: int
    mana: int
    max_mana: int


@dataclass(frozen=True)
class ItemSlot:
    slot: int
    type: int
    stack: int
    damage: int
    auto_reuse: int
    use_time: int
    pick: int
    tile_boost: int


@dataclass(frozen=True)
class Snapshot:
    pid: int
    version: str | None
    buildid: str | None
    compat_level: str
    compat_msg: str
    copies: int
    player: PlayerState | None
    inventory: list[ItemSlot] = field(default_factory=list)


class Service:
    """Bound to one running game process; all operations go through it."""

    def __init__(self, mem: Mem):
        self.mem = mem

    @classmethod
    def connect(cls) -> "Service":
        """Attach to the running game. Assumes the caller already has root."""
        try:
            return cls(Mem(find_pid()))
        except ProcError as e:
            raise ServiceError(str(e)) from e

    # --- build gate --------------------------------------------------------
    def compatibility(self) -> tuple[str, str]:
        version = ver.detect_version(self.mem)
        buildid = ver.read_buildid(self.mem.exe_path())
        return ver.compatibility(version, buildid)

    def require_compatible(self, force: bool = False) -> None:
        """Raise on an incompatible build unless forced (for mutating ops)."""
        level, msg = self.compatibility()
        if level == "incompatible" and not force:
            raise ServiceError(
                f"{msg}. Re-derive offsets (docs/discovery.md) or force to override.")

    # --- locating ----------------------------------------------------------
    def players(self):
        blocks = find_players(self.mem)
        if not blocks:
            raise ServiceError("no player found. Load into a world first.")
        return blocks

    def live_block(self):
        """The live player copy: activity-picked, else richest inventory."""
        blocks = self.players()
        live = pick_live(self.mem, blocks)
        if live is None:
            live = max(blocks,
                       key=lambda b: Inventory(self.mem, b.life_addr).nonempty_count())
        return live

    def _all_targets(self):
        """Every player copy as a Player handle (stat writes hit all; snapshots inert)."""
        return [Player(self.mem, b.life_addr) for b in self.players()]

    def _live_inventory(self) -> Inventory:
        return Inventory(self.mem, self.live_block().life_addr)

    # --- reads -------------------------------------------------------------
    def snapshot(self, with_inventory: bool = True) -> Snapshot:
        blocks = find_players(self.mem)
        version = ver.detect_version(self.mem)
        buildid = ver.read_buildid(self.mem.exe_path())
        level, msg = ver.compatibility(version, buildid)
        player = inv_slots = None
        inv_slots = []
        if blocks:
            live = pick_live(self.mem, blocks)
            if live is None:
                live = max(blocks,
                           key=lambda b: Inventory(self.mem, b.life_addr).nonempty_count())
            player = PlayerState(live.name, live.stat_life, live.stat_life_max,
                                 live.stat_mana, live.stat_mana_max)
            if with_inventory:
                inv_slots = [self._to_slot(s)
                             for s in Inventory(self.mem, live.life_addr).slots()]
        return Snapshot(self.mem.pid, version, buildid, level, msg,
                        len(blocks), player, inv_slots)

    @staticmethod
    def _to_slot(s: Slot) -> ItemSlot:
        return ItemSlot(s.index, s.type, s.stack, s.damage, s.auto_reuse,
                        s.use_time, s.pick, s.tile_boost)

    def inventory(self) -> list[ItemSlot]:
        return [self._to_slot(s) for s in self._live_inventory().slots()]

    # --- stat mutations (applied to all copies; live one takes effect) ------
    def set_hp(self, value) -> None:
        for p in self._all_targets():
            p.set_life(p.stat_life_max if value == "max" else int(value))

    def set_mana(self, value) -> None:
        for p in self._all_targets():
            p.set_mana(p.stat_mana_max if value == "max" else int(value))

    def set_max_hp(self, value: int) -> None:
        for p in self._all_targets():
            p.set_max_life(int(value))

    def set_max_mana(self, value: int) -> None:
        for p in self._all_targets():
            p.set_max_mana(int(value))

    # --- inventory mutations (live copy only) ------------------------------
    def set_stack(self, slot: int, value: int) -> None:
        self._live_inventory().set_stack(slot, value)

    def set_item(self, slot: int, item_type: int, *, stack=None, damage=None,
                 auto_reuse=None, use_time=None, use_anim=None, pick=None,
                 tile_boost=None) -> None:
        inv = self._live_inventory()
        inv.set_type(slot, item_type)
        if stack is not None:
            inv.set_stack(slot, stack)
        if damage is not None:
            inv.set_damage(slot, damage)
        if auto_reuse is not None:
            inv.set_auto_reuse(slot, bool(auto_reuse))
        if use_time is not None:
            inv.set_use_speed(slot, use_time, use_anim)
        if pick is not None:
            inv.set_pick(slot, pick)
        if tile_boost is not None:
            inv.set_tile_boost(slot, tile_boost)

    def give_item(self, item_type: int, stack: int = 1) -> int:
        """Put an item in the first empty main-inventory slot. Returns that slot."""
        inv = self._live_inventory()
        by_slot = {s.index: s for s in inv.slots()}
        empty = next((i for i in GIVE_RANGE
                      if i in by_slot and by_slot[i].empty), None)
        if empty is None:
            raise ServiceError("inventory full — no empty slot to give into")
        inv.set_type(empty, item_type)
        inv.set_stack(empty, stack)
        return empty

    def fast_mining(self, use_time: int = 8, use_anim: int = 13, pick: int = 200) -> list[int]:
        return self._live_inventory().make_fast_mining(use_time, use_anim, pick)

    def long_reach(self, tiles: int = 20) -> list[int]:
        return self._live_inventory().long_reach(tiles)


__all__ = ["Service", "ServiceError", "Snapshot", "PlayerState", "ItemSlot",
           "INVENTORY_SLOTS", "GIVE_RANGE"]
