"""Auto-sell pricing, coin math, bank access and the tile scan (spec 048).

The offsets asserted here are **literals on purpose**. They were obtained from the mono
runtime with ``tools/monofields.py`` on 1.4.5.7+24893155 and confirmed against live data
in the same session -- Item.value by the four coin types reading 5 / 500 / 50000 /
5000000, and Chest.item by every one of the four bank chests holding a 40-long array. A
test that read them back through the module's own constant would prove only that the
constant equals itself.
"""

import struct

import pytest

from terrariabonker import selling as S
from terrariabonker.inventory import (ARR_DATA_OFF, ARR_LEN_OFF, ITEM_FAVORITED,
                                      ITEM_STACK, ITEM_TYPE)

BASE = 0x30000000
LIFE = BASE + 0x4000
CHEST = BASE + 0x200
ARR = BASE + 0x300
ITEMS = BASE + 0x1000


def test_offsets_are_the_measured_ones():
    assert S.ITEM_VALUE == 0x124
    assert S.BANK_PTR_OFF == 0x0E0 - 0x738
    assert S.CHEST_ITEM_OFF == 0x08
    assert S.BANK_SLOTS == 40


def test_coin_types_and_worth_agree_with_the_game():
    # DoCoins reads exactly 71..74 and promotes at 100.
    assert S.COIN_TYPES == (71, 72, 73, 74)
    assert S.COIN_WORTH == (1, 100, 10_000, 1_000_000)
    assert S.COIN_MAX_STACK == 100


@pytest.mark.parametrize("value,worth", [(5, 1), (500, 100), (50_000, 10_000),
                                         (5_000_000, 1_000_000)])
def test_a_coin_sells_for_its_own_face_value(value, worth):
    """value // 5 round-trips the four coins -- the check that pinned the formula."""
    assert S.sell_price(value, 1) == worth


def test_price_floors_per_unit_not_per_stack():
    # SellItem floors the unit price at 1 BEFORE multiplying, so 1000 near-worthless
    # items are worth 1000 copper, not 0.
    assert S.sell_price(4, 1000) == 1000
    assert S.sell_price(0, 10) == 0


def test_price_scales_with_stack():
    assert S.sell_price(500, 7) == 700


def test_coin_stacks_splits_largest_first():
    assert S.coin_stacks(1_234_567) == [(74, 1), (73, 23), (72, 45), (71, 67)]
    assert S.coin_stacks(0) == []


def test_coin_stacks_conserves_value():
    for copper in (1, 99, 100, 101, 999_999, 1_000_000, 12_345_678):
        total = sum(n * S.COIN_WORTH[S.COIN_TYPES.index(t)]
                    for t, n in S.coin_stacks(copper))
        assert total == copper


def _bank_mem(rows, length=S.BANK_SLOTS):
    """A player whose Piggy Bank chest holds ``rows`` of (type, stack)."""
    from conftest import FakeMem
    m = FakeMem(BASE, 0x20000)
    m.write(LIFE + S.BANK_PTR_OFF, struct.pack("<I", CHEST))
    m.write(CHEST + S.CHEST_ITEM_OFF, struct.pack("<I", ARR))
    m.poke_i32(ARR + ARR_LEN_OFF, length)
    for i in range(S.BANK_SLOTS):
        addr = ITEMS + i * 0x200
        m.write(ARR + ARR_DATA_OFF + i * 4, struct.pack("<I", addr))
        t, stack = rows[i] if i < len(rows) else (0, 0)
        m.poke_i32(addr + ITEM_TYPE, t)
        m.poke_i32(addr + ITEM_STACK, stack)
    return m


def test_bank_container_reads_through_the_chest():
    m = _bank_mem([(71, 37), (73, 5)])
    c = S.bank_container(m, LIFE)
    assert c is not None
    rows = c.rows()
    assert (rows[0]["type"], rows[0]["stack"]) == (71, 37)
    assert (rows[1]["type"], rows[1]["stack"]) == (73, 5)
    assert rows[2]["type"] == 0


def test_bank_container_rejects_a_wrong_length_array():
    """The 40-slot length is the cheapest evidence the pointer is really a chest."""
    assert S.bank_container(_bank_mem([], length=7), LIFE) is None


def test_bank_container_rejects_a_null_chest():
    m = _bank_mem([])
    m.write(LIFE + S.BANK_PTR_OFF, struct.pack("<I", 0))
    assert S.bank_container(m, LIFE) is None


def test_container_reports_favorited_and_value():
    m = _bank_mem([(3507, 1)])
    addr = ITEMS
    m.write(addr + ITEM_FAVORITED, b"\x01")
    m.poke_i32(addr + S.ITEM_VALUE, 100_000)
    row = S.bank_container(m, LIFE).read(0)
    assert row["favorited"] is True
    assert row["value"] == 100_000
    assert S.sell_price(row["value"], row["stack"]) == 20_000


def test_two_items_of_one_type_can_price_differently():
    """A prefix scales Item.value: three Slime Staffs read 100000 / 189062 / 58522 in
    game. Pricing from the type's template would have mispriced every modified item."""
    m = _bank_mem([(3507, 1), (3507, 1)])
    m.poke_i32(ITEMS + S.ITEM_VALUE, 100_000)
    m.poke_i32(ITEMS + 0x200 + S.ITEM_VALUE, 189_062)
    c = S.bank_container(m, LIFE)
    assert S.sell_price(c.read(0)["value"], 1) == 20_000
    assert S.sell_price(c.read(1)["value"], 1) == 37_812
