"""The player's buff arrays (spec 041) — read, and renew without ever shortening."""

import struct

import pytest

from terrariabonker import buffs as B
from terrariabonker.inventory import ARR_DATA_OFF, ARR_LEN_OFF

BASE = 0x20000000
LIFE = BASE + 0x2000
TYPE_ARR = BASE + 0x100
TIME_ARR = BASE + 0x400
SLOTS = 44


def _game(active=(), slots=SLOTS):
    """A player whose buff arrays hold ``active`` as (slot, type, ticks)."""
    from conftest import FakeMem

    m = FakeMem(BASE, 0x4000)
    for ptr_off, arr in ((B.BUFF_TYPE_PTR_OFF, TYPE_ARR), (B.BUFF_TIME_PTR_OFF, TIME_ARR)):
        m.write(LIFE + ptr_off, struct.pack("<I", arr))
        m.poke_i32(arr + ARR_LEN_OFF, slots)
        for i in range(slots):
            m.poke_i32(arr + ARR_DATA_OFF + i * 4, 0)
    for slot, t, ticks in active:
        m.poke_i32(TYPE_ARR + ARR_DATA_OFF + slot * 4, t)
        m.poke_i32(TIME_ARR + ARR_DATA_OFF + slot * 4, ticks)
    return m, B.Buffs(m, LIFE)


def _read(m, arr, slot):
    return struct.unpack("<i", m.read(arr + ARR_DATA_OFF + slot * 4, 4))[0]


class _Recorder:
    """Wraps a FakeMem to record the order of writes."""

    def __init__(self, m):
        self._m, self.writes = m, []

    def read(self, addr, size):
        return self._m.read(addr, size)

    def write(self, addr, data):
        self.writes.append((addr, data))
        return self._m.write(addr, data)


# --- reading ------------------------------------------------------------------

def test_active_reports_only_occupied_slots():
    _, b = _game([(0, 5, 18000), (3, 87, 1)])
    assert b.active() == {0: (5, 18000), 3: (87, 1)}


def test_the_array_is_44_slots_not_22():
    """A search for a 22-element array finds nothing on 1.4.5.8, which is exactly how the
    first attempt to locate these missed them."""
    _, b = _game()
    assert b.slots() == 44


def test_time_of_finds_a_buff_wherever_it_sits():
    _, b = _game([(7, 5, 900)])
    assert b.time_of(5) == 900
    assert b.time_of(6) == 0


def test_a_null_pointer_is_an_error_not_a_wrong_answer():
    m, b = _game()
    m.write(LIFE + B.BUFF_TYPE_PTR_OFF, struct.pack("<I", 0))
    with pytest.raises(B.BuffError):
        b.active()


def test_an_absurd_length_is_refused():
    """A stale or wrong pointer usually reads as a huge length; treating that as a real
    array would walk megabytes and report nonsense as buffs."""
    m, b = _game()
    m.poke_i32(TYPE_ARR + ARR_LEN_OFF, 999999)
    with pytest.raises(B.BuffError):
        b.active()


# --- renewing -----------------------------------------------------------------

def test_renewing_an_absent_buff_fills_a_free_slot():
    m, b = _game([(0, 5, 100)])
    assert b.renew(11, 120) == "added"
    assert _read(m, TYPE_ARR, 1) == 11
    assert _read(m, TIME_ARR, 1) == 120


def test_renewing_a_shorter_buff_extends_it():
    m, b = _game([(2, 11, 30)])
    assert b.renew(11, 120) == "renewed"
    assert _read(m, TIME_ARR, 2) == 120
    assert _read(m, TYPE_ARR, 2) == 11, "the slot was re-typed"


def test_a_potion_the_player_drank_is_never_shortened():
    """The sharpest failure mode in spec 041. Drinking Ironskin gives ~8 minutes; a
    renewal loop that wrote its own short time over that would leave the player with two
    seconds the moment they dropped the stack -- and they would blame the potion."""
    m, b = _game([(0, 5, 28800)])
    assert b.renew(5, 120) == "kept"
    assert _read(m, TIME_ARR, 0) == 28800


def test_a_buff_with_exactly_the_asked_for_time_is_left_alone():
    """The renewal loop asks for the same figure every round, so equality is the common
    case, not an edge one: a boundary that wrote on equality would rewrite the same value
    into the game several times a second forever."""
    m, b = _game([(0, 5, 120)])
    rec = _Recorder(m)
    b.mem = rec
    assert b.renew(5, 120) == "kept"
    assert rec.writes == []


def test_keeping_a_longer_buff_writes_nothing_at_all():
    """Not merely 'writes the larger value'. Every write to a live game is a chance to
    corrupt something, and this one has no reason to happen."""
    m, b = _game([(0, 5, 28800)])
    rec = _Recorder(m)
    b.mem = rec
    assert b.renew(5, 120) == "kept"
    assert rec.writes == [], "a no-op renewal touched the game's memory"


def test_a_full_bar_is_reported_not_forced():
    m, b = _game([(i, 100 + i, 60) for i in range(SLOTS)])
    assert b.renew(11, 120) == "full"
    assert b.time_of(11) == 0


def test_a_new_buff_gets_its_time_before_it_gets_its_type():
    """Until the type is set the game ignores the slot, so a torn write is an empty slot
    rather than a buff with no time on it. NPC spawning writes `active` last for the same
    reason."""
    m, b = _game()
    rec = _Recorder(m)
    b.mem = rec
    b.renew(11, 120)
    addrs = [a for a, _ in rec.writes]
    assert addrs == [TIME_ARR + ARR_DATA_OFF, TYPE_ARR + ARR_DATA_OFF]


def test_nonsense_arguments_are_refused():
    _, b = _game()
    with pytest.raises(ValueError):
        b.renew(0, 120)
    with pytest.raises(ValueError):
        b.renew(11, 0)


# --- the service round (spec 041) ---------------------------------------------

def _service(potions, active=(), stacks=None):
    """A Service whose live inventory yields `potions` as (slot, buff type).

    `stacks` maps slot -> stack size, so the fake applies the same threshold the real
    inventory does and the service's pass-through of min_stack is actually exercised.
    """
    from terrariabonker import service as S

    m, bar = _game(active)
    held = dict(stacks or {})

    class FakeInv:
        def favorited_potions(self, min_stack=1):
            return [(sl, b) for sl, b in potions if held.get(sl, 1) >= min_stack]

    svc = S.Service.__new__(S.Service)
    svc.mem = m
    svc._live_inventory = lambda: FakeInv()
    svc.live_block = lambda: type("B", (), {"life_addr": LIFE})()
    return m, svc


def test_a_round_adds_a_buff_for_each_carried_potion():
    m, svc = _service([(40, 5), (41, 3)])
    got = svc.potion_tick()
    assert got["carried"] == 2
    assert [e["buff"] for e in got["added"]] == [5, 3]
    assert B.Buffs(m, LIFE).time_of(5) == B.DEFAULT_TICKS


def test_a_round_leaves_a_drunk_potion_alone_and_says_so():
    """The reported outcome matters as much as the memory: 'kept' is how the caller knows
    it did not stamp on something the player did by hand."""
    m, svc = _service([(41, 5)], active=[(0, 5, 28800)])
    got = svc.potion_tick()
    assert got["kept"] == [{"slot": 41, "buff": 5}]
    assert got["added"] == []
    assert B.Buffs(m, LIFE).time_of(5) == 28800


def test_carrying_nothing_favorited_does_nothing():
    m, svc = _service([])
    got = svc.potion_tick()
    assert got["carried"] == 0
    assert B.Buffs(m, LIFE).active() == {}


def test_a_refresh_interval_that_cannot_hold_the_buff_up_is_refused():
    """A 5s interval renewing a 2s buff leaves the player with a buff that flickers off
    three seconds in every five. That is a misconfiguration, not a degraded mode."""
    import pytest as _pytest

    from terrariabonker.service import ServiceError
    _, svc = _service([(40, 5)])
    with _pytest.raises(ServiceError):
        svc.watch_potions(interval=5.0, ticks=120, rounds=1)


def test_a_sane_interval_is_accepted_and_applies():
    _, svc = _service([(40, 5)])
    got = svc.watch_potions(interval=0.0, ticks=120, rounds=1)
    assert got["rounds"] == 1 and got["applied"] == 1


def test_the_stack_threshold_reaches_the_inventory():
    """The tunable is the user-facing half of the feature; a service that accepted it and
    dropped it would look like it worked until someone set it above 1."""
    m, svc = _service([(40, 5), (41, 3)], stacks={40: 10, 41: 2})
    assert svc.potion_tick(min_stack=1)["carried"] == 2
    assert [e["buff"] for e in svc.potion_tick(min_stack=5)["added"]] == []
    assert svc.potion_tick(min_stack=5)["carried"] == 1
