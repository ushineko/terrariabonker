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
    assert "/.cache/" in uistate._DEFAULT_PATH
    assert "/.config/" not in uistate._DEFAULT_PATH


# --- the Effects panel survives a restart (reported bug) ----------------------

def test_saving_the_size_keeps_the_effects(tmp_path, monkeypatch):
    """save_size used to dump {"w","h"} over the whole file.

    That is written on closeEvent -- the one moment the effects are guaranteed to be saved
    too -- so anything else stored here would have been dropped every single time.
    """
    from terrariabonker.gui import uistate

    monkeypatch.setattr(uistate, "_PATH", str(tmp_path / "window.json"))
    uistate.save_effects({"cb_fishing": True, "sp_bait": 30})
    uistate.save_size(900, 700)
    assert uistate.load_effects() == {"cb_fishing": True, "sp_bait": 30}
    assert uistate.load_size() == (900, 700)


def test_saving_the_effects_keeps_the_size(tmp_path, monkeypatch):
    from terrariabonker.gui import uistate

    monkeypatch.setattr(uistate, "_PATH", str(tmp_path / "window.json"))
    uistate.save_size(900, 700)
    uistate.save_effects({"cb_potions": True})
    assert uistate.load_size() == (900, 700)
    assert uistate.load_effects() == {"cb_potions": True}


def test_effects_from_a_missing_or_junk_file_are_empty(tmp_path, monkeypatch):
    """Never a crash on a corrupt cache: an unreadable panel state is no panel state."""
    from terrariabonker.gui import uistate

    p = tmp_path / "window.json"
    monkeypatch.setattr(uistate, "_PATH", str(p))
    assert uistate.load_effects() == {}
    p.write_text("not json at all")
    assert uistate.load_effects() == {}
    p.write_text('{"effects": "a string"}')
    assert uistate.load_effects() == {}


def test_only_switches_and_numbers_are_stored(tmp_path, monkeypatch):
    """The state is read back into widgets, so a stray object would be restored into one."""
    from terrariabonker.gui import uistate

    monkeypatch.setattr(uistate, "_PATH", str(tmp_path / "window.json"))
    uistate.save_effects({"cb_fishing": True, "sp_bait": 30, "junk": {"a": 1},
                          "also_junk": None})
    assert uistate.load_effects() == {"cb_fishing": True, "sp_bait": 30}
