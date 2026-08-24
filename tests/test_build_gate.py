"""The unrecognised-build gate (spec 036).

Terraria updated 1.4.5.7 -> 1.4.5.8 underneath a running panel and the panel said nothing.
An AOB is derived against one exact build, so an update means one of three things — every
pattern still matches, some do, or none do — and the user could not tell which.
"""

import os

import pytest

pytest.importorskip("PyQt6.QtWidgets")
os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

from PyQt6.QtWidgets import QApplication                       # noqa: E402

from terrariabonker import builds                              # noqa: E402
from terrariabonker.gui import buildgate                       # noqa: E402

ALL_OK = {"build": "1.4.5.8+24893155", "recognised": False, "failed": [],
          "cheats": {n: {"resolved": True, "reason": ""}
                     for n in ("mining", "reach", "loot")}}
SOME_DEAD = {"build": "1.4.5.8+24893155", "recognised": False, "failed": ["loot"],
             "cheats": {"mining": {"resolved": True, "reason": ""},
                        "reach": {"resolved": True, "reason": ""},
                        "loot": {"resolved": False, "reason": "matched nothing"}}}


@pytest.fixture
def app():
    yield QApplication.instance() or QApplication([])


def test_all_clear_offers_one_click_accept(app):
    dlg = buildgate.BuildGateDialog(None, ALL_OK, "1.4.5.7+24825745")
    assert "Accept" in dlg.btn_ok.text()
    dlg.btn_ok.click()
    assert dlg.result_decision == buildgate.ACCEPT


def test_a_dead_cheat_is_named_and_continuing_is_the_default(app):
    dlg = buildgate.BuildGateDialog(None, SOME_DEAD, "1.4.5.7+24825745")
    assert "1" in dlg.btn_ok.text(), "the count of dead cheats belongs on the button"
    dlg.btn_ok.click()
    assert dlg.result_decision == buildgate.CONTINUE


def test_exit_is_always_offered(app):
    dlg = buildgate.BuildGateDialog(None, SOME_DEAD, "1.4.5.7+24825745")
    dlg.btn_exit.click()
    assert dlg.result_decision == buildgate.EXIT


def test_closing_the_window_is_not_consent(app):
    """Dismissing the dialog must not silently accept a build."""
    dlg = buildgate.BuildGateDialog(None, ALL_OK, "1.4.5.7+24825745")
    assert dlg.result_decision == buildgate.EXIT


def test_the_dialog_names_both_builds_and_the_dead_cheat(app):
    from PyQt6.QtWidgets import QLabel

    def words(check):
        dlg = buildgate.BuildGateDialog(None, check, "1.4.5.7+24825745")
        return "".join(lbl.text() for lbl in dlg.findChildren(QLabel))

    blob = words(ALL_OK)
    assert "1.4.5.8+24893155" in blob, "does not say what is running"
    assert "1.4.5.7+24825745" in blob, "does not say what it knows"

    blob = words(SOME_DEAD)
    assert "loot" in blob, "a dead cheat must be named, not merely counted"
    assert "matched nothing" in blob, "the reason belongs in front of the user"


# --- the record of what this machine decided --------------------------------

def test_a_decision_is_remembered_per_build(tmp_path, monkeypatch):
    monkeypatch.setattr(builds, "_PATH", str(tmp_path / "accepted-builds.json"))
    assert builds.decision("1.4.5.8+1") is None
    builds.remember("1.4.5.8+1", builds.ACCEPTED)
    assert builds.decision("1.4.5.8+1")["decision"] == builds.ACCEPTED
    assert builds.decision("1.4.5.9+2") is None, "a decision must not cover other builds"


def test_degraded_records_which_cheats_are_dead(tmp_path, monkeypatch):
    monkeypatch.setattr(builds, "_PATH", str(tmp_path / "accepted-builds.json"))
    builds.remember("1.4.5.8+1", builds.DEGRADED, ["loot", "teleport"])
    assert builds.failed_cheats("1.4.5.8+1") == {"loot", "teleport"}
    assert builds.failed_cheats("nope") == set()


def test_forgetting_brings_the_question_back(tmp_path, monkeypatch):
    monkeypatch.setattr(builds, "_PATH", str(tmp_path / "accepted-builds.json"))
    builds.remember("1.4.5.8+1", builds.ACCEPTED)
    builds.forget("1.4.5.8+1")
    assert builds.decision("1.4.5.8+1") is None


def test_a_corrupt_file_is_not_fatal(tmp_path, monkeypatch):
    path = tmp_path / "accepted-builds.json"
    path.write_text("{ not json")
    monkeypatch.setattr(builds, "_PATH", str(path))
    assert builds.load() == {}
    builds.remember("1.4.5.8+1", builds.ACCEPTED)          # must still be writable
    assert builds.decision("1.4.5.8+1") is not None


# --- the panel's side of it ---------------------------------------------------

def _window(monkeypatch):
    from terrariabonker.gui import main_window as mw
    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)
    calls = []
    monkeypatch.setattr(mw.MainWindow, "_call",
                        lambda self, argv, on_output=None: calls.append((argv, on_output)))
    return mw, mw.MainWindow(), calls


def test_an_unknown_build_is_only_asked_about_once(app, monkeypatch):
    """The gate keys on the build, not on startup, so it fires when the game restarts
    into an update — but it must not nag on every status refresh."""
    mw, w, calls = _window(monkeypatch)
    try:
        calls.clear()
        w._maybe_gate_build("1.4.5.8+24893155")
        checks = [c for c in calls if c[0][0] == "build-check"]
        assert len(checks) == 1

        # Let the check finish, so the in-flight guard is down and only the record of
        # having asked can stop a second one. Status ticks about once a second.
        checks[0][1]('{"build": "1.4.5.8+24893155", "recognised": true, '
                     '"failed": [], "cheats": {}}')
        assert not w._gate_open
        w._maybe_gate_build("1.4.5.8+24893155")
        assert len([c for c in calls if c[0][0] == "build-check"]) == 1, \
            "asked again about a build it had already checked"

        w._maybe_gate_build("1.4.5.9+99")                  # a different build must ask
        assert len([c for c in calls if c[0][0] == "build-check"]) == 2
    finally:
        w.close()


def test_a_recognised_build_asks_nothing(app, monkeypatch):
    mw, w, calls = _window(monkeypatch)
    try:
        shown = []
        monkeypatch.setattr(mw.buildgate, "BuildGateDialog",
                            lambda *a, **k: shown.append(1))
        w._apply_build_decision({"build": "x", "recognised": True, "failed": [],
                                 "cheats": {}})
        assert not shown
    finally:
        w.close()


def test_continuing_disables_exactly_the_dead_cheats(app, monkeypatch):
    mw, w, calls = _window(monkeypatch)
    try:
        class Dlg:
            result_decision = buildgate.CONTINUE

            def __init__(self, *a, **k):
                pass

            def exec(self):
                return 1

        monkeypatch.setattr(mw.buildgate, "BuildGateDialog", Dlg)
        monkeypatch.setattr(mw.MainWindow, "refresh_patches", lambda self: None)
        w._apply_build_decision(SOME_DEAD)
        assert w._unavailable == {"loot"}
    finally:
        w.close()


def test_an_unreadable_check_is_asked_again_rather_than_assumed_fine(app, monkeypatch):
    mw, w, calls = _window(monkeypatch)
    try:
        calls.clear()
        w._maybe_gate_build("1.4.5.8+24893155")
        cb = [c for c in calls if c[0][0] == "build-check"][0][1]
        cb("this is not json")
        assert "1.4.5.8+24893155" not in w._gated_builds, "swallowed a failed check"
    finally:
        w.close()


def test_the_gate_waits_for_a_player_to_be_in_world(app, monkeypatch):
    """Several cheats hook methods mono compiles lazily, so a scan at the main menu
    reports them unmatched. Saying a cheat is dead when it is merely not compiled yet is
    worse than saying nothing."""
    mw, w, calls = _window(monkeypatch)
    try:
        calls.clear()
        w._maybe_gate_build("1.4.5.8+24893155", {"pid": 1, "name": None})
        assert not [c for c in calls if c[0][0] == "build-check"], "judged at the menu"
        w._maybe_gate_build("1.4.5.8+24893155", {"pid": 1, "name": "player"})
        assert [c for c in calls if c[0][0] == "build-check"]
    finally:
        w.close()
