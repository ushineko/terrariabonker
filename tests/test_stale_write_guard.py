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


def _svc(items, live=None):
    """A Service over a synthetic image, with locating stubbed out.

    ``live`` is the copy the *game* is using -- what `inventory()` reports and what the
    guard must check. It defaults to the same block, but the two are separable because
    the copies are not identical: an inert snapshot holds whatever its slots contained
    when it was taken.
    """
    m = _mem(items)
    svc = Service(m)
    svc._all_inventories = lambda: [Inventory(m, LIFE)]
    svc._live_inventory = lambda: Inventory(m, LIFE if live is None else live)
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


def test_the_guard_checks_the_copy_the_player_can_actually_see():
    """Reported from the game: editing the last hotbar slot was refused for holding a
    Green Torch while both Terraria and the grid showed a regular Torch.

    The guard was reading ``_all_inventories()[0]`` -- an arbitrary copy, and not
    necessarily the live one. Writes go to every copy because the inert ones ignore them,
    but a *read* has to come from the copy the caller was looking at: `inventory()`
    reports the live one, so checking any other compares against something the user never
    saw. The copies are demonstrably not identical.
    """
    from test_inventory import _mem as mkmem

    TORCH, GREEN_TORCH = 8, 974
    m = mkmem([(TORCH, 1, 0)])
    svc = Service(m)

    # An inert copy that still believes the slot holds a green torch. It accepts writes
    # and drops them, which is exactly what a snapshot does in the game.
    class StaleInv:
        def read_slot(self, i):
            class S:
                type = GREEN_TORCH
            return S()

        def __getattr__(self, _name):
            return lambda *a, **k: None

    live = Inventory(m, LIFE)
    svc._all_inventories = lambda: [StaleInv(), live]     # stale copy first, as it was
    svc._live_inventory = lambda: live

    # the caller saw a Torch (because inventory() reports the live copy) and edits it
    svc.set_item(0, TORCH, stack=42, expect_type=TORCH)
    assert Inventory(m, LIFE).read_slot(0).stack == 42, \
        "the edit was refused against a copy the player never saw"


def test_give_picks_a_free_slot_from_the_copy_the_game_is_using():
    """The same bug as the stale guard, but this one loses an item instead of refusing.

    `give_item` picks the first empty slot. Read that from an inert snapshot and it can
    choose a slot that is empty in the snapshot and occupied in the game -- the give then
    lands on a real item and destroys it.
    """
    from terrariabonker.service import GIVE_RANGE
    from test_inventory import _mem as mkmem

    DIRT_, SWORD_ = 2, 3507
    first = sorted(GIVE_RANGE)[0]

    # the game HAS a sword in the first give slot; the slots after it are free
    m = mkmem([(SWORD_, 1, 0)] + [(0, 0, 0)] * 5)
    svc = Service(m)
    live = Inventory(m, LIFE)

    class StaleInv:
        """A snapshot from before the sword was picked up: it thinks the slot is free."""

        def slots(self):
            class S:
                index = first
                empty = True
            return [S()]

        def __getattr__(self, _name):
            return lambda *a, **k: None

    svc._all_inventories = lambda: [StaleInv(), live]
    svc._live_inventory = lambda: live
    svc._template_block = lambda t: None

    got = svc.give_item(DIRT_, 1)
    assert got != first, "gave into a slot the game is using — the sword was destroyed"
    assert Inventory(m, LIFE).read_slot(first).type == SWORD_, "the real item was lost"
