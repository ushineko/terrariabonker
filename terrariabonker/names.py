"""Terraria ItemID <-> display-name lookup for the inventory browser.

Backed by ``data/items.json`` (id -> name), extracted directly from the game's own
``Terraria.exe`` (build 1.4.5.7: 6195 items) by ``tools/extract_item_names`` — it
joins the ``ItemID`` constant fields (internal-name -> id) with the embedded
``en-US.Items.json`` localization resource (internal-name -> display name). This is
authoritative and complete for the exact build, and was the only way to name items
on a release so new that no community ``ItemID.cs`` existed yet. Regenerate with the
tool after a game update; unknown ids fall back to ``#<id>`` in the UI.
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
