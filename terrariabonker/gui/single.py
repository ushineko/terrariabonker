"""One control panel at a time.

Two panels would mean two privileged ``serve`` workers, two 1 Hz inventory syncs and two
auto-restore loops racing on one game's patch state. ``patches.json`` is flock'd so it
cannot corrupt, but the instances still fight: one toggles a cheat, the other's next
status refresh flips the checkbox back.

The lock lives under ``XDG_RUNTIME_DIR`` rather than the config directory, because the
config directory is created by the CLI under sudo and is root-owned — the unprivileged GUI
cannot write there. The kernel releases an ``flock`` when the holder dies, so a crash or a
SIGKILL cannot leave a stale lock behind.
"""

from __future__ import annotations

import fcntl
import os
import tempfile

_HELD = None            # the open file object; the lock lasts as long as it is alive


def lock_path() -> str:
    runtime = os.environ.get("XDG_RUNTIME_DIR") or tempfile.gettempdir()
    return os.path.join(runtime, f"terrariabonker-gui-{os.getuid()}.lock")


def acquire(path: str | None = None) -> tuple[bool, str]:
    """Take the single-instance lock.

    Returns ``(True, "")`` when this process now owns it, or ``(False, pid)`` naming the
    instance that already does (``pid`` may be empty if the holder never recorded one).
    """
    global _HELD
    fh = open(path or lock_path(), "a+")
    try:
        fcntl.flock(fh.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        fh.seek(0)
        other = fh.read().strip()
        fh.close()
        return False, other
    fh.seek(0)
    fh.truncate()
    fh.write(str(os.getpid()))
    fh.flush()
    _HELD = fh                      # keep the fd open: closing it drops the lock
    return True, ""
