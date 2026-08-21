"""Service-core operations against a synthetic game image (no game, no root)."""

import struct

from terrariabonker.inventory import (ARR_DATA_OFF, ARR_LEN_OFF, INVENTORY_PTR_OFF,
                                      INVENTORY_SLOTS, ITEM_DAMAGE, ITEM_STACK, ITEM_TYPE)
from terrariabonker.service import Service

BASE = 0x10000000
LIFE = BASE + 0x3000
NAME_AT = BASE + 0x40
ARR = BASE + 0x100
ITEMS = BASE + 0x4000
VTABLE = 0x0ABCDE00                # shared "Item vtable" for the fake
TEMPLATE_AT = BASE + 0x400         # a pristine template Item, outside the inventory


def _game(items):
    """Build a fake process image: one player + an inventory of `items` (type,stack)."""
    from conftest import FakeMem
    m = FakeMem(BASE, 0xC000)
    m.plant_mono_string(NAME_AT, "hero")
    m.plant_player(LIFE, [100, 100, 80, 20, 20, 20], NAME_AT)   # life/mana block + name ptr
    m.write(LIFE + INVENTORY_PTR_OFF, struct.pack("<I", ARR))
    m.poke_i32(ARR + ARR_LEN_OFF, INVENTORY_SLOTS)
    for i in range(INVENTORY_SLOTS):
        addr = ITEMS + i * 0x120
        m.write(ARR + ARR_DATA_OFF + i * 4, struct.pack("<I", addr))
        t, s = items[i] if i < len(items) else (0, 0)
        m.poke_i32(addr + 0, VTABLE)          # object vtable (identifies an Item)
        m.poke_i32(addr + ITEM_TYPE, t)
        m.poke_i32(addr + ITEM_STACK, s)
    return m


def test_snapshot_reports_player_and_inventory():
    svc = Service(_game([(3507, 1), (2, 250)]))
    snap = svc.snapshot()
    assert snap.player.name == "hero"
    assert (snap.player.hp, snap.player.max_hp) == (80, 100)
    assert (snap.player.mana, snap.player.max_mana) == (20, 20)
    assert len(snap.inventory) == INVENTORY_SLOTS
    assert snap.inventory[0].type == 3507 and snap.inventory[1].stack == 250


def test_set_hp_max_heals():
    m = _game([(2, 1)])
    Service(m).set_hp("max")
    assert struct.unpack("<i", m.read(LIFE, 4))[0] == 100


def test_set_stack_and_set_item():
    m = _game([(2, 1)])
    svc = Service(m)
    svc.set_stack(0, 9999)
    assert svc.inventory()[0].stack == 9999
    svc.set_item(1, 3521, stack=1, damage=500, auto_reuse=1)
    slot = svc.inventory()[1]
    assert (slot.type, slot.damage, slot.auto_reuse) == (3521, 500, 1)


def test_give_item_uses_first_empty_slot():
    # slots 0,1 occupied; give should land in slot 2
    svc = Service(_game([(3507, 1), (2, 99)]))
    slot = svc.give_item(9, 500)
    assert slot == 2
    inv = {s.slot: s for s in svc.inventory()}
    assert inv[2].type == 9 and inv[2].stack == 500


def test_give_copies_template_stats():
    # A pristine template Item of type 9 with real stats sits in memory; giving
    # type 9 must copy its stat block (damage), not just set a bare type.
    m = _game([(2, 1)])
    m.poke_i32(TEMPLATE_AT + 0, VTABLE)
    m.poke_i32(TEMPLATE_AT + ITEM_TYPE, 9)
    m.poke_i32(TEMPLATE_AT + ITEM_DAMAGE, 77)      # within the copied 0x1c..0x140 block
    svc = Service(m)
    slot = svc.give_item(9, 3)
    s = svc.inventory()[slot]
    assert s.type == 9 and s.stack == 3
    assert s.damage == 77, "give did not copy the template's stat block"


def test_set_item_type_change_copies_template():
    m = _game([(2, 1)])
    m.poke_i32(TEMPLATE_AT + 0, VTABLE)
    m.poke_i32(TEMPLATE_AT + ITEM_TYPE, 3521)
    m.poke_i32(TEMPLATE_AT + ITEM_DAMAGE, 55)
    svc = Service(m)
    svc.set_item(0, 3521)                          # change slot 0 (was type 2) -> 3521
    s = svc.inventory()[0]
    assert s.type == 3521 and s.damage == 55


def test_give_item_raises_when_full():
    from terrariabonker.service import ServiceError
    svc = Service(_game([(3507 + i % 5 + 1, 1) for i in range(50)]))  # fill slots 0-49
    try:
        svc.give_item(2, 1)
        assert False, "expected ServiceError on full inventory"
    except ServiceError:
        pass
