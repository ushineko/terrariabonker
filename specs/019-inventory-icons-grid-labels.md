# Spec 019: Item icons in the inventory grid; truncated recipe-grid labels

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: No issue tracker ticket (personal utility). A small GUI follow-up to spec 018,
> reusing the icon cache it introduced. No game-memory access and no new dependencies.

## Context

Spec 018 added the item-sprite icon cache and used it in the new recipe browser. Two
follow-ups: (1) show those icons in the **Inventory** grid too (it showed only abbreviated
names), and (2) the recipe grid's labels overflowed their cells — shrink the font and
truncate like the inventory does, keeping the full name on hover/click.

## Requirements

1. Inventory cells render the item **sprite** (from the shared icon cache) with the stack
   count in the corner (Terraria-style); the rarity tint stays as the cell border; the full
   name and details remain on the tooltip. Cells with no sprite fall back to the previous
   abbreviated-name text.
2. Recipe-grid labels are **truncated** with `invgrid.abbrev` at a smaller font so they fit
   the cell; the full name stays in the tooltip and the click popup, and the live filter
   still matches on the full name (not the abbreviation).

### Technical

3. The icon cache (`_icon_cache`, `_pixmap_cache`, `_placeholder`) is hoisted to
   `MainWindow.__init__` so both tabs share it regardless of build order. `_pixmap_for`
   returns the raw cached sprite; `_cell_icon(item_id, stack)` composites the sprite +
   stack (outlined text, bottom-right) via `QPainter` into a cell-sized `QIcon`.
4. `_render_cell` sets the composited icon (empty → clears icon; no sprite → abbrev text).
   `_make_cell` sets the button `iconSize`.
5. The recipe `QListView` gets an 8pt font; grid items display `invgrid.abbrev(name)` while
   `ROLE_SEARCH` keeps the full `"name #id"` for filtering.

## Risks & Assumptions

- **Icon availability.** If the sprite cache isn't built yet, inventory cells fall back to
  text (no crash); once icons are extracted (recipe tab first-run), a refresh shows them.
- **Compositing cost.** One small `QPainter` pass per filled cell on refresh (≤ ~50 cells)
  — negligible.
- **Rollback.** `git revert`. GUI-only; no memory, no files written, no new deps.

## Acceptance Criteria

- [x] Inventory cells show the item sprite with the stack count in the corner and the
      rarity-tinted border; tooltip keeps the full details; no-sprite items fall back to
      abbreviated text; empty cells clear cleanly
- [x] Recipe-grid labels are truncated at a smaller font and no longer clip; full name on
      hover and in the popup; live filter still matches full names/ItemID
- [x] Tests pass headless (101); flake8 clean on changed file; README + version (0.15.0,
      user-approved) updated; offscreen render verified both tabs

## Executive Summary

Inventory cells now render the cached item sprites with the stack count composited into the
corner (rarity tint kept as the border, full details on hover), and the recipe grid's labels
are truncated with `invgrid.abbrev` at 8pt so they fit — full name on hover/click, filter
still on the full name. GUI-only, reusing spec 018's icon cache; no memory access, no new
deps. Reviewers: `_cell_icon`/`_render_cell` and the recipe-grid label/font change.

## Testing

Existing suite stays green (101). Offscreen renders confirmed: inventory cells show
pickaxe / torch ×826 / wood ×1553 / stone ×50 with rarity borders, and the recipe grid
labels ("Ada.Pic", "Act.Rod") fit without clipping.
