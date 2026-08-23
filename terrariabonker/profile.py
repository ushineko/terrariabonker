"""Cross-session desired-config profile: which code-patch cheats are on (+ their values)
and per-slot item edits, persisted **independent of game pid** so they can be re-applied to
a fresh game (auto-restore).

This is distinct from ``patcher``'s per-pid live state (which is correctly discarded when
the game restarts): the profile is what the user *wants*, and survives restarts. It is
updated by the common layer on every mutating action, and read by the ``restore`` operation.
Writes are atomic and lock-serialized (same pattern as the patch state) so concurrent CLI
invocations can't clobber it.
"""

from __future__ import annotations

import fcntl
import json
import os
from contextlib import contextmanager

_PATH = os.path.expanduser("~/.config/terrariabonker/profile.json")


@contextmanager
def _locked():
    os.makedirs(os.path.dirname(_PATH), exist_ok=True)
    lock = open(_PATH + ".lock", "a+")
    try:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
        yield
    finally:
        fcntl.flock(lock.fileno(), fcntl.LOCK_UN)
        lock.close()


def load() -> dict:
    try:
        with open(_PATH) as f:
            d = json.load(f)
    except (OSError, ValueError):
        d = {}
    d.setdefault("cheats", {})     # name -> value (None for valueless)
    d.setdefault("items", {})      # slot(str) -> set-item kwargs ({"type": 0} == empty)
    return d


def _save(d: dict) -> None:
    os.makedirs(os.path.dirname(_PATH), exist_ok=True)
    tmp = _PATH + f".{os.getpid()}.tmp"
    with open(tmp, "w") as f:
        json.dump(d, f)
    os.replace(tmp, _PATH)


def set_cheat(name: str, on: bool, value=None) -> None:
    """Record (or clear) a desired cheat and its value."""
    with _locked():
        d = load()
        if on:
            d["cheats"][name] = value
        else:
            d["cheats"].pop(name, None)
        _save(d)


def set_item(slot: int, kwargs: dict) -> None:
    """Record a per-slot item edit (the full set-item kwargs, incl. ``type``)."""
    with _locked():
        d = load()
        d["items"][str(slot)] = dict(kwargs)
        _save(d)


def clear_item(slot: int) -> None:
    """Record that a slot was emptied."""
    with _locked():
        d = load()
        d["items"][str(slot)] = {"type": 0}
        _save(d)


def cheats() -> dict:
    return load().get("cheats", {})


def items() -> dict:
    return load().get("items", {})
