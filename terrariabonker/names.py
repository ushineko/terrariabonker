"""Terraria ItemID <-> display-name lookup for the inventory browser.

Backed by ``data/items.json`` (id -> name), generated from a Terraria ``ItemID.cs``.
The bundled map is 1.4.4.1 (5452 items, Count 5453), which covers the running
1.4.5.7 build; only a handful of 1.4.5-only ids (above ~5453) may be missing and
fall back to ``#<id>`` in the UI. Regenerate ``items.json`` from a matching
``ItemID.cs`` if a future update adds items.
"""

from __future__ import annotations

import json
import os

_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "data", "items.json")

try:
    with open(_PATH) as _f:
        _NAMES: dict[int, str] = {int(k): v for k, v in json.load(_f).items()}
except (OSError, ValueError):
    _NAMES = {}


def name(item_id: int) -> str:
    """Display name for an ItemID, or '' if unknown (e.g. a 1.4-only item)."""
    return _NAMES.get(item_id, "")


def label(item_id: int) -> str:
    """A never-empty label: 'Copper Shortsword' or '#5400' for unknown ids."""
    return _NAMES.get(item_id) or (f"#{item_id}" if item_id else "(empty)")


def search(query: str, limit: int = 60) -> list[tuple[int, str]]:
    """Items whose name contains ``query`` (case-insensitive), id-sorted."""
    q = query.strip().lower()
    if not q:
        return []
    hits = [(i, n) for i, n in _NAMES.items() if q in n.lower()]
    hits.sort(key=lambda t: (len(t[1]), t[0]))   # shorter names first = closer matches
    return hits[:limit]


def count() -> int:
    return len(_NAMES)
