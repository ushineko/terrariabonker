"""NPCID -> display-name lookup for the compendium.

Backed by ``data/npcs.json``, extracted from the game's own ``Terraria.exe`` by
``tools/extract_item_names --npcs``: the ``NPCID`` constant fields joined with the embedded
``en-US.NPCs.json`` localization (``NPCName`` plus ``SpecialNPCName``). Unlike items, NPC ids
go negative for some entries, so they are not filtered to positive values.
"""

from __future__ import annotations

import json
import os

_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "data", "npcs.json")

try:
    with open(_PATH) as _f:
        _NAMES: dict[int, str] = {int(k): v for k, v in json.load(_f).items()}
except (OSError, ValueError):
    _NAMES = {}


def name(npc_id: int) -> str:
    """Display name for an NPCID, or '' if unknown."""
    return _NAMES.get(npc_id, "")


def label(npc_id: int) -> str:
    """A never-empty label: 'Blue Slime' or '#123' for unknown ids."""
    return _NAMES.get(npc_id) or f"#{npc_id}"


def all_names() -> dict[int, str]:
    return dict(_NAMES)


def count() -> int:
    return len(_NAMES)
