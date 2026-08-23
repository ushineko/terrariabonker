"""The worker transport's fallback contract (spec 029).

The GUI must keep working when the worker is not up: `request` has to say so rather
than swallow the call, because that is what makes the caller spawn a one-shot CLI
instead. Nothing here starts a process.
"""

import pytest

pytest.importorskip("PyQt6.QtCore")

from terrariabonker.gui.helper import Helper          # noqa: E402


def _helper():
    return Helper(None, "sudo", ["-n", "true"])


def test_request_refuses_when_the_worker_is_not_up():
    h = _helper()
    assert h.available is False
    assert h.request(["status", "--json"], lambda _out: None) is False


def test_death_answers_every_pending_request_so_callers_are_not_stranded():
    """A dropped callback would leave the sync's in-flight flag stuck forever."""
    h = _helper()
    h.available = True
    got = []
    h._pending = {1: lambda out: got.append(out), 2: lambda out: got.append(out)}
    h._on_finished()
    assert len(got) == 2 and all("[ERROR]" in g for g in got)
    assert h.available is False and h._pending == {}


def test_replies_are_dispatched_by_id_and_ignore_foreign_lines():
    h = _helper()
    seen = {}
    h._pending = {5: lambda out: seen.setdefault(5, out)}
    h._dispatch('{"id": 5, "ok": true, "out": "hello"}')
    h._dispatch('not json')                     # must not raise
    h._dispatch('{"id": 99, "ok": true, "out": "nobody waiting"}')
    assert seen == {5: "hello"} and h._pending == {}


def test_no_threads_in_the_gui_transport():
    """Spec 029 requires the Qt event loop, not threads. Checks real use, not prose --
    main_window's module docstring mentions QThread precisely to say it is not used."""
    import inspect
    from terrariabonker.gui import helper, main_window
    for mod in (helper, main_window):
        src = inspect.getsource(mod)
        assert "import threading" not in src, mod.__name__
        assert "QThread(" not in src, mod.__name__
        assert "QRunnable" not in src and "QThreadPool" not in src, mod.__name__
