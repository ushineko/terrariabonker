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

import struct
from dataclasses import dataclass, field

import numpy as np

from terrariabonker import version as ver
from terrariabonker.inventory import INVENTORY_SLOTS, ITEM_TYPE, Inventory, Slot
from terrariabonker.locate import find_players, pick_live
from terrariabonker.player import Player
from terrariabonker.proc import Mem, ProcError, find_pid

# Main-inventory slots used for "give" (0-49): skips coin/ammo/equip slots.
GIVE_RANGE = range(0, 50)

# When changing a slot's item, copy the pristine template's field block so the
# new item has real stats (SetDefaults values), not a bare type with zeroed
# damage/useTime. Range starts past the object header/reference pointers and
# covers the stat block; every Item is the same large class, so this stays
# safely inside the object.
ITEM_COPY_LO = 0x1C
ITEM_COPY_HI = 0x140


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
    use_anim: int
    rare: int


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

    def _all_inventories(self) -> list[Inventory]:
        """Every player copy's inventory. Writes go to all so the live copy is
        always hit (copies are identical; snapshots ignore the writes) - which
        copy is 'live' can't be told apart while the game is paused."""
        return [Inventory(self.mem, b.life_addr) for b in self.players()]

    def _item_vtable(self) -> int | None:
        """The shared mono vtable of Item objects, read from any real item."""
        for s in self._all_inventories()[0].slots():
            if not s.empty:
                return self.mem.read_u32(s.item_addr)
        return None

    def _template_block(self, item_type: int) -> bytes | None:
        """Field bytes of a pristine Item of this type (from ContentSamples).

        The game keeps a template Item for every type; scan for an Item object
        (vtable match) whose type field equals ``item_type`` and return its stat
        block. Returns None if not found (caller falls back to a bare type set).
        """
        vt = self._item_vtable()
        if vt is None:
            return None
        for start, end in self.mem.regions():
            buf = self.mem.read(start, end - start)
            n = len(buf) // 4
            if n < 1:
                continue
            arr = np.frombuffer(buf[: n * 4], dtype=np.int32)
            for idx in np.where(arr == item_type)[0].tolist():
                off = idx * 4 - ITEM_TYPE
                if off < 0 or off + 4 > len(buf):
                    continue
                if struct.unpack_from("<I", buf, off)[0] != vt:
                    continue
                base = start + off
                block = self.mem.read(base + ITEM_COPY_LO, ITEM_COPY_HI - ITEM_COPY_LO)
                if len(block) == ITEM_COPY_HI - ITEM_COPY_LO:
                    return block
        return None

    def _place_item(self, invs: list[Inventory], slot: int, item_type: int,
                    block: bytes | None) -> None:
        """Set a slot to an item type in every copy, using the template block if
        available so the item has real stats, else a bare type set."""
        for inv in invs:
            addr = inv._item_addr(slot)
            if addr and block:
                self.mem.write(addr + ITEM_COPY_LO, block)
            else:
                inv.set_type(slot, item_type)

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
                        s.use_time, s.pick, s.tile_boost, s.use_anim, s.rare)

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

    # --- inventory mutations (applied to every copy) -----------------------
    def set_stack(self, slot: int, value: int) -> None:
        for inv in self._all_inventories():
            inv.set_stack(slot, value)

    def set_item(self, slot: int, item_type: int, *, stack=None, damage=None,
                 auto_reuse=None, use_time=None, use_anim=None, pick=None,
                 tile_boost=None) -> None:
        invs = self._all_inventories()
        cur = invs[0].read_slot(slot)
        # Only re-template when the item type actually changes (field tweaks on the
        # same item must not wipe the edits, and must stay scan-free/fast).
        changed = cur is None or item_type != cur.type
        # type 0 clears the slot — never template-scan for the "empty" type.
        block = self._template_block(item_type) if (changed and item_type) else None
        if changed:
            self._place_item(invs, slot, item_type, block)
        for inv in invs:
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
        """Put a fully-statted item in the first empty main-inventory slot."""
        invs = self._all_inventories()
        by_slot = {s.index: s for s in invs[0].slots()}
        empty = next((i for i in GIVE_RANGE
                      if i in by_slot and by_slot[i].empty), None)
        if empty is None:
            raise ServiceError("inventory full — no empty slot to give into")
        block = self._template_block(item_type)
        self._place_item(invs, empty, item_type, block)
        for inv in invs:
            inv.set_stack(empty, stack)
        return empty

    def patcher(self):
        """A Patcher bound to this game process (the code-patch cheats)."""
        from terrariabonker.patcher import Patcher
        return Patcher(self.mem)

    def fast_mining(self, use_time: int = 8, use_anim: int = 13, pick: int = 200) -> list[int]:
        hit = []
        for inv in self._all_inventories():
            hit = inv.make_fast_mining(use_time, use_anim, pick)
        return hit

    def long_reach(self, tiles: int = 20) -> list[int]:
        hit = []
        for inv in self._all_inventories():
            hit = inv.long_reach(tiles)
        return hit


__all__ = ["Service", "ServiceError", "Snapshot", "PlayerState", "ItemSlot",
           "INVENTORY_SLOTS", "GIVE_RANGE"]
