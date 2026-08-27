"""Fishing potion effects without the potions, and who wins when both are running.

The precedence rule is the whole point of these tests: a potion the player drank, and the
passive-potions cheat, must both beat this. That falls out of Buffs.renew refusing to
shorten a buff -- but "it follows from something else" is exactly the kind of claim that
stops being true after a refactor, so it is pinned here.
"""

import struct

import pytest

from terrariabonker import buffs as B
from terrariabonker import service as S
from terrariabonker.inventory import ARR_DATA_OFF, ARR_LEN_OFF

BASE = 0x60000000
LIFE = BASE + 0x4000
TYPES = BASE + 0x100
TIMES = BASE + 0x400
SLOTS = 44

FISHING, SONAR, CRATE = 121, 122, 123


@pytest.fixture
def game():
    from conftest import FakeMem

    m = FakeMem(BASE, 0x8000)
    for ptr, off in ((TYPES, B.BUFF_TYPE_PTR_OFF), (TIMES, B.BUFF_TIME_PTR_OFF)):
        m.write(LIFE + off, struct.pack("<I", ptr))
        m.poke_i32(ptr + ARR_LEN_OFF, SLOTS)
    svc = S.Service.__new__(S.Service)
    svc.mem = m

    class Block:
        life_addr = LIFE

    svc.live_block = lambda: Block()
    return m, svc


def buff_at(m, i):
    return (m.read_i32(TYPES + ARR_DATA_OFF + i * 4), m.read_i32(TIMES + ARR_DATA_OFF + i * 4))


def test_the_buff_ids_come_from_the_game_not_from_memory():
    """Read out of ContentSamples: items 2354/2355/2356 carry buffType 121/122/123."""
    assert S.Service.FISHING_BUFFS["power"][0] == FISHING
    assert S.Service.FISHING_BUFFS["sonar"][0] == SONAR
    assert S.Service.FISHING_BUFFS["crate"][0] == CRATE


def test_each_effect_is_its_own_switch(game):
    m, svc = game
    got = svc.fishing_buff_tick(power=True)
    assert [d["buff"] for d in got["held"]] == [FISHING]
    assert buff_at(m, 0)[0] == FISHING


def test_all_three_together(game):
    m, svc = game
    got = svc.fishing_buff_tick(power=True, sonar=True, crate=True)
    assert sorted(d["buff"] for d in got["held"]) == [FISHING, SONAR, CRATE]


def test_nothing_ticked_writes_nothing(game):
    m, svc = game
    assert svc.fishing_buff_tick() == {"held": [], "deferred": []}
    assert buff_at(m, 0) == (0, 0)


def test_a_potion_the_player_drank_is_left_alone(game):
    """Eight minutes of a real Fishing Potion must not become two seconds.

    This is the precedence the maintainer asked for: the potion -- and by extension the
    passive-potions cheat that renews one -- beats the fishing trainer's copy.
    """
    m, svc = game
    m.poke_i32(TIMES + ARR_DATA_OFF, 28800)          # 8 minutes, as the potion gives
    m.poke_i32(TYPES + ARR_DATA_OFF, FISHING)
    got = svc.fishing_buff_tick(power=True)
    assert buff_at(m, 0) == (FISHING, 28800), "the trainer shortened a drunk potion"
    assert [d["effect"] for d in got["deferred"]] == ["power"]
    assert got["held"] == []


def test_a_longer_running_effect_is_reported_as_deferred_not_held(game):
    """The panel says it deferred rather than claiming it did the work."""
    m, svc = game
    m.poke_i32(TIMES + ARR_DATA_OFF, 28800)
    m.poke_i32(TYPES + ARR_DATA_OFF, CRATE)
    got = svc.fishing_buff_tick(sonar=True, crate=True)
    assert [d["effect"] for d in got["deferred"]] == ["crate"]
    assert [d["effect"] for d in got["held"]] == ["sonar"]


def test_it_never_removes_a_buff(game):
    """Switching off must drop only what this was holding: buffs lapse, they are not cut.

    Nothing here writes a zero -- the effect fades on its own a couple of seconds after
    the renewals stop, exactly as a campfire's does.
    """
    m, svc = game
    svc.fishing_buff_tick(power=True, sonar=True)
    before = [buff_at(m, i) for i in range(4)]
    svc.fishing_buff_tick()                           # everything unticked
    assert [buff_at(m, i) for i in range(4)] == before


def test_a_watch_interval_that_would_let_it_lapse_is_refused(game):
    _, svc = game
    with pytest.raises(S.ServiceError, match="lapse"):
        svc.watch_fishing_buffs(power=True, interval=5.0, rounds=1)


# --- the panel switches -------------------------------------------------------

def test_the_boxes_run_independently_of_the_fishing_cheat(gui_window, monkeypatch):
    """They are separate effects, so they must not need the rod-and-bait cheat on."""
    w = gui_window()
    try:
        assert not w._buff_timer.isActive()
        w.cb_buff_sonar.setChecked(True)
        assert w._buff_timer.isActive()
        assert not w.cb_fishing.isChecked(), "it turned the fishing cheat on by itself"
    finally:
        w.close()


def test_the_watch_stops_only_when_all_three_are_clear(gui_window, monkeypatch):
    w = gui_window()
    try:
        w.cb_buff_power.setChecked(True)
        w.cb_buff_crate.setChecked(True)
        w.cb_buff_power.setChecked(False)
        assert w._buff_timer.isActive(), "it stopped while Crates was still ticked"
        w.cb_buff_crate.setChecked(False)
        assert not w._buff_timer.isActive()
    finally:
        w.close()


def test_the_ticked_boxes_reach_the_worker(gui_window, monkeypatch):
    w = gui_window()
    try:
        sent = []
        w.helper.available = True
        monkeypatch.setattr(w.helper, "request",
                            lambda argv, cb: (sent.append(argv), cb("{}"), True)[-1])
        w.cb_buff_power.setChecked(True)
        w.cb_buff_crate.setChecked(True)
        w._tick_fishing_buffs()
        assert "--power" in sent[-1] and "--crate" in sent[-1]
        assert "--sonar" not in sent[-1]
    finally:
        w.close()


def test_a_deferral_is_logged_once_not_every_second(gui_window, monkeypatch):
    """It defers on every round for as long as the potion runs -- eight minutes of it
    would bury the panel, which is the bug spec 042 already fixed once for bait."""
    w = gui_window()
    try:
        w.helper.available = True
        reply = ('{"held": [], "deferred": [{"effect": "power", "buff": 121, '
                 '"name": "Fishing Potion", "what": "kept"}]}')
        monkeypatch.setattr(w.helper, "request",
                            lambda argv, cb: (cb(reply), True)[-1])
        w.cb_buff_power.setChecked(True)
        for _ in range(5):
            w._tick_fishing_buffs()
        lines = [ln for ln in w.log.toPlainText().splitlines() if "Fishing Potion" in ln]
        assert len(lines) == 1, lines
    finally:
        w.close()


def test_our_own_renewal_is_not_mistaken_for_a_potion(game):
    """`renew` says "kept" for two different situations and only one is a deferral.

    Caught live: a second round a moment after the first reported "left Sonar Potion alone
    -- a potion is already running it" when the only thing running it was the round before.
    """
    m, svc = game
    svc.fishing_buff_tick(sonar=True)                 # ours, 120 ticks
    got = svc.fishing_buff_tick(sonar=True)           # again, immediately
    assert got["deferred"] == [], "it thought its own renewal was somebody else's potion"
    assert [d["effect"] for d in got["held"]] == ["sonar"]
