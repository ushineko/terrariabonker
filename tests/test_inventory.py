"""Inventory read/edit logic against a synthetic process image."""

import struct

from terrariabonker.inventory import (ARR_DATA_OFF, ARR_LEN_OFF, INVENTORY_PTR_OFF,
                                      ITEM_BUFF_TYPE, ITEM_CONSUMABLE, ITEM_DAMAGE,
                                      ITEM_FAVORITED, ITEM_PICK, ITEM_STACK, ITEM_TYPE,
                                      ITEM_USE_ANIM, ITEM_USE_TIME, Inventory)

BASE = 0x30000000
LIFE = BASE + 0x4000
ARR = BASE + 0x100
ITEMS = BASE + 0x1000        # item objects laid out every 0x200 bytes


def _mem(items):
    """items: list of (type, stack, damage); builds array + Item objects."""
    from conftest import FakeMem
    m = FakeMem(BASE, 0x8000)
    m.write(LIFE + INVENTORY_PTR_OFF, struct.pack("<I", ARR))
    m.poke_i32(ARR + ARR_LEN_OFF, len(items))
    for i, (t, s, d) in enumerate(items):
        addr = ITEMS + i * 0x200
        m.write(ARR + ARR_DATA_OFF + i * 4, struct.pack("<I", addr))
        m.poke_i32(addr + ITEM_TYPE, t)
        m.poke_i32(addr + ITEM_STACK, s)
        m.poke_i32(addr + ITEM_DAMAGE, d)
    return m


def test_reads_slots():
    m = _mem([(2, 377, -1), (0, 0, -1), (3507, 1, 3)])
    inv = Inventory(m, LIFE)
    assert inv.array_addr() == ARR
    slots = inv.slots()
    assert len(slots) == 3
    assert (slots[0].type, slots[0].stack, slots[0].damage) == (2, 377, -1)
    assert slots[1].empty
    assert slots[2].damage == 3


def test_find_type_locates_dirt():
    m = _mem([(0, 0, -1), (2, 377, -1), (9, 50, -1)])
    assert Inventory(m, LIFE).find_type(2) == [1]


def test_set_stack_writes_through():
    m = _mem([(2, 377, -1)])
    inv = Inventory(m, LIFE)
    assert inv.set_stack(0, 9999)
    assert inv.read_slot(0).stack == 9999


def test_set_item_changes_type_stack_damage_autoreuse():
    m = _mem([(0, 0, -1)])
    inv = Inventory(m, LIFE)
    assert inv.set_type(0, 3521)      # e.g. a sword ItemID
    assert inv.set_stack(0, 1)
    assert inv.set_damage(0, 500)
    assert inv.set_auto_reuse(0, True)
    s = inv.read_slot(0)
    assert (s.type, s.stack, s.damage, s.auto_reuse) == (3521, 1, 500, 1)


def test_array_addr_none_when_pointer_null():
    from conftest import FakeMem
    m = FakeMem(BASE, 0x8000)         # no inventory pointer planted
    assert Inventory(m, LIFE).array_addr() is None


def _plant_tool(m, index, item_type, use_time, use_anim, pick):
    """Extend an already-built item with tool stats."""
    addr = ITEMS + index * 0x200
    m.poke_i32(addr + ITEM_TYPE, item_type)
    m.poke_i32(addr + ITEM_USE_TIME, use_time)
    m.poke_i32(addr + ITEM_USE_ANIM, use_anim)
    m.poke_i32(addr + ITEM_PICK, pick)


def test_set_use_speed_and_pick():
    m = _mem([(3509, 1, 3)])          # a pickaxe-ish item
    _plant_tool(m, 0, 3509, 15, 23, 35)
    inv = Inventory(m, LIFE)
    assert inv.set_use_speed(0, 8, 13)
    assert inv.set_pick(0, 200)
    s = inv.read_slot(0)
    assert (s.use_time, s.use_anim, s.pick) == (8, 13, 200)
    assert s.is_pickaxe


def test_set_tile_boost_and_long_reach():
    m = _mem([(2, 999, -1), (0, 0, -1), (9, 50, -1)])   # dirt, empty, wood
    inv = Inventory(m, LIFE)
    assert inv.set_tile_boost(0, 25)
    assert inv.read_slot(0).tile_boost == 25
    hit = inv.long_reach(tiles=30)
    assert hit == [0, 2]                                 # both non-empty items, not the empty slot
    assert inv.read_slot(0).tile_boost == 30
    assert inv.read_slot(2).tile_boost == 30


def test_make_fast_mining_only_touches_pickaxes():
    m = _mem([(3509, 1, 3), (2, 999, -1)])   # slot0 tool, slot1 dirt (no pick)
    _plant_tool(m, 0, 3509, 15, 23, 35)      # slot0 is a pickaxe (pick=35)
    _plant_tool(m, 1, 2, 10, 15, 0)          # slot1 dirt (pick=0, not a pickaxe)
    inv = Inventory(m, LIFE)
    hit = inv.make_fast_mining(use_time=8, use_anim=13, pick=200)
    assert hit == [0]
    assert inv.read_slot(0).use_time == 8 and inv.read_slot(0).pick == 200
    assert inv.read_slot(1).use_time == 10   # dirt untouched


# --- passive potions: which items qualify (spec 041) ---------------------------

def _potions(items):
    """items: (type, stack, favorited, consumable, buffType) per slot."""
    from conftest import FakeMem
    m = FakeMem(BASE, 0x8000)
    m.write(LIFE + INVENTORY_PTR_OFF, struct.pack("<I", ARR))
    m.poke_i32(ARR + ARR_LEN_OFF, len(items))
    for i, (t, st, fav, cons, buff) in enumerate(items):
        addr = ITEMS + i * 0x200
        m.write(ARR + ARR_DATA_OFF + i * 4, struct.pack("<I", addr))
        m.poke_i32(addr + ITEM_TYPE, t)
        m.poke_i32(addr + ITEM_STACK, st)
        m.write(addr + ITEM_FAVORITED, bytes([fav]))
        m.write(addr + ITEM_CONSUMABLE, bytes([cons]))
        m.poke_i32(addr + ITEM_BUFF_TYPE, buff)
    return m


IRONSKIN = (292, 4, 1, 1, 5)          # favorited, consumable, grants buff 5


def test_a_favorited_consumable_with_a_buff_qualifies():
    m = _potions([IRONSKIN])
    assert Inventory(m, LIFE).favorited_potions() == [(0, 5)]


def test_an_unfavorited_potion_never_qualifies():
    """The whole point of the favorite gate: potions you pick up must stay inert, however
    large the stack."""
    m = _potions([(292, 999, 0, 1, 5)])
    assert Inventory(m, LIFE).favorited_potions() == []


def test_a_favorited_pet_is_inert_because_it_is_not_consumable():
    """Pets, light pets and mounts all carry a buffType. Summoning a pet because it was
    in the bag is not what "passive potions" means."""
    m = _potions([(1927, 1, 1, 0, 40)])
    assert Inventory(m, LIFE).favorited_potions() == []


def test_a_stack_below_the_threshold_does_not_qualify():
    m = _potions([IRONSKIN])
    inv = Inventory(m, LIFE)
    assert inv.favorited_potions(min_stack=4) == [(0, 5)]
    assert inv.favorited_potions(min_stack=5) == []


def test_a_favorited_consumable_with_no_buff_is_skipped():
    """Most consumables are food or ammo, not potions, and carry no buff."""
    m = _potions([(40, 500, 1, 1, 0)])
    assert Inventory(m, LIFE).favorited_potions() == []


def test_every_qualifying_potion_is_reported_not_just_the_first():
    m = _potions([IRONSKIN, (292, 1, 0, 1, 5), (290, 6, 1, 1, 3), (302, 6, 1, 1, 15)])
    assert Inventory(m, LIFE).favorited_potions() == [(0, 5), (2, 3), (3, 15)]


def test_an_empty_slot_is_skipped_without_reading_through_a_null():
    m = _potions([IRONSKIN, (0, 0, 0, 0, 0)])
    m.write(ARR + ARR_DATA_OFF + 1 * 4, struct.pack("<I", 0))
    assert Inventory(m, LIFE).favorited_potions() == [(0, 5)]
