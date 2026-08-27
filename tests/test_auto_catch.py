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


def test_empty_water_is_left_alone(monkeypatch):
    """The cheat never casts. It takes fish; it does not fish.

    Casting needs a fresh press -- controlUseItem together with releaseUseItem -- and the
    stub only writes the first. An earlier version armed on empty water and reported
    "cast the line", which was measured to be false: six arms, six presses by the stub's
    own counter, and not one line in the water. The player casts.
    """
    p = FakePatcher()
    svc = _service(monkeypatch, [], p)
    got = svc.catch_tick(budget=0.05)
    assert p.arms == 0 and got["events"] == []


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
