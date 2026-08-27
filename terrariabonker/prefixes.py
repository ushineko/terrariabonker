"""Item prefixes (modifiers): ID -> readable name, per-item-class applicability, and a
good/bad quality for the inventory indicator.

Names come from ``data/prefixes.json`` (extracted from Terraria.exe by the same tool that
builds ``items.json``; see ``tools/extract_item_names --prefixes``). The category pools and
quality below are Terraria's own modifier categorisation (factual game data): which weapon
class a modifier can roll on, and whether it is beneficial or detrimental.

Applicability (Terraria's roll categories):
- accessories roll only accessory modifiers;
- weapons roll the universal set plus their damage-class set (melee also gets the size
  modifiers, and each class has its "best" top-tier: Legendary / Unreal / Mythical);
- summon weapons roll the universal set plus the summon set.
"""

from __future__ import annotations

import json
import os

_DATA = os.path.join(os.path.dirname(__file__), "data", "prefixes.json")
_STATS = os.path.join(os.path.dirname(__file__), "data", "prefix_stats.json")

_NAMES: dict[int, str] = {}
try:
    with open(_DATA) as _f:
        _NAMES = {int(k): v for k, v in json.load(_f).items()}
except (OSError, ValueError):
    _NAMES = {}

# What each modifier does to the item's own fields, extracted from the game's IL by
# tools/extract_prefix_stats.py. A modifier is not a display value: `Item.Prefix`
# multiplies these into the item and stores the results, which is why assigning the prefix
# byte alone gives the name and nothing else (spec 046).
_STAT_MULTIPLIERS: dict[int, dict[str, float]] = {}
try:
    with open(_STATS) as _f:
        _STAT_MULTIPLIERS = {int(k): v for k, v in json.load(_f).items()}
except (OSError, ValueError):
    _STAT_MULTIPLIERS = {}


def stat_multipliers(prefix_id: int) -> dict[str, float]:
    """What this modifier does to the item's fields. ``{}`` for one that does nothing.

    Empty is a real answer, not a gap: the accessory modifiers (Hard, Warding, Menacing
    and the rest of 62-80) change the *player* when the item is equipped -- the game reads
    the prefix byte in `Player.GrantPrefixBenefits` -- so they need nothing written into
    the item and already work.
    """
    return dict(_STAT_MULTIPLIERS.get(prefix_id, {}))


#: Which of the above are added to the field rather than multiplied into it.
ADDITIVE_STATS = frozenset({"crit", "tagdamage", "armorpen"})


# Modifiers shared by every weapon (melee / ranged / magic / summon).
_UNIVERSAL = set(range(36, 62))
# Damage-class-specific pools (added to the universal set for that class).
_CLASS_POOL: dict[str, set[int]] = {
    "melee": set(range(1, 16)) | {81},     # size modifiers + Legendary
    "ranged": set(range(16, 26)) | {82},   # ranged modifiers + Unreal
    "magic": set(range(26, 36)) | {83},    # magic modifiers + Mythical
    "summon": set(range(84, 98)),          # summon / whip modifiers
    "accessory": set(range(62, 81)),       # accessory-only modifiers
}

# Detrimental (red) and neutral (gray) modifiers; every other real modifier is beneficial.
_BAD = {7, 8, 9, 10, 11, 13, 22, 23, 24, 29, 30, 31, 39, 40, 41, 47, 48, 49, 50, 55, 56,
        91, 92, 93, 94, 97}
_NEUTRAL = {12, 14, 15}

CLASS_FLAGS = ("melee", "ranged", "magic", "summon", "accessory")


def name(prefix_id: int) -> str:
    """Readable modifier name, or "" for 0/none/unknown."""
    return _NAMES.get(int(prefix_id), "") if prefix_id else ""


def quality(prefix_id: int) -> str:
    """"good" | "bad" | "neutral" | "none" — for the inventory indicator dot."""
    p = int(prefix_id)
    if not p:
        return "none"
    if p in _BAD:
        return "bad"
    if p in _NEUTRAL:
        return "neutral"
    return "good"


def valid_prefixes(flags: dict) -> list[int]:
    """Prefix IDs that can apply to an item with the given class flags (a dict with any of
    ``melee/ranged/magic/summon/accessory`` truthy). Accessories get the accessory pool;
    weapons get the universal set plus each set class; a non-weapon/non-accessory item gets
    none. Returned sorted by name for the dropdown."""
    if flags.get("accessory"):
        pool = set(_CLASS_POOL["accessory"])
    else:
        pool = set()
        for cls in ("melee", "ranged", "magic", "summon"):
            if flags.get(cls):
                pool |= _UNIVERSAL | _CLASS_POOL[cls]
    return sorted(pool, key=lambda i: _NAMES.get(i, "").lower())


def has_categories(flags: dict) -> bool:
    """True if the item can take a prefix at all (a weapon or an accessory)."""
    return any(flags.get(c) for c in CLASS_FLAGS)


def all_ids() -> list[int]:
    """Every prefix ID, sorted by name (for the place-a-new-item fallback)."""
    return sorted(_NAMES, key=lambda i: _NAMES[i].lower())
