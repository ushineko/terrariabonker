"""Only one control panel at a time (see gui/single.py).

Two panels means two privileged workers and two auto-restore loops racing on one game's
patch state, so the second one must decline to start.
"""

import os

from terrariabonker.gui import single


def test_second_acquire_is_refused_and_names_the_holder(tmp_path):
    path = str(tmp_path / "gui.lock")
    ok, _ = single.acquire(path)
    assert ok is True
    ok2, other = single.acquire(path)
    assert ok2 is False
    assert other == str(os.getpid()), "the refusal should name the instance holding it"


def test_lock_is_released_when_the_holder_lets_go(tmp_path):
    """The kernel drops an flock when the fd closes, so a crash cannot wedge it."""
    path = str(tmp_path / "gui.lock")
    assert single.acquire(path)[0] is True
    single._HELD.close()                     # simulate the process going away
    single._HELD = None
    assert single.acquire(path)[0] is True


def test_lock_lives_outside_the_root_owned_config_dir(monkeypatch):
    """The config dir is created by the CLI under sudo; the unprivileged GUI can't
    write there, so the lock belongs in the per-user runtime dir."""
    monkeypatch.setenv("XDG_RUNTIME_DIR", "/run/user/1234")
    assert single.lock_path().startswith("/run/user/1234/")
    monkeypatch.delenv("XDG_RUNTIME_DIR")
    assert single.lock_path().startswith("/tmp/") or "/tmp" in single.lock_path()
