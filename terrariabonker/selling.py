"""Auto-selling: price a whitelisted item, take it, and pay for it in coins (spec 048).

The game's own sell path (``Player::SellItem``) is deliberately **not** called. It takes no
NPC argument and checks none -- the shopkeeper gate lives entirely in the UI -- so the whole
transaction is "clear a slot, credit some coins", which is arithmetic this module can do
without injecting anything into the game.

Two facts here were measured rather than reasoned, and both would have been got wrong:

* ``Item.value`` is held at **five times** the item's copper worth. Copper/Silver/Gold/
  Platinum Coin read 5 / 500 / 50000 / 5000000, so ``value // 5`` returns exactly one of
  each. That is why ``SellItem`` divides by 5: the division is the sell rate itself.
* **A modifier scales ``value``.** Three Slime Staffs in one inventory read 100000, 189062
  and 58522. Pricing from the item *type's* template -- the obvious implementation -- would
  mis-price every prefixed item, so the price is always read off the live item.
"""

from __future__ import annotations

import struct

from terrariabonker.inventory import (ARR_DATA_OFF, ARR_LEN_OFF, ITEM_FAVORITED,
                                      ITEM_STACK, ITEM_TYPE)

#: Where a ContentSamples template block is copied into an Item (``service.ITEM_COPY_LO``,
#: spelled once and imported rather than restated -- see AGENTS.md on re-spelling offsets).
COPY_LO = 0x1C

# --- offsets ----------------------------------------------------------------
# Asked of the mono runtime with ``tools/monofields.py`` on 1.4.5.7+24893155 and then
# checked against live data, because a runtime dump only proves the runtime agrees with
# itself. Pinned as literals in ``tests/test_selling.py``.
ITEM_VALUE = 0x124          # Item.value; 5x the copper worth (see the module docstring)

#: ``Player.bank`` is at object offset 0x0E0 and ``statLife`` at 0x738, and every
#: inventory-side accessor in this project addresses the player by its statLife -- so this
#: is expressed the same way ``INVENTORY_PTR_OFF`` is, as a delta from statLife.
BANK_PTR_OFF = 0x0E0 - 0x738        # -0x658, the Piggy Bank chest
SAFE_PTR_OFF = 0x0E4 - 0x738        # -0x654, kept for provenance; not used as a fallback

CHEST_ITEM_OFF = 0x08       # Chest.item, the Item[] the container holds
BANK_SLOTS = 40             # Chest.DefaultMaxItems; the read length is validated, not trusted

#: ``Player.inventory`` is 59 long, but the last slot is not a normal one and the grid
#: hides it. Selling walks the visible slots; coins go only where ``SellItem`` puts them,
#: which its own IL bounds at slot 53 (hotbar + main + coin slots).
SELL_SLOTS = 58
COIN_SLOTS = 54

# --- the game's own constants ----------------------------------------------
#: Copper, Silver, Gold, Platinum, and what one of each is worth in copper. ``DoCoins``
#: reads exactly these four types and promotes a stack of 100 to ``type + 1``.
COIN_TYPES = (71, 72, 73, 74)
COIN_WORTH = (1, 100, 10_000, 1_000_000)
COIN_MAX_STACK = 100        # 100 of a coin becomes 1 of the next tier up

PIGGY_BANK_ITEM = 87        # placeable anywhere, so carrying one makes the bank reachable
MONEY_TROUGH_ITEM = 3213    # summons a flying piggy bank; opens the same container
PIGGY_BANK_TILE = 29        # TileID.PiggyBank, a 2x1 placed furniture

SELL_DIVISOR = 5


def sell_price(value: int, stack: int) -> int:
    """What ``stack`` of an item worth ``value`` sells for, in copper.

    ``SellItem`` floors the per-unit price at 1 *before* multiplying by the stack, so a
    thousand near-worthless items are worth a thousand copper rather than nothing.
    """
    if value <= 0 or stack <= 0:
        return 0
    return max(1, value // SELL_DIVISOR) * stack


def coin_stacks(copper: int) -> list[tuple[int, int]]:
    """``copper`` split into ``(coin type, stack)`` pairs, largest denomination first.

    This is the shape ``DoCoins`` leaves an inventory in: nothing below 100 of a
    denomination that could have been carried by the next one up.
    """
    out: list[tuple[int, int]] = []
    if copper <= 0:
        return out
    for ctype, worth in zip(reversed(COIN_TYPES), reversed(COIN_WORTH)):
        n, copper = divmod(copper, worth)
        if n:
            out.append((ctype, n))
    return out


class Container:
    """A read/write view of one ``Item[]`` -- the inventory or a bank chest.

    Deliberately addressed by *identity on every access* rather than by a cached array
    pointer: mono moves objects, and a stale pointer writes into whatever now lives there.
    """

    def __init__(self, mem, array_getter, slots: int):
        self.mem = mem
        self._get_array = array_getter
        self.slots = slots

    def array_addr(self) -> int | None:
        return self._get_array()

    def item_addr(self, index: int) -> int | None:
        arr = self.array_addr()
        if arr is None or not 0 <= index < self.slots:
            return None
        return self.mem.read_u32(arr + ARR_DATA_OFF + index * 4) or None

    def write(self, index: int, *, expect_type: int, expect_stack: int,
              stack: int | None = None, item_type: int | None = None,
              block: bytes | None = None) -> bool:
        """Write one slot, re-resolving its address and checking what is there first.

        **Never write through an address read earlier.** mono moves objects, so a pointer
        captured a few syscalls ago can name memory that now belongs to something else --
        and a round that reads forty slots and then writes to them is a wide window.
        Opening a chest is exactly the kind of allocation that moves things.

        ``expect_type``/``expect_stack`` are what the caller believed was in the slot. A
        mismatch means the slot changed underneath us, which is a reason to abort rather
        than a value to overwrite.
        """
        addr = self.item_addr(index)          # resolved NOW, not earlier
        if not addr:
            return False
        if self.mem.read_i32(addr + ITEM_TYPE) != expect_type:
            return False
        if self.mem.read_i32(addr + ITEM_STACK) != expect_stack:
            return False
        if block is not None:
            self.mem.write(addr + COPY_LO, block)
        elif item_type is not None:
            self.mem.write_i32(addr + ITEM_TYPE, item_type)
        if stack is not None:
            self.mem.write_i32(addr + ITEM_STACK, stack)
        return True

    def read(self, index: int) -> dict | None:
        """``{type, stack, value, favorited}`` for one slot, or None if unreadable."""
        addr = self.item_addr(index)
        if not addr:
            return None
        try:
            head = self.mem.read(addr + ITEM_TYPE, ITEM_STACK - ITEM_TYPE + 4)
        except OSError:
            return None
        if len(head) < ITEM_STACK - ITEM_TYPE + 4:
            return None
        itype = struct.unpack_from("<i", head, 0)[0]
        return {
            "index": index,
            "addr": addr,
            "type": itype,
            "stack": struct.unpack_from("<i", head, ITEM_STACK - ITEM_TYPE)[0],
            "favorited": bool(head[ITEM_FAVORITED - ITEM_TYPE]),
            "value": self.mem.read_i32(addr + ITEM_VALUE) or 0,
        }

    def rows(self) -> list[dict]:
        return [r for r in (self.read(i) for i in range(self.slots)) if r]


def bank_container(mem, life_addr: int) -> Container | None:
    """The player's Piggy Bank as a :class:`Container`, or None if it does not read.

    The 40-slot length is **checked rather than trusted**: it is the cheapest signal that
    the chest pointer is really a chest, and a wrong offset is far more likely to miss it
    than to hit it.
    """
    def array():
        chest = mem.read_u32(life_addr + BANK_PTR_OFF)
        if not chest:
            return None
        arr = mem.read_u32(chest + CHEST_ITEM_OFF)
        if not arr:
            return None
        return arr if mem.read_i32(arr + ARR_LEN_OFF) == BANK_SLOTS else None

    return Container(mem, array, BANK_SLOTS) if array() else None
