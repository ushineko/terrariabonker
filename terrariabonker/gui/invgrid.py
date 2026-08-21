"""Qt-free helpers for the grid inventory view.

The grid mirrors Terraria's own inventory layout. Keeping the layout/label/tooltip
logic here (no PyQt import) means it can be unit-tested without a display. The GUI
layer (``main_window``) resolves item names and builds the widgets; this module only
maps slots to sections and formats the text a cell shows.
"""

from __future__ import annotations

# (title, slots, columns) — slot 58 is internal and intentionally omitted.
SECTIONS: list[tuple[str, range, int]] = [
    ("Hotbar", range(0, 10), 10),
    ("Inventory", range(10, 50), 10),
    ("Coins", range(50, 54), 4),
    ("Ammo", range(54, 58), 4),
]

# Every slot the grid renders, in display order.
GRID_SLOTS: list[int] = [i for _t, rng, _c in SECTIONS for i in rng]


def section_of(index: int) -> str | None:
    """The section title a slot belongs to, or None if it is not shown."""
    for title, rng, _cols in SECTIONS:
        if index in rng:
            return title
    return None


def abbrev(name: str, width: int = 8) -> str:
    """A short cell label for an item name; the tooltip carries the full name.

    Short names pass through. Multi-word names collapse to dot-joined prefixes
    ("Copper Pickaxe" -> "Cop.Pick"); a long single word is truncated with an
    ellipsis.
    """
    name = name.strip()
    if len(name) <= width:
        return name
    words = name.split()
    if len(words) >= 2:
        take = max(2, (width - (len(words) - 1)) // len(words))
        short = ".".join(w[:take] for w in words)
        if len(short) <= width + 3:
            return short
    return name[: width - 1] + "…"


def stack_badge(stack: int) -> str:
    """Text for the stack badge: shown only when more than one."""
    return str(stack) if stack and stack > 1 else ""


def is_empty(row: dict) -> bool:
    return not row or row.get("type", 0) == 0


def tooltip(row: dict, name: str) -> str:
    """Full-detail hover text for a cell."""
    slot = row.get("slot")
    if is_empty(row):
        return f"Slot {slot} — empty\n(click to place an item)"
    lines = [f"{name}  (#{row.get('type')})", f"Slot {slot}",
             f"Stack {row.get('stack')}"]
    if row.get("damage", -1) >= 0:
        lines.append(f"Damage {row['damage']}")
    if row.get("pick", 0) > 0:
        lines.append(f"Pickaxe power {row['pick']}%")
    if row.get("tile_boost", 0) > 0:
        lines.append(f"Placement reach +{row['tile_boost']}")
    lines.append("Auto-reuse " + ("on" if row.get("auto_reuse") else "off"))
    lines.append(f"Use time {row.get('use_time')}")
    return "\n".join(lines)
