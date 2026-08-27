"""Auto-catch wiring (spec 043): the round, the gate, and the panel switch.

The stub itself is tested in test_patcher; this is about *when* it gets armed. The
service is built by hand rather than against a real process, so the interesting parts
-- one arm per bite, no arm on an empty signal, recast gated on the player having cast
-- are testable without a game.
"""

import pytest

from terrariabonker import service as S
from terrariabonker.service import ServiceError
from terrariabonker import projectiles as P


class FakeInventory:
    def __init__(self, holding=True):
        self.holding = holding

    def holding_rod(self):
        return self.holding


class FakePatcher:
    def __init__(self, enabled=True):
        self.enabled = enabled
        self.arms = 0
        self.presses = 0

    def is_enabled(self, name):
        return self.enabled and name == "auto_use"

    def auto_use_arm(self):
        self.arms += 1
        self.presses += 1        # the stub consumes it on the next frame
        return True

    def auto_use_armed(self):
        return False             # already consumed

    def auto_use_presses(self):
        return self.presses


def _bobber(*, biting, catch=2290, slot=3):
    ai = (0.0, -240.0 if biting else 0.0, 0.0)
    local = (0.0, float(catch) if biting else 651.0, 0.0)
    return P.Bobber(slot=slot, addr=0x1000, ai=ai, local_ai=local)


def _service(monkeypatch, water, patcher):
    """A Service whose only live parts are the patcher and what is in the water."""
    svc = S.Service.__new__(S.Service)
    svc.mem = object()
    svc._proj_arr = 0xC0FFEE
    svc._last_reel = 0.0
    svc._seen_cast = True          # most tests are mid-session; the gate has its own test
    svc._live_inventory = lambda: FakeInventory(holding=True)
    svc.patcher = lambda: patcher
    monkeypatch.setattr(P, "find_bobbers", lambda mem, arr: list(water))
    monkeypatch.setattr(P, "find_bite",
                        lambda mem, arr: next((b for b in water if b.biting), None))
    return svc


def test_a_bite_is_reeled_in_once(monkeypatch):
    p = FakePatcher()
    svc = _service(monkeypatch, [_bobber(biting=True)], p)
    got = svc.catch_tick(budget=0.05)
    assert p.arms == 1
    assert got["events"] == [{"what": "reel", "catch": 2290, "slot": 3}]


def test_a_climbing_counter_is_not_a_bite(monkeypatch):
    """The bobber is in the water with the counter most of the way up: not a fish."""
    p = FakePatcher()
    svc = _service(monkeypatch, [_bobber(biting=False)], p)
    got = svc.catch_tick(budget=0.05)
    assert p.arms == 0 and got["events"] == []


def test_empty_water_is_left_alone_by_default(monkeypatch):
    """Without --recast the cheat takes fish and never casts."""
    p = FakePatcher()
    svc = _service(monkeypatch, [], p)
    got = svc.catch_tick(budget=0.05)
    assert p.arms == 0 and got["events"] == []


def test_a_cast_is_only_reported_when_a_bobber_appears(monkeypatch):
    """Arming is not casting.

    The first version logged "cast the line" the moment it armed, and was caught taking
    credit for the player's own casts. Only a bobber in the water proves it.
    """
    p = FakePatcher()
    svc = _service(monkeypatch, [], p)
    svc.CAST_CONFIRM = 0.05
    got = svc.catch_tick(recast=True, budget=0.05)
    assert p.arms == 1
    assert got["events"] == [{"what": "cast", "confirmed": False}]


def test_a_confirmed_cast_is_the_bobber_going_out(monkeypatch):
    p = FakePatcher()
    water: list = []
    svc = _service(monkeypatch, water, p)
    real = p.auto_use_arm

    def arm_and_cast():
        water.append(_bobber(biting=False))
        return real()

    p.auto_use_arm = arm_and_cast
    got = svc.catch_tick(recast=True, budget=0.05)
    assert got["events"] == [{"what": "cast", "confirmed": True}]


def test_recast_waits_until_the_player_has_cast_once(monkeypatch):
    """The gate that stops the cheat following a player away from the lake.

    Standing at the water holding a rod, having cast nothing, must produce nothing: the
    game gives no signal for "I meant to stop", so the loop earns the right to cast by
    seeing the player cast first.
    """
    p = FakePatcher()
    svc = _service(monkeypatch, [], p)
    svc._seen_cast = False
    got = svc.watch_catch(recast=True, rounds=3)
    assert p.arms == 0 and got["cast"] == 0


def test_the_gate_lives_in_the_round_so_both_callers_share_it(monkeypatch):
    """The panel drives catch_tick directly and never calls watch_catch.

    With the gate in the loop instead, ticking the panel checkbox would cast at once
    while the CLI waited -- the same cheat behaving differently depending on which
    surface you used.
    """
    p = FakePatcher()
    svc = _service(monkeypatch, [], p)
    svc._seen_cast = False
    svc.catch_tick(recast=True, budget=0.05)
    assert p.arms == 0, "the tick cast without the player having cast first"


def test_a_bobber_in_the_water_opens_the_gate(monkeypatch):
    p = FakePatcher()
    water = [_bobber(biting=False)]
    svc = _service(monkeypatch, water, p)
    svc._seen_cast = False
    svc.catch_tick(recast=True, budget=0.05)
    assert svc._seen_cast is True


def test_switching_off_closes_the_gate_again(monkeypatch):
    """Off and on again means start over, not "they cast once, minutes ago"."""
    p = FakePatcher()
    svc = _service(monkeypatch, [], p)
    svc._seen_cast = True
    svc.catch_stop()
    assert svc._seen_cast is False


def test_the_cheat_must_be_on(monkeypatch):
    """Nothing can arm a stub that is not installed, and saying so beats failing quietly."""
    p = FakePatcher(enabled=False)
    svc = _service(monkeypatch, [_bobber(biting=True)], p)
    with pytest.raises(ServiceError, match="auto-use"):
        svc.catch_tick()


def test_stop_forgets_the_located_array(monkeypatch):
    p = FakePatcher()
    svc = _service(monkeypatch, [], p)
    assert svc.catch_stop() == {"stopped": True}
    assert svc._proj_arr is None


# --- the panel switch ---------------------------------------------------------

@pytest.fixture
def qt_app():
    from PyQt6.QtWidgets import QApplication

    yield QApplication.instance() or QApplication([])


def _window(monkeypatch):
    from terrariabonker.gui import main_window as mw

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)
    return mw.MainWindow()


def test_panel_switch_starts_and_stops_the_watch(qt_app, monkeypatch):
    """Unticking stops the arming and touches nothing else.

    Auto-use is a separate cheat on the Patches tab; this switch decides *when* to arm
    it, so switching off must not reach into the game and change what the player set.
    """
    w = _window(monkeypatch)
    try:
        sent = []
        w.helper.available = True
        monkeypatch.setattr(w.helper, "request",
                            lambda argv, cb: (sent.append(argv), cb("{}"), True)[-1])
        w.cb_catch.setChecked(True)
        assert w._catch_timer.isActive()
        w.cb_catch.setChecked(False)
        assert not w._catch_timer.isActive()
        assert sent[-1] == ["catch-stop", "--json"], sent
        assert not any("patch" in a for argv in sent for a in argv), sent
    finally:
        w.close()


def test_panel_unticks_itself_when_auto_use_is_off(qt_app, monkeypatch):
    """The commonest mistake: ticking this without the cheat that presses the button.

    Say it once and untick, rather than logging the same error every 50 ms.
    """
    w = _window(monkeypatch)
    try:
        w.helper.available = True
        monkeypatch.setattr(
            w.helper, "request",
            lambda argv, cb: (cb("[ERROR] the auto-use cheat is not enabled"), True)[-1])
        w.cb_catch.setChecked(True)
        w._tick_catch()
        assert not w.cb_catch.isChecked()
        lines = [ln for ln in w.log.toPlainText().splitlines() if "[catch]" in ln]
        assert len(lines) == 1 and "auto-use" in lines[0]
    finally:
        w.close()


def test_panel_logs_what_was_caught(qt_app, monkeypatch):
    w = _window(monkeypatch)
    try:
        w.helper.available = True
        monkeypatch.setattr(
            w.helper, "request",
            lambda argv, cb: (cb('{"events": [{"what": "reel", "catch": 2290}]}'), True)[-1])
        w._tick_catch()
        assert "Bass" in w.log.toPlainText()
    finally:
        w.close()


def test_a_cast_is_not_attempted_straight_after_a_reel(monkeypatch):
    """The rod is still in its use animation for a few frames after the pull.

    Measured in the game: every catch produced "tried to cast and no line went out"
    followed by "cast the line" — one wasted press per fish, going somewhere unexamined.
    """
    import time

    p = FakePatcher()
    svc = _service(monkeypatch, [], p)
    svc._last_reel = time.time()
    got = svc.catch_tick(recast=True, budget=0.05)
    assert p.arms == 0 and got["events"] == []


def test_the_cast_box_is_dead_until_reeling_is_on(qt_app, monkeypatch):
    """"and cast" does nothing on its own — it is a modifier on the watch, not a cheat."""
    w = _window(monkeypatch)
    try:
        assert not w.cb_recast.isEnabled()
        w.cb_catch.setChecked(True)
        assert w.cb_recast.isEnabled()
        w.cb_catch.setChecked(False)
        assert not w.cb_recast.isEnabled()
    finally:
        w.close()


def test_the_cast_box_reaches_the_worker(qt_app, monkeypatch):
    w = _window(monkeypatch)
    try:
        sent = []
        w.helper.available = True
        monkeypatch.setattr(w.helper, "request",
                            lambda argv, cb: (sent.append(argv), cb("{}"), True)[-1])
        w.cb_catch.setChecked(True)
        w._tick_catch()
        assert "--recast" not in sent[-1]
        w.cb_recast.setChecked(True)
        w._tick_catch()
        assert "--recast" in sent[-1]
    finally:
        w.close()


def test_the_panel_logs_a_cast_only_when_the_line_went_out(qt_app, monkeypatch):
    w = _window(monkeypatch)
    try:
        w.helper.available = True
        monkeypatch.setattr(
            w.helper, "request",
            lambda argv, cb: (cb('{"events": [{"what": "cast", "confirmed": false}]}'),
                              True)[-1])
        w._tick_catch()
        assert "no line went out" in w.log.toPlainText()
    finally:
        w.close()


def test_a_cast_that_goes_nowhere_stops_the_casting(monkeypatch):
    """Reported from the game: with "and cast" on, swapping to a sword swung the sword.

    The cheat cannot see what is in the player's hand, so it cannot tell a rod from a
    pickaxe -- but it can tell that pressing produced no line, and that is enough to
    stop. One stray press is a bug; a stream of them is a different program.
    """
    p = FakePatcher()
    svc = _service(monkeypatch, [], p)
    svc.CAST_CONFIRM = 0.02
    svc.catch_tick(recast=True, budget=0.05)          # one press, no bobber
    assert p.arms == 1
    assert svc._seen_cast is False, "the gate stayed open after a cast went nowhere"
    svc.catch_tick(recast=True, budget=0.05)          # and no more
    svc.catch_tick(recast=True, budget=0.05)
    assert p.arms == 1


def test_a_real_cast_reopens_the_gate(monkeypatch):
    """Picking the rod back up and casting must start it going again."""
    p = FakePatcher()
    water: list = []
    svc = _service(monkeypatch, water, p)
    svc.CAST_CONFIRM = 0.02
    svc.catch_tick(recast=True, budget=0.05)
    assert svc._seen_cast is False
    water.append(_bobber(biting=False))               # the player casts
    svc.catch_tick(recast=True, budget=0.05)
    assert svc._seen_cast is True


def test_nothing_is_cast_with_a_sword_in_hand(monkeypatch):
    """Reported from the game: "and cast" recast anything you wield, not just rods.

    The use button is not fishing-specific, so an empty-water press against a weapon is
    a swing. The water is the wrong thing to look at on its own; the hand decides.
    """
    p = FakePatcher()
    svc = _service(monkeypatch, [], p)
    svc._live_inventory = lambda: FakeInventory(holding=False)
    got = svc.catch_tick(recast=True, budget=0.05)
    assert p.arms == 0 and got["events"] == []


def test_reeling_does_not_need_a_rod_in_hand(monkeypatch):
    """A bite can only exist because a rod cast it, and the pull path does not care what
    is held now. Gating the reel on the hand would drop fish for no reason."""
    p = FakePatcher()
    svc = _service(monkeypatch, [_bobber(biting=True)], p)
    svc._live_inventory = lambda: FakeInventory(holding=False)
    got = svc.catch_tick(budget=0.05)
    assert p.arms == 1 and got["events"][0]["what"] == "reel"
