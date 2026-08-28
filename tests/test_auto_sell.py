"""The auto-sell round: what it sells, what it refuses, and where the coins go (spec 048).

The rule that matters most here is the ordering: the coins are credited BEFORE the item is
taken. The other order loses the item whenever the credit cannot complete, and "no room in
the bank" is a routine state, not an edge case.
"""

import struct

import pytest

from terrariabonker import profile
from terrariabonker import selling as S
from terrariabonker.inventory import (ARR_DATA_OFF, ARR_LEN_OFF, INVENTORY_PTR_OFF,
                                      ITEM_FAVORITED, ITEM_STACK, ITEM_TYPE, Inventory)
from terrariabonker.service import Service

BASE = 0x50000000
LIFE = BASE + 0x8000
INV_ARR = BASE + 0x100
CHEST = BASE + 0x200
BANK_ARR = BASE + 0x300
INV_ITEMS = BASE + 0x10000
BANK_ITEMS = BASE + 0x40000

ORE = 12                     # a whitelisted item type
ORE_VALUE = 500              # sells for 100 copper each


def _mem(inv_rows, bank_rows=(), bank_len=S.BANK_SLOTS):
    """A player with an inventory and a Piggy Bank, both laid out like the game's."""
    from conftest import FakeMem
    m = FakeMem(BASE, 0x80000)
    m.write(LIFE + INVENTORY_PTR_OFF, struct.pack("<I", INV_ARR))
    m.poke_i32(INV_ARR + ARR_LEN_OFF, 59)
    for i in range(59):
        addr = INV_ITEMS + i * 0x200
        m.write(INV_ARR + ARR_DATA_OFF + i * 4, struct.pack("<I", addr))
        row = inv_rows[i] if i < len(inv_rows) else None
        t, stack, value, fav = row if row else (0, 0, 0, False)
        m.poke_i32(addr + ITEM_TYPE, t)
        m.poke_i32(addr + ITEM_STACK, stack)
        m.poke_i32(addr + S.ITEM_VALUE, value)
        m.write(addr + ITEM_FAVORITED, bytes([1 if fav else 0]))
    m.write(LIFE + S.BANK_PTR_OFF, struct.pack("<I", CHEST))
    m.write(CHEST + S.CHEST_ITEM_OFF, struct.pack("<I", BANK_ARR))
    m.poke_i32(BANK_ARR + ARR_LEN_OFF, bank_len)
    for i in range(S.BANK_SLOTS):
        addr = BANK_ITEMS + i * 0x200
        m.write(BANK_ARR + ARR_DATA_OFF + i * 4, struct.pack("<I", addr))
        t, stack = bank_rows[i] if i < len(bank_rows) else (0, 0)
        m.poke_i32(addr + ITEM_TYPE, t)
        m.poke_i32(addr + ITEM_STACK, stack)
    return m


def _svc(m, *, reachable=True):
    svc = Service(m)
    inv = Inventory(m, LIFE)
    svc._all_inventories = lambda: [inv]
    svc._live_inventory = lambda: inv
    svc.live_block = lambda: type("B", (), {"life_addr": LIFE})()
    svc._template_block = lambda t: None          # no ContentSamples in the image
    svc.bank_reachable = lambda **kw: {"reachable": reachable, "why": "test",
                                       "carried": [], "placed": reachable}
    return svc


@pytest.fixture(autouse=True)
def _whitelist(tmp_path, monkeypatch):
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    profile.set_sell_whitelist(ORE, True)
    yield


def _bank(m):
    return {r["index"]: (r["type"], r["stack"])
            for r in S.bank_container(m, LIFE).rows() if r["type"]}


def test_sells_a_whitelisted_stack_and_pays_into_the_bank():
    m = _mem([(ORE, 7, ORE_VALUE, False)])
    r = _svc(m).sell_tick()
    assert r["copper"] == 700 and r["destination"] == "bank"
    assert Inventory(m, LIFE).read_slot(0).empty
    assert _bank(m) == {0: (72, 7)}, "700 copper is seven silver, in one stack"


def test_leaves_everything_not_whitelisted_alone():
    m = _mem([(ORE, 2, ORE_VALUE, False), (999, 5, 10_000, False)])
    _svc(m).sell_tick()
    inv = Inventory(m, LIFE)
    assert inv.read_slot(0).empty
    assert (inv.read_slot(1).type, inv.read_slot(1).stack) == (999, 5)


def test_never_sells_a_favorited_stack():
    m = _mem([(ORE, 3, ORE_VALUE, True)])
    r = _svc(m).sell_tick()
    assert r["sold"] == [] and r["copper"] == 0
    assert [s["why"] for s in r["skipped"]] == ["favorited"]
    inv = Inventory(m, LIFE)
    assert (inv.read_slot(0).type, inv.read_slot(0).stack) == (ORE, 3)


def test_favorite_protects_one_stack_while_the_rest_of_the_type_sells():
    m = _mem([(ORE, 3, ORE_VALUE, True), (ORE, 4, ORE_VALUE, False)])
    r = _svc(m).sell_tick()
    inv = Inventory(m, LIFE)
    assert (inv.read_slot(0).type, inv.read_slot(0).stack) == (ORE, 3)
    assert inv.read_slot(1).empty
    assert r["copper"] == 400


def test_coins_go_to_the_inventory_when_the_bank_is_out_of_reach():
    m = _mem([(ORE, 1, ORE_VALUE, False)])
    r = _svc(m, reachable=False).sell_tick()
    assert r["destination"] == "inventory" and r["copper"] == 100
    assert _bank(m) == {}
    got = [s for s in Inventory(m, LIFE).slots() if s.type in S.COIN_TYPES]
    assert sum(s.stack for s in got) == 1        # one silver
    assert got[0].type == 72


def test_a_full_bank_overflows_into_the_inventory():
    full = [(999, 1)] * S.BANK_SLOTS
    m = _mem([(ORE, 1, ORE_VALUE, False)], bank_rows=full)
    r = _svc(m).sell_tick()
    assert r["destination"] == "bank+inventory"
    assert sum(s.stack * S.COIN_WORTH[S.COIN_TYPES.index(s.type)]
               for s in Inventory(m, LIFE).slots() if s.type in S.COIN_TYPES) == 100


def test_nothing_is_sold_when_the_coins_cannot_be_paid_anywhere():
    """The item must survive a failed credit. Selling first and paying second would
    destroy it here."""
    full = [(999, 1)] * S.BANK_SLOTS
    inv = [(ORE, 1, ORE_VALUE, False)] + [(999, 1, 0, False)] * 58
    m = _mem(inv, bank_rows=full)
    r = _svc(m).sell_tick()
    assert r["sold"] == [] and "error" in r
    slot = Inventory(m, LIFE).read_slot(0)
    assert (slot.type, slot.stack) == (ORE, 1), "the item must still be there"


def test_coins_merge_into_an_existing_stack():
    m = _mem([(ORE, 1, ORE_VALUE, False)], bank_rows=[(72, 3)])
    _svc(m).sell_tick()                    # 100 copper = 1 silver, onto a stack of 3
    assert _bank(m) == {0: (72, 4)}, "merged rather than opening a second stack"


def test_a_full_stack_is_promoted_the_way_docoins_does_it():
    """100 of a denomination becomes 1 of the next one up. Without this the credit is
    worth the right amount but leaves a state the game would never leave -- which is
    exactly what the first live run produced: a stack of 100 silver where 1 gold belongs."""
    m = _mem([(ORE, 1, ORE_VALUE, False)], bank_rows=[(72, 99)])
    _svc(m).sell_tick()                    # 99 + 1 silver = 100 -> 1 gold
    held = _bank(m)
    assert (73, 1) in held.values(), "the full silver stack became a gold"
    assert (72, 100) not in held.values()


def test_promotion_merges_into_an_existing_higher_stack():
    m = _mem([(ORE, 1, ORE_VALUE, False)], bank_rows=[(72, 99), (73, 4)])
    _svc(m).sell_tick()
    assert _bank(m) == {1: (73, 5)}, "the gold stack grew; the silver slot emptied"


def test_promotion_leaves_platinum_alone():
    """Platinum is the top denomination -- there is nothing to promote it into, and the
    game stacks it past 100 too."""
    m = _mem([(ORE, 1, ORE_VALUE, False)], bank_rows=[(74, 150)])
    _svc(m).sell_tick()
    assert _bank(m)[0] == (74, 150)


def test_value_is_conserved_across_a_promotion():
    m = _mem([(ORE, 1, ORE_VALUE, False)], bank_rows=[(72, 99)])
    before = 99 * 100
    r = _svc(m).sell_tick()
    after = sum(n * S.COIN_WORTH[S.COIN_TYPES.index(t)] for t, n in _bank(m).values())
    assert after == before + r["copper"]


def test_a_worthless_item_is_taken_and_pays_nothing():
    """For a junk drop "sell it" and "bin it" are the same request, so a whitelisted item
    worth 0 is still taken -- refusing would leave the one thing the player most wants
    gone sitting in the bag. It pays 0 rather than rounding up to something."""
    m = _mem([(ORE, 5, 0, False)])
    r = _svc(m).sell_tick()
    assert r["copper"] == 0
    assert [e["slot"] for e in r["sold"]] == [0]
    assert Inventory(m, LIFE).read_slot(0).empty


def test_trashing_a_worthless_item_puts_no_coins_anywhere():
    m = _mem([(ORE, 5, 0, False)])
    _svc(m).sell_tick()
    assert _bank(m) == {}, "nothing was paid, so nothing was written to the bank"


def test_a_favorited_worthless_item_is_still_protected():
    """The favorite is the override, and it must not depend on the item being worth
    something."""
    m = _mem([(ORE, 5, 0, True)])
    r = _svc(m).sell_tick()
    assert r["sold"] == []
    assert Inventory(m, LIFE).read_slot(0).type == ORE


def test_dry_run_changes_nothing():
    m = _mem([(ORE, 7, ORE_VALUE, False)])
    before = bytes(m.buf)
    r = _svc(m).sell_tick(dry_run=True)
    assert r["copper"] == 700 and r["dry_run"] is True
    assert bytes(m.buf) == before


def test_value_is_conserved_across_a_sale():
    m = _mem([(ORE, 13, ORE_VALUE, False)])
    r = _svc(m).sell_tick()
    paid = sum(n * S.COIN_WORTH[S.COIN_TYPES.index(t)] for t, n in _bank(m).values())
    assert paid == r["copper"] == S.sell_price(ORE_VALUE, 13)


# --- reachability: can the player open their bank IN THIS WORLD? -------------
# Player.bank is character state, not world state, so coins put there while the player has
# no way to open a piggy bank are not lost -- but they are unreachable, which is the
# outcome this gate exists to avoid.

class _FakeTiles:
    def __init__(self, hits, buf=1, w=100, h=100):
        self._hits, self.buf, self.max_x, self.max_y = hits, buf, w, h
        self.scans = 0

    def find_type(self, want, limit=0):
        self.scans += 1
        return list(self._hits)[:limit or None]


def _reach_svc(m, carried=(), placed=()):
    svc = Service(m)
    inv = Inventory(m, LIFE)
    svc._all_inventories = lambda: [inv]
    svc._live_inventory = lambda: inv
    svc.live_block = lambda: type("B", (), {"life_addr": LIFE})()
    tiles = _FakeTiles(placed)
    svc.tilemap = lambda: tiles
    return svc, tiles


def test_carrying_a_piggy_bank_makes_it_reachable_without_a_scan():
    """The item deploys anywhere, so there is nothing to search the world for."""
    m = _mem([(S.PIGGY_BANK_ITEM, 1, 0, False)])
    svc, tiles = _reach_svc(m)
    got = svc.bank_reachable()
    assert got["reachable"] and got["why"] == "carried"
    assert tiles.scans == 0, "carrying one must not cost a world scan"


def test_carrying_a_money_trough_also_counts():
    m = _mem([(S.MONEY_TROUGH_ITEM, 1, 0, False)])
    assert _reach_svc(m)[0].bank_reachable()["reachable"]


def test_a_placed_piggy_bank_makes_it_reachable():
    m = _mem([])
    svc, tiles = _reach_svc(m, placed=[(2114, 311)])
    got = svc.bank_reachable()
    assert got["reachable"] and got["why"] == "placed"
    assert tiles.scans == 1


def test_neither_carried_nor_placed_is_not_reachable():
    m = _mem([])
    svc, _ = _reach_svc(m)
    assert svc.bank_reachable()["reachable"] is False


def test_the_world_is_scanned_once_not_once_per_tick():
    """0.15s a tick would be unaffordable; the answer is cached per loaded world."""
    m = _mem([])
    svc, tiles = _reach_svc(m, placed=[(1, 1)])
    for _ in range(5):
        svc.bank_reachable()
    assert tiles.scans == 1


def test_a_new_world_is_rescanned():
    """A missed world change would leave the previous world's answer standing."""
    m = _mem([])
    svc, tiles = _reach_svc(m, placed=[(1, 1)])
    svc.bank_reachable()
    tiles.buf += 0x1000                 # the tile buffer moved: a different world
    svc.bank_reachable()
    assert tiles.scans == 2


def test_the_whitelist_survives_a_restart():
    """It lives in the profile, not the per-pid patch state, so a game restart keeps it."""
    profile.set_sell_whitelist(999, True)
    assert profile.sell_whitelist() == {ORE, 999}
    assert profile.load()["sell_whitelist"], "persisted, not held in memory"
    profile.set_sell_whitelist(999, False)
    assert profile.sell_whitelist() == {ORE}


# --- never write through a stale address ------------------------------------

def test_a_write_is_refused_when_the_slot_changed_underneath_it():
    """The rule AGENTS.md states and this code broke: mono moves objects, so an address
    read a few syscalls ago can name something else by the time it is written."""
    m = _mem([], bank_rows=[(72, 5)])
    c = S.bank_container(m, LIFE)
    row = c.read(0)
    m.poke_i32(row["addr"] + ITEM_TYPE, 999)          # something else lives here now
    assert not c.write(0, expect_type=72, expect_stack=5, stack=6)
    assert m.read_i32(row["addr"] + ITEM_STACK) == 5, "the stack was not touched"


def test_a_write_lands_when_the_slot_is_unchanged():
    m = _mem([], bank_rows=[(72, 5)])
    c = S.bank_container(m, LIFE)
    assert c.write(0, expect_type=72, expect_stack=5, stack=6)
    assert c.read(0)["stack"] == 6


def test_a_write_re_resolves_rather_than_reusing_an_address():
    """Point the array at a different object and the write must follow it."""
    m = _mem([], bank_rows=[(72, 5)])
    c = S.bank_container(m, LIFE)
    moved = BANK_ITEMS + 0x30000
    m.poke_i32(moved + ITEM_TYPE, 72)
    m.poke_i32(moved + ITEM_STACK, 5)
    m.write(BANK_ARR + ARR_DATA_OFF, struct.pack("<I", moved))
    assert c.write(0, expect_type=72, expect_stack=5, stack=9)
    assert m.read_i32(moved + ITEM_STACK) == 9, "wrote to where the slot points NOW"


def test_coins_never_go_past_the_slots_sellitem_uses():
    """SellItem's own loop stops at 53; slot 58 is not a normal slot and the grid hides
    it. A coin written outside that range is a coin in a slot the game treats specially."""
    assert S.COIN_SLOTS == 54
    assert S.SELL_SLOTS == 58
    inv = [(999, 1, 0, False)] * S.COIN_SLOTS        # 0..53 all full
    inv[0] = (ORE, 1, ORE_VALUE, False)
    m = _mem(inv, bank_rows=[(999, 1)] * S.BANK_SLOTS)
    r = _svc(m).sell_tick()
    assert r["sold"] == [] and "error" in r, "no room in 0..53 means no sale"
    for i in range(S.COIN_SLOTS, 59):
        assert Inventory(m, LIFE).read_slot(i).type in (0, 999), \
            "nothing was written past slot 53"
