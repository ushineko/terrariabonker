"""The panel's remembered window size (gui/uistate.py).

Size is the only geometry the app can honestly own: under KWin/Wayland Qt's position API
is a no-op that reports success, so placement is left to a KWin rule instead.
"""

import json

from terrariabonker.gui import uistate


def test_roundtrip(tmp_path, monkeypatch):
    monkeypatch.setattr(uistate, "_PATH", str(tmp_path / "window.json"))
    uistate.save_size(1200, 900)
    assert uistate.load_size() == (1200, 900)


def test_unset_returns_none(tmp_path, monkeypatch):
    monkeypatch.setattr(uistate, "_PATH", str(tmp_path / "nope.json"))
    assert uistate.load_size() is None


def test_corrupt_file_is_ignored(tmp_path, monkeypatch):
    p = tmp_path / "window.json"
    p.write_text("{not json")
    monkeypatch.setattr(uistate, "_PATH", str(p))
    assert uistate.load_size() is None


def test_absurd_sizes_are_rejected(tmp_path, monkeypatch):
    """A stale size from an unplugged monitor must not open a 30000px window."""
    p = tmp_path / "window.json"
    monkeypatch.setattr(uistate, "_PATH", str(p))
    for w, h in ((30000, 900), (10, 10), (-5, 400)):
        p.write_text(json.dumps({"w": w, "h": h}))
        assert uistate.load_size() is None, (w, h)


def test_saving_never_raises_even_when_unwritable(tmp_path, monkeypatch):
    """Closing the window must not fail because a settings write did."""
    monkeypatch.setattr(uistate, "_PATH", "/proc/definitely/not/writable/window.json")
    uistate.save_size(800, 600)          # must not raise


def test_state_lives_under_cache_not_the_root_owned_config_dir():
    """The config dir can be root-owned from sudo memory commands; this file is written
    by the unprivileged GUI, so it follows the sprite cache into ~/.cache."""
    assert "/.cache/" in uistate._PATH and "/.config/" not in uistate._PATH
