"""The Projectiles tab (spec 047): what the controls actually store and send.

The panel is where a value stops being a number in a widget and becomes a write into a
running game, so the cases here are the translation ones -- a checkbox that means zero,
a projectile type shared by two weapons, and the switch going off.
"""

import pytest

pytest.importorskip("PyQt6.QtWidgets")


def test_pass_through_blocks_stores_a_zero(gui_window):
    """The checkbox reads as "pass through"; the field it writes is tileCollide = 0.

    Ticking a box named for the effect and storing the flag it clears is exactly the kind
    of inversion that gets written backwards, so it is pinned here.
    """
    w = gui_window()
    w._pj_select(837)
    w.cb_pj_nocollide.setChecked(True)
    assert w._proj_overrides == {837: {"tileCollide": 0}}


def test_clearing_every_box_forgets_the_type(gui_window):
    """An entry with no fields would send `{837: {}}` and enforce nothing, forever."""
    w = gui_window()
    w._pj_select(837)
    w.cb_pj_nocollide.setChecked(True)
    w.cb_pj_nocollide.setChecked(False)
    assert w._proj_overrides == {}


def test_overrides_are_kept_per_projectile_type(gui_window):
    w = gui_window()
    w._pj_select(837)
    w.cb_pj_nocollide.setChecked(True)
    w._pj_select(532)                           # as choosing another weapon does
    w.sp_pj_scale.setValue(3.0)
    w.cb_pj_scale.setChecked(True)
    assert w._proj_overrides == {837: {"tileCollide": 0}, 532: {"scale": 3.0}}


def test_switching_weapons_does_not_leak_the_previous_settings(gui_window):
    """`_pj_select` exists so the type and the controls cannot drift apart.

    Setting the type alone leaves the last weapon's boxes ticked, and the next edit
    writes them onto the new projectile with nothing on screen to say so.
    """
    w = gui_window()
    w._pj_select(837)
    w.cb_pj_nocollide.setChecked(True)
    w._pj_select(532)
    assert not w.cb_pj_nocollide.isChecked()
    w.sp_pj_scale.setValue(3.0)
    w.cb_pj_scale.setChecked(True)
    assert "tileCollide" not in w._proj_overrides[532]


def test_selecting_a_type_shows_what_was_stored_for_it(gui_window):
    """Switching weapons must not carry the previous weapon's settings across."""
    w = gui_window()
    w._proj_overrides = {837: {"tileCollide": 0, "timeLeft": 4200}}
    w._pj_load_fields(837)
    assert w.cb_pj_nocollide.isChecked() and w.sp_pj_life.value() == 4200

    w._pj_load_fields(532)                      # nothing stored for this one
    assert not w.cb_pj_nocollide.isChecked()
    assert not w.cb_pj_life.isChecked()


def test_nothing_is_stored_without_a_resolved_projectile(gui_window):
    """A weapon that fires nothing has `shoot == 0`, which is not a projectile type."""
    w = gui_window()
    w._pj_select(0)
    w.cb_pj_nocollide.setChecked(True)
    assert w._proj_overrides == {}


def test_switching_off_tells_the_worker_to_forget(gui_window):
    """Per-projectile state lives in the worker; leaving it would deny a set-once field."""
    w, calls = gui_window(record_calls=True)
    w.cb_projectiles.setChecked(True)
    assert w._proj_timer.isActive()
    w.cb_projectiles.setChecked(False)
    assert not w._proj_timer.isActive()
    assert ["projectile-stop", "--json"] in [argv for argv, _cb in calls]


class _FakeHelper:
    """A worker that is up and records what was asked of it."""

    available = True

    def __init__(self):
        self.sent: list[list[str]] = []

    def request(self, argv, on_output) -> bool:
        self.sent.append(argv)
        return True

    def stop(self) -> None:
        """The window stops the worker on close, and may be closed more than once."""


def test_a_tick_with_no_overrides_sends_nothing(gui_window):
    """Otherwise the panel polls a privileged worker 20x a second to write zero fields."""
    w = gui_window()
    w.helper = _FakeHelper()
    w._proj_overrides = {}
    w._tick_projectiles()
    assert w.helper.sent == []


def test_a_tick_sends_the_stored_overrides(gui_window):
    w = gui_window()
    w.helper = _FakeHelper()
    w._proj_overrides = {837: {"tileCollide": 0}}
    w._tick_projectiles()
    assert w.helper.sent and w.helper.sent[0][0] == "projectile-tick"
    assert "837:tileCollide=0" in w.helper.sent[0]


def test_overrides_are_saved_on_close_and_restored_next_time(gui_window, tmp_path,
                                                             monkeypatch):
    """The point of storing them: a player sets this up once, not every session."""
    from terrariabonker.gui import uistate

    monkeypatch.setattr(uistate, "_PATH", str(tmp_path / "window.json"))

    w = gui_window()
    w.helper = _FakeHelper()                    # the real one aborts if stopped twice
    w._proj_overrides = {837: {"tileCollide": 0, "timeLeft": 4200}}
    w.close()                                   # the real path, not closeEvent by hand
    assert uistate.load_projectiles() == {837: {"tileCollide": 0, "timeLeft": 4200}}

    fresh = gui_window()
    fresh._proj_overrides = {}
    fresh._restore_effects()
    assert fresh._proj_overrides == {837: {"tileCollide": 0, "timeLeft": 4200}}
