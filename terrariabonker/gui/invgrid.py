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


# Terraria's canonical item-rarity name colours (the tooltip-name colour).
RARITY_RGB: dict[int, tuple[int, int, int]] = {
    -13: (255, 60, 60),   # Master
    -12: (200, 160, 255),  # Expert (rainbow in-game; static approximation)
    -11: (255, 175, 0),   # Quest / Amber
    -1: (130, 130, 130),  # Gray (junk)
    0: (200, 200, 200),   # White (common)
    1: (150, 150, 255),   # Blue
    2: (150, 255, 150),   # Green
    3: (255, 200, 150),   # Orange
    4: (255, 150, 150),   # Light red
    5: (255, 150, 255),   # Pink
    6: (210, 160, 255),   # Light purple
    7: (150, 255, 10),    # Lime
    8: (255, 255, 10),    # Yellow
    9: (5, 200, 255),     # Cyan
    10: (255, 40, 100),   # Red
    11: (180, 40, 255),   # Purple
}
_DEFAULT_RGB = (200, 200, 200)

# Terraria's canonical rarity-tier names (the ItemRarityID constants).
RARITY_NAME: dict[int, str] = {
    -13: "Master", -12: "Expert", -11: "Quest", -1: "Gray",
    0: "White", 1: "Blue", 2: "Green", 3: "Orange", 4: "Light Red",
    5: "Pink", 6: "Light Purple", 7: "Lime", 8: "Yellow", 9: "Cyan",
    10: "Red", 11: "Purple",
}


def rarity_rgb(rare: int) -> tuple[int, int, int]:
    """Canonical bright colour for a rarity tier."""
    return RARITY_RGB.get(rare, _DEFAULT_RGB)


def rarity_name(rare: int) -> str:
    """Canonical name for a rarity tier, or the bare number if unknown."""
    return RARITY_NAME.get(rare, str(rare))


def cell_colors(rare: int) -> tuple[tuple[int, int, int], tuple[int, int, int]]:
    """(background, border) RGB for a slot tinted by rarity. The background is a
    dark tint so light cell text stays readable; the border is brighter."""
    r, g, b = rarity_rgb(rare)
    bg = (r * 20 // 100 + 20, g * 20 // 100 + 20, b * 20 // 100 + 20)
    border = (r * 55 // 100 + 45, g * 55 // 100 + 45, b * 55 // 100 + 45)
    return bg, border


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
    if "rare" in row:
        lines.append(f"Rarity: {rarity_name(row['rare'])} ({row['rare']})")
    lines.append("Auto-reuse " + ("on" if row.get("auto_reuse") else "off"))
    lines.append(f"Use time {row.get('use_time')}")
    return "\n".join(lines)
