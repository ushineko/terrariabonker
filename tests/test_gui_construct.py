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
