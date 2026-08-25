"""Builds this machine has decided about (spec 036).

Separate from the verified sets in ``patcher`` on purpose. Those are the project's claim:
somebody derived the AOBs against that exact build and confirmed the cheats in play. What
lives here is weaker and local — "on this machine, on this build, the patterns still
matched, and I chose to carry on" — so it is recorded apart and presented differently.

Same shape as ``profile``: a small JSON file under the config directory, written
atomically under a lock, and disposable. Losing it only brings the dialog back.
"""

from __future__ import annotations

import fcntl
import json
import os

_PATH = os.path.expanduser("~/.config/terrariabonker/accepted-builds.json")

ACCEPTED = "accepted"        # every cheat resolved and the user accepted the build
DEGRADED = "degraded"        # some did not, and the user chose to carry on anyway


def _locked():
    os.makedirs(os.path.dirname(_PATH), exist_ok=True)
    lock = open(_PATH + ".lock", "a+")
    fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
    return lock


def load() -> dict:
    try:
        with open(_PATH) as f:
            blob = json.load(f)
        return blob if isinstance(blob, dict) else {}
    except (OSError, ValueError):
        return {}


def decision(build_key: str) -> dict | None:
    """What this machine already decided about a build, or None if it has not."""
    got = load().get(build_key)
    return got if isinstance(got, dict) else None


def remember(build_key: str, how: str, failed=(), runtime: str | None = None) -> None:
    """Record a decision so the dialog does not ask again for this build.

    ``failed`` is the cheats that did not resolve, kept so the panel can disable exactly
    those on a later launch without re-probing.

    ``runtime`` is the .NET runtime that was executing the game, e.g. ``wine-mono-11.2.0``.
    Recorded because the patches match code that runtime's JIT emitted: a decision made
    under one runtime says nothing about another, even for the same game build. Kept
    beside the key rather than folded into it, so a decision made before this was tracked
    stays valid and simply reports no runtime -- which is the truth about it.
    """
    lock = _locked()
    try:
        blob = load()
        entry = {"decision": how, "failed": sorted(failed)}
        if runtime:
            entry["runtime"] = runtime
        blob[build_key] = entry
        tmp = _PATH + f".{os.getpid()}.tmp"
        with open(tmp, "w") as f:
            json.dump(blob, f, indent=1, sort_keys=True)
        os.replace(tmp, _PATH)
    finally:
        fcntl.flock(lock.fileno(), fcntl.LOCK_UN)
        lock.close()


def forget(build_key: str) -> None:
    """Drop a decision, so the panel asks about that build again."""
    lock = _locked()
    try:
        blob = load()
        if blob.pop(build_key, None) is None:
            return
        tmp = _PATH + f".{os.getpid()}.tmp"
        with open(tmp, "w") as f:
            json.dump(blob, f, indent=1, sort_keys=True)
        os.replace(tmp, _PATH)
    finally:
        fcntl.flock(lock.fileno(), fcntl.LOCK_UN)
        lock.close()


def failed_cheats(build_key: str) -> set[str]:
    """Cheats recorded as not resolving on this build."""
    got = decision(build_key) or {}
    return set(got.get("failed") or ())
