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

# The only fields worth saving: the ones Terraria regenerates from the item type on load.
# Type, stack and prefix are written into the save file by the game itself, so re-applying
# them achieves nothing — auto-restore used to record all three and then report failures
# about items whose only "edit" was a prefix that had survived perfectly well on its own.
RESTORABLE = ("damage", "auto_reuse", "use_time", "use_anim", "pick", "tile_boost",
              "defense")


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
    d.setdefault("cheats", {})       # name -> value (None for valueless)
    d.setdefault("item_edits", {})   # item type(str) -> {restorable field: value}
    d.setdefault("empty_slots", [])  # slots the user deliberately cleared
    return _migrate(d)


def _migrate(d: dict) -> dict:
    """Fold a slot-keyed profile into the type-keyed one (spec 038).

    Edits used to be stored per slot with every field the dialog submitted, which meant an
    item that moved lost its edit and an item whose only change was a prefix was reported
    as a failure on every launch. Only the regenerated fields are carried over; whether
    those differ from the item's own defaults needs the game's templates, so that pruning
    happens on the next restore rather than here.
    """
    legacy = d.pop("items", None)
    if not legacy:
        return d
    for slot_s, kw in legacy.items():
        itype = int(kw.get("type", 0) or 0)
        if not itype:
            slot = int(slot_s)
            if slot not in d["empty_slots"]:
                d["empty_slots"].append(slot)
            continue
        fields = {k: v for k, v in kw.items() if k in RESTORABLE and v is not None}
        if fields:
            d["item_edits"].setdefault(str(itype), {}).update(fields)
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


def set_item_edit(item_type: int, fields: dict) -> None:
    """Record an edit against the item *type*, not the slot it happens to be in.

    ``fields`` should already be narrowed to what differs from the item's defaults; an
    empty mapping removes the entry rather than storing something with nothing to restore.
    """
    fields = {k: v for k, v in (fields or {}).items() if k in RESTORABLE and v is not None}
    with _locked():
        d = load()
        if fields:
            d["item_edits"][str(int(item_type))] = fields
        else:
            d["item_edits"].pop(str(int(item_type)), None)
        _save(d)


def forget_item_edit(item_type: int) -> None:
    """Drop a saved edit — used when a restore finds nothing left that differs."""
    with _locked():
        d = load()
        if d["item_edits"].pop(str(int(item_type)), None) is not None:
            _save(d)


def clear_item(slot: int) -> None:
    """Record that a slot was emptied."""
    with _locked():
        d = load()
        if int(slot) not in d["empty_slots"]:
            d["empty_slots"].append(int(slot))
        _save(d)


def cheats() -> dict:
    return load().get("cheats", {})


def item_edits() -> dict:
    """``{item type (int): {field: value}}`` for every saved edit."""
    return {int(k): v for k, v in load().get("item_edits", {}).items()}


def empty_slots() -> list:
    return list(load().get("empty_slots", []))
