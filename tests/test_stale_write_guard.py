"""The stale-snapshot guard on slot writes (spec 029).

Editing from a stale grid used to template the caller's remembered item over
whatever the slot really held, destroying it. ``expect_type`` states what the
caller believed was there; a mismatch must refuse the write outright.
"""

import pytest

from terrariabonker import names
from terrariabonker.inventory import ARR_DATA_OFF, ITEM_STACK, Inventory
from terrariabonker.service import Service, ServiceError

from test_inventory import ARR, LIFE, _mem

DIRT = 2
SWORD = 3507


def _svc(items):
    """A Service over a synthetic image, with locating stubbed out."""
    m = _mem(items)
    svc = Service(m)
    svc._all_inventories = lambda: [Inventory(m, LIFE)]
    return svc, m


def test_write_proceeds_when_slot_still_holds_the_expected_item():
    svc, m = _svc([(DIRT, 10, -1)])
    svc.set_item(0, DIRT, stack=99, expect_type=DIRT)
    assert Inventory(m, LIFE).read_slot(0).stack == 99


def test_write_refused_when_item_changed_in_game():
    svc, m = _svc([(SWORD, 1, 15)])          # the game now holds a sword here
    before = bytes(m.buf)
    with pytest.raises(ServiceError) as e:
        svc.set_item(0, DIRT, stack=99, expect_type=DIRT)   # grid still believed dirt
    assert bytes(m.buf) == before, "a refused write must not touch memory"
    msg = str(e.value)
    assert names.label(SWORD) in msg and names.label(DIRT) in msg


def test_write_refused_when_slot_emptied_in_game():
    svc, m = _svc([(0, 0, 0)])
    before = bytes(m.buf)
    with pytest.raises(ServiceError):
        svc.set_item(0, DIRT, stack=5, expect_type=DIRT)
    assert bytes(m.buf) == before


def test_placing_into_a_slot_filled_since_the_snapshot_is_refused():
    """The empty-slot case: the grid showed empty (type 0), the game filled it."""
    svc, m = _svc([(SWORD, 1, 15)])
    before = bytes(m.buf)
    with pytest.raises(ServiceError):
        svc.set_item(0, DIRT, stack=1, expect_type=0)
    assert bytes(m.buf) == before


def test_unreadable_slot_is_refused_rather_than_written():
    """A null item pointer means our addresses are wrong; fail safe, never write."""
    svc, m = _svc([(DIRT, 10, -1)])
    m.write(ARR + ARR_DATA_OFF, b"\x00\x00\x00\x00")     # slot 0 -> null Item*
    before = bytes(m.buf)
    with pytest.raises(ServiceError) as e:
        svc.set_item(0, DIRT, stack=99, expect_type=DIRT)
    assert bytes(m.buf) == before
    assert "could not be read" in str(e.value)


def test_expect_type_omitted_keeps_legacy_behaviour():
    """CLI users and scripts that don't pass --expect-type are unaffected."""
    svc, m = _svc([(DIRT, 10, -1)])
    svc.set_item(0, DIRT, stack=42)
    assert Inventory(m, LIFE).read_slot(0).stack == 42


def test_guard_checks_before_any_field_write():
    """Not even the non-type fields leak through on a refused write."""
    svc, m = _svc([(SWORD, 1, 15)])
    addr = Inventory(m, LIFE)._item_addr(0)
    stack_before = m.read(addr + ITEM_STACK, 4)
    with pytest.raises(ServiceError):
        svc.set_item(0, SWORD, stack=777, damage=999, prefix=81, expect_type=DIRT)
    assert m.read(addr + ITEM_STACK, 4) == stack_before


def test_argv_builder_carries_expect_type():
    """The GUI's argv contract: every slot write states what it believed."""
    from terrariabonker.gui import client
    argv = client.set_item_argv(5, DIRT, stack=1, expect_type=SWORD)
    assert argv[:3] == ["set-item", "5", str(DIRT)]
    assert "--expect-type" in argv and argv[argv.index("--expect-type") + 1] == str(SWORD)


def test_argv_builder_omits_expect_type_when_not_given():
    from terrariabonker.gui import client
    assert "--expect-type" not in client.set_item_argv(5, DIRT, stack=1)
