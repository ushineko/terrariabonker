"""The panel must actually build.

The import smoke test catches a bad import line, but not a widget wired up before the
widget it depends on exists — which is how `self.log.appendPlainText` passed to a tab
constructed earlier in _build() slipped through and stopped the GUI starting. This builds
the real MainWindow with the privileged paths stubbed out, so nothing is spawned.
"""

import os

import pytest

pytest.importorskip("PyQt6.QtWidgets")
os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")


@pytest.fixture
def app():
    from PyQt6.QtWidgets import QApplication
    inst = QApplication.instance() or QApplication([])
    yield inst


def test_main_window_builds_with_every_tab(app, monkeypatch):
    from terrariabonker.gui import main_window as mw

    # no sudo probe, no worker, no subprocesses: this test is about widget wiring
    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)

    w = mw.MainWindow()
    try:
        titles = [w.tabs.tabText(i) for i in range(w.tabs.count())]
        assert titles == ["Trainer", "Inventory", "Recipes", "Compendium"]
        assert w.log is not None, "the log widget the tabs log through must exist"
    finally:
        w.close()


def test_every_cheat_has_a_checkbox(app, monkeypatch):
    """The patch list is generated from the catalog, so a new cheat must appear."""
    from terrariabonker.gui import main_window as mw
    from terrariabonker.patcher import PATCH_CATALOG

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)

    w = mw.MainWindow()
    try:
        assert set(w._patch_cbs) == set(PATCH_CATALOG)
    finally:
        w.close()


def test_code_patches_are_split_into_section_tabs(app, monkeypatch):
    """One long list did not scale: tabs keep the Trainer tab a fixed height as patches
    are added."""
    from terrariabonker.gui import main_window as mw
    from terrariabonker.patcher import SECTIONS

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)

    w = mw.MainWindow()
    try:
        pages = [w.patch_pages.tabText(i) for i in range(w.patch_pages.count())]
        assert pages == [name for name, _members in SECTIONS]
    finally:
        w.close()


def test_extraction_reports_through_a_progress_bar(app, monkeypatch):
    """The status text used to go to the Recipes tab's label, which is invisible when the
    Compendium tab is what triggered the extraction."""
    from terrariabonker.gui import main_window as mw

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    captured = {}
    monkeypatch.setattr(
        mw.MainWindow, "_spawn_user",
        lambda self, argv, on_output=None, on_progress=None: captured.update(
            on_progress=on_progress, on_output=on_output))

    w = mw.MainWindow()
    try:
        _check_progress(w, captured)
    finally:
        w.close()


def _check_progress(w, captured):
    assert w.progress.isHidden(), "progress bar showing before anything runs"
    w._extract_sprites()

    assert not w.progress.isHidden(), "no progress bar while extracting"
    captured["on_progress"]("120/6892")
    assert w.progress.maximum() == 6892 and w.progress.value() == 120
    captured["on_progress"]("not a count")          # must not throw or reset
    assert w.progress.value() == 120
    captured["on_output"]("[OK] done")
    assert w.progress.isHidden(), "progress bar left on screen after extraction"


def _window(monkeypatch):
    from terrariabonker.gui import main_window as mw
    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)
    return mw, mw.MainWindow()


def test_auto_restore_retries_a_refusal_instead_of_giving_up(app, monkeypatch):
    """The refusal that actually happens is a startup race — the version is scanned out
    of live memory, and for a moment after launch the game's own string is not there yet.
    Giving up on the first error killed auto-restore for the whole session over a
    condition that clears itself in seconds."""
    mw, w = _window(monkeypatch)
    try:
        scheduled = []
        monkeypatch.setattr(mw.QTimer, "singleShot",
                            staticmethod(lambda ms, fn: scheduled.append(ms)))
        captured = {}
        monkeypatch.setattr(mw.MainWindow, "_call",
                            lambda self, argv, on_output=None: captured.update(
                                cb=on_output))
        refusal = ("[ERROR] Terraria 2.0.50727 differs from 1.4.5.7 in "
                   "major/minor/patch; the offsets are almost certainly wrong")

        w._restore_attempts = 0
        w._do_restore()
        captured["cb"](refusal)
        assert scheduled, "a refusal was not retried"

        # ...but not forever: a build that really is wrong must stop nagging.
        scheduled.clear()
        w._restore_attempts = mw.RESTORE_RETRIES
        w._do_restore()
        captured["cb"](refusal)
        assert not scheduled, "kept retrying past the limit"
        assert "auto-restore FAILED" in w.log.toPlainText()
    finally:
        w.close()


def test_the_vein_watcher_follows_the_extractor_checkbox(app, monkeypatch):
    """The ore extractor is the one cheat that is not just a patch. Enabling it puts a
    stub in the game, but something has to notice the player breaking an ore and hand the
    rest of the vein over — and that loop cannot run on the Qt thread. It is driven from a
    timer, so the timer has to follow the cheat.

    Switching it off must also tell the worker to drop its watcher, not merely stop the
    timer: a queue left armed is re-mined every frame.
    """
    from terrariabonker.gui import main_window as mw

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)
    w = mw.MainWindow()
    try:
        sent = []
        w.helper.available = True
        w.helper.request = lambda argv, cb: (sent.append(argv), True)[1]

        assert not w._vein_timer.isActive(), "the watcher must not run unasked"
        w._set_vein_watch(True)
        assert w._vein_timer.isActive(), "the cheat is on but nothing is watching"
        assert w._vein_timer.interval() <= 250, "too slow to catch the first tile"

        w._set_vein_watch(False)
        assert not w._vein_timer.isActive()
        assert any(a[0] == "extract-stop" for a in sent), \
            "switching off must disarm the queue, not just stop the timer"

        # a tick asks the worker for one slice, and only one at a time
        sent.clear()
        w._set_vein_watch(True)
        w._tick_veins()
        assert sent and sent[0][0] == "extract-tick"
        sent.clear()
        w._tick_veins()                       # the first is still in flight
        assert not sent, "overlapping ticks would pile up on the worker"
    finally:
        w.close()
