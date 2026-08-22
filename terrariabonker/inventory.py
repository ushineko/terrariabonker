"""Terraria inventory reading and editing.

The inventory is reached structurally, not by value-scanning a stack count:
scanning finds only downstream caches (a crafting aggregate, a UI mirror) whose
writes do not affect the game. The real source is ``Player.inventory``, an
``Item[]`` of 59 slots reached through a pointer at ``statLife - 0x664``.

mono szarray layout (32-bit): ``+0xC`` max_length, ``+0x10`` element pointers.
Every slot holds a real ``Item`` (empty slots are type 0, never null). Within an
``Item``: type at ``+0x6C`` (ItemID), stack at ``+0x88``, prefix at ``+0xAC``
(modifier tier, -1 when unmodified).

Offsets are for Terraria 1.4.5.7. See docs/discovery.md.
"""

from __future__ import annotations

from dataclasses import dataclass

INVENTORY_PTR_OFF = -0x664      # Player field holding the Item[] pointer, from statLife
INVENTORY_SLOTS = 59

ARR_LEN_OFF = 0x0C              # mono szarray max_length
ARR_DATA_OFF = 0x10            # first element pointer

# Item field offsets, derived by diffing Copper Pickaxe (3509) vs Copper Axe (3506).
ITEM_USE_ANIM = 0x80    # useAnimation (visual swing frames)
ITEM_USE_TIME = 0x84    # useTime (ticks per use; lower = faster mining/placing/attack)
ITEM_TYPE = 0x6C
ITEM_STACK = 0x88
ITEM_TILEBOOST = 0x9C   # extra tiles of placement reach (added to base tileRangeX/Y)
ITEM_PICK = 0x90        # pickaxe power %
ITEM_AXE = 0x94         # axe power (x5 = displayed %)
ITEM_HAMMER = 0x98      # hammer power %
ITEM_DAMAGE = 0xAC      # weapon/tool damage (-1 for non-damaging items)
ITEM_DEFENSE = 0xD4     # defense the item grants (armor/some accessories; 0 otherwise)
ITEM_CONSUMABLE = 0xBD  # byte bool; if set, the item is used up on use (careful!)
ITEM_AUTOREUSE = 0xBE   # byte bool; auto-swing while the button is held
ITEM_RARE = 0xF8        # rarity tier (int): -1 gray .. 0 white .. 10 red .. 11 purple
ITEM_PREFIX = 0x15C     # modifier tier (byte): 0 none .. e.g. Legendary/Warding/Menacing
# Damage-class / accessory flags (byte bools) — used to offer only item-appropriate
# modifiers in the editor and to label a modified item.
ITEM_ACCESSORY = 0x7D
ITEM_MELEE = 0x15D
ITEM_MAGIC = 0x15E
ITEM_RANGED = 0x15F
ITEM_SUMMON = 0x160


@dataclass
class Slot:
    index: int
    item_addr: int
    type: int
    stack: int
    use_time: int
    use_anim: int
    pick: int
    tile_boost: int
    damage: int
    auto_reuse: int
    rare: int
    defense: int
    prefix: int
    flags: dict          # damage-class / accessory bools: melee/ranged/magic/summon/accessory

    @property
    def empty(self) -> bool:
        return self.type == 0

    @property
    def is_pickaxe(self) -> bool:
        return self.pick > 0


class Inventory:
    """The inventory of one player copy, addressed by that copy's statLife."""

    def __init__(self, mem, life_addr: int):
        self.mem = mem
        self.life = life_addr

    def array_addr(self) -> int | None:
        """Resolve the current Item[] address (re-read so it self-corrects)."""
        ptr = self.mem.read_u32(self.life + INVENTORY_PTR_OFF)
        return ptr or None

    def _item_addr(self, index: int) -> int | None:
        arr = self.array_addr()
        if arr is None:
            return None
        return self.mem.read_u32(arr + ARR_DATA_OFF + index * 4)

    def read_slot(self, index: int) -> Slot | None:
        addr = self._item_addr(index)
        if not addr:
            return None
        return Slot(
            index=index,
            item_addr=addr,
            type=self.mem.read_i32(addr + ITEM_TYPE),
            stack=self.mem.read_i32(addr + ITEM_STACK),
            use_time=self.mem.read_i32(addr + ITEM_USE_TIME),
            use_anim=self.mem.read_i32(addr + ITEM_USE_ANIM),
            pick=self.mem.read_i32(addr + ITEM_PICK),
            tile_boost=self.mem.read_i32(addr + ITEM_TILEBOOST),
            damage=self.mem.read_i32(addr + ITEM_DAMAGE),
            auto_reuse=self.mem.read(addr + ITEM_AUTOREUSE, 1)[0] if addr else 0,
            rare=self.mem.read_i32(addr + ITEM_RARE),
            defense=self.mem.read_i32(addr + ITEM_DEFENSE),
            prefix=self.mem.read(addr + ITEM_PREFIX, 1)[0] if addr else 0,
            flags={
                "accessory": bool(self.mem.read(addr + ITEM_ACCESSORY, 1)[0]),
                "melee": bool(self.mem.read(addr + ITEM_MELEE, 1)[0]),
                "magic": bool(self.mem.read(addr + ITEM_MAGIC, 1)[0]),
                "ranged": bool(self.mem.read(addr + ITEM_RANGED, 1)[0]),
                "summon": bool(self.mem.read(addr + ITEM_SUMMON, 1)[0]),
            },
        )

    def slots(self) -> list[Slot]:
        out = []
        for i in range(INVENTORY_SLOTS):
            s = self.read_slot(i)
            if s is not None:
                out.append(s)
        return out

    def find_type(self, item_type: int) -> list[int]:
        """Indices of slots holding the given ItemID."""
        return [s.index for s in self.slots() if s.type == item_type]

    def nonempty_count(self) -> int:
        """How many slots hold a real item.

        A distinguishing signal for the live player: its inventory reflects
        actual play, while the load-time snapshot copies hold only the starting
        items. Used to pick the live copy when activity sampling is inconclusive
        (e.g. the game is paused).
        """
        return sum(1 for s in self.slots() if not s.empty)

    # --- edits -------------------------------------------------------------
    def set_stack(self, index: int, value: int) -> bool:
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_STACK, value) if addr else False

    def set_type(self, index: int, value: int) -> bool:
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_TYPE, value) if addr else False

    def set_damage(self, index: int, value: int) -> bool:
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_DAMAGE, value) if addr else False

    def set_auto_reuse(self, index: int, on: bool = True) -> bool:
        """Toggle auto-swing (hold to attack) on the item in this slot."""
        addr = self._item_addr(index)
        return self.mem.write(addr + ITEM_AUTOREUSE, bytes([1 if on else 0])) if addr else False

    def set_use_speed(self, index: int, use_time: int, use_anim: int | None = None) -> bool:
        """Set swing speed (mining/placing/attacking). Lower = faster."""
        addr = self._item_addr(index)
        if not addr:
            return False
        ok = self.mem.write_i32(addr + ITEM_USE_TIME, use_time)
        ok = self.mem.write_i32(addr + ITEM_USE_ANIM,
                                use_time if use_anim is None else use_anim) and ok
        return ok

    def set_pick(self, index: int, value: int) -> bool:
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_PICK, value) if addr else False

    def set_defense(self, index: int, value: int) -> bool:
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_DEFENSE, value) if addr else False

    def set_prefix(self, index: int, value: int) -> bool:
        """Set the item's modifier tier (a byte, e.g. Legendary/Warding)."""
        addr = self._item_addr(index)
        return self.mem.write(addr + ITEM_PREFIX, bytes([value & 0xFF])) if addr else False

    def set_tile_boost(self, index: int, value: int) -> bool:
        """Set extra placement reach (tiles) for the item in this slot."""
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_TILEBOOST, value) if addr else False

    def long_reach(self, tiles: int = 20) -> list[int]:
        """Give every non-empty item extended placement reach. Returns slots hit.

        Harmless on non-placeable items (tileBoost only affects tile placement),
        so this is applied inventory-wide rather than only to the held slot.
        """
        hit = []
        for s in self.slots():
            if not s.empty:
                self.set_tile_boost(s.index, tiles)
                hit.append(s.index)
        return hit

    def make_fast_mining(self, use_time: int = 8, use_anim: int = 13,
                         pick: int | None = 200) -> list[int]:
        """Speed up every pickaxe in the inventory (persistent). Returns slots hit.

        Defaults are Picksaw-tier: fast but smooth against the 60 fps swing cap.
        A pickaxe is any item whose ``pick`` power is above zero.
        """
        hit = []
        for s in self.slots():
            if s.is_pickaxe:
                self.set_use_speed(s.index, use_time, use_anim)
                if pick is not None:
                    self.set_pick(s.index, pick)
                hit.append(s.index)
        return hit
