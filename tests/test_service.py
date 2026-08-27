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


def test_restore_matches_an_edited_item_wherever_it_now_sits(monkeypatch, tmp_path):
    """Slot-keying lost the edit as soon as the player moved the item — which moving
    accessories into the equipment column does routinely (spec 038)."""
    from terrariabonker import profile
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    svc = Service(_game([(2, 250), (3507, 1)]))      # the edited sword is now in slot 1
    profile.set_item_edit(3507, {"damage": 200})

    rep = svc.restore()
    assert 1 in rep["items"], "did not follow the item to its new slot"
    assert svc.inventory()[1].damage == 200


def test_restore_applies_to_every_copy(monkeypatch, tmp_path):
    from terrariabonker import profile
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    svc = Service(_game([(3507, 1), (3507, 1)]))
    profile.set_item_edit(3507, {"damage": 200})

    rep = svc.restore()
    assert sorted(rep["items"]) == [0, 1]
    assert [svc.inventory()[i].damage for i in (0, 1)] == [200, 200]


def test_an_item_you_are_not_carrying_is_absent_not_a_failure(monkeypatch, tmp_path):
    from terrariabonker import profile
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    svc = Service(_game([(2, 250)]))
    profile.set_item_edit(3507, {"damage": 200})

    rep = svc.restore()
    assert rep["absent"] == [3507]
    assert rep["items"] == [] and rep["skipped"] == []
    assert profile.item_edits() == {3507: {"damage": 200}}, "the edit must be kept"


def test_a_recorded_edit_is_never_pruned_against_a_template(monkeypatch, tmp_path):
    """Pruning fields that matched the item's "default" destroyed real edits.

    The default came from the ContentSamples scan, which can pick up a live *edited* item
    as the template — so an edit was compared against itself and dropped. It cost the
    maintainer a Boomstick's use-time and use-animation, silently, on the very edit made
    to test the feature. A redundant field costs a harmless rewrite; a dropped one costs
    the user's work.
    """
    from terrariabonker import profile
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    svc = Service(_game([(964, 1)]))

    svc.record_item_edit(964, {"damage": 31, "use_time": 12, "use_anim": 12,
                               "prefix": 65, "stack": 1, "type": 964})
    assert profile.item_edits() == {964: {"damage": 31, "use_time": 12, "use_anim": 12}}, \
        "an edit was pruned, or an unrestorable field was kept"

    rep = svc.restore()
    assert rep["items"] == [0]
    assert svc.inventory()[0].damage == 31


# --- reporting the live copy, not whichever was last (mid-project review 1.2) ---

def _two_copies(monkeypatch):
    """A live inventory holding a pickaxe, and an inert snapshot holding none.

    That difference is the whole point: while every copy looks the same, reporting the
    wrong one is invisible.
    """
    import struct

    from conftest import FakeMem
    from terrariabonker import service as S
    from terrariabonker.inventory import (ARR_DATA_OFF, ARR_LEN_OFF, INVENTORY_PTR_OFF,
                                          ITEM_PICK, ITEM_TYPE, Inventory)

    base = 0x70000000
    m = FakeMem(base, 0x40000)
    live_life, dead_life = base + 0x2000, base + 0x3000
    # The snapshot differs in BOTH ways the two cheats look at a slot: it carries no
    # pickaxe (fast_mining) and its second slot is empty (long_reach). A fixture whose
    # copies differ in only one of those passes against the bug for the other cheat --
    # which is exactly what happened on the first attempt at this test.
    for life, arr, items, live_copy in ((live_life, base + 0x100, base + 0x8000, True),
                                        (dead_life, base + 0x200, base + 0x9000, False)):
        m.write(life + INVENTORY_PTR_OFF, struct.pack("<I", arr))
        m.poke_i32(arr + ARR_LEN_OFF, 2)
        for i in range(2):
            addr = items + i * 0x200
            m.write(arr + ARR_DATA_OFF + i * 4, struct.pack("<I", addr))
            empty = not live_copy and i == 1
            m.poke_i32(addr + ITEM_TYPE, 0 if empty else 3509)
            m.poke_i32(addr + ITEM_PICK, 100 if live_copy else 0)

    svc = S.Service.__new__(S.Service)
    svc.mem = m

    class Block:
        def __init__(self, life):
            self.life_addr = life

    svc.live_block = lambda: Block(live_life)
    # The live copy first, the snapshot LAST -- the order the old code reported from.
    svc.players = lambda: [Block(live_life), Block(dead_life)]
    return svc, Inventory(m, live_life), Inventory(m, dead_life)


def test_fast_mining_reports_the_live_copy_not_the_last(monkeypatch):
    """It returned whichever copy came last, which is usually an inert snapshot.

    `_all_inventories` says in its own docstring not to read from those copies -- a
    snapshot holds whatever it held when it was taken. The count goes straight to the
    user on the CLI, so the wrong one is a lie about what was changed.
    """
    svc, live, dead = _two_copies(monkeypatch)
    hit = svc.fast_mining()
    assert hit == [0, 1], "reported the snapshot's slots (it has no pickaxes) or nothing"
    assert [s.index for s in live.slots() if s.is_pickaxe] == [0, 1]


def test_long_reach_reports_the_live_copy_not_the_last(monkeypatch):
    """The snapshot's second slot is empty, so it would report [0] where the live copy
    reports [0, 1]."""
    svc, live, dead = _two_copies(monkeypatch)
    assert svc.long_reach(30) == [0, 1]
    assert dead.long_reach(30) == [0], "the fixture's copies do not differ for this cheat"


def test_both_still_write_to_every_copy(monkeypatch):
    """Reporting the live copy must not stop the inert ones being written.

    Writing to all of them is what guarantees the live one is hit at all (spec 039).
    """
    from terrariabonker.inventory import ITEM_TILEBOOST

    svc, live, dead = _two_copies(monkeypatch)
    svc.long_reach(30)
    for inv in (live, dead):
        addr = inv._item_addr(0)
        assert inv.mem.read_i32(addr + ITEM_TILEBOOST) == 30
