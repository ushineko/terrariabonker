# Spec 006: Grid inventory with a per-item edit dialog

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

The inventory tab is a sortable table with a few inline-editable columns. A grid
that mirrors Terraria's own inventory layout is more legible (spatial position
matches the game) and gives room to edit every mapped item property through a
dialog rather than cramming editors into table cells. This replaces the table.

Design decisions (confirmed with the user):

- **Text tiles**, not sprites: each cell shows an abbreviated item name and a stack
  badge, with a full-detail tooltip. No game assets are extracted or bundled.
- **Grid replaces the table** — it becomes the only inventory view.
- **Click a cell → a modal edit dialog** with all mapped properties (room to grow
  as more offsets are mapped).
- **Accessories in the inventory** must be editable like weapons/tools. Scope is the
  carried inventory (`Player.inventory[]`), not the equipped-accessory slots
  (`Player.armor[]`, out of scope this spec).

## Requirements

### Functional

1. The Inventory tab renders `Player.inventory[]` as a grid grouped like the game:
   Hotbar (slots 0–9, one row of 10), Inventory (10–49, 4×10), Coins (50–53), Ammo
   (54–57). Slot 58 (internal) is not shown.
2. Each cell shows an abbreviated name and, when stack > 1, a stack badge. Empty
   slots render as dim/empty. Hover shows a tooltip: full name, ID, slot, stack, and
   any of damage / pickaxe power / placement reach / auto-reuse that apply.
3. Clicking a **filled** cell opens a modal dialog prefilled with that item's values;
   editing and applying writes the changes. Clicking an **empty** cell opens the
   dialog in place mode with an item picker (name-autocomplete or ID) to place a new,
   fully-statted item into that exact slot.
4. The dialog edits: item type (by name or ID), stack, damage, auto-reuse, use-time,
   use-animation, pickaxe power, placement reach (tile boost). It can also **clear**
   the slot (set it empty).
5. An accessory sitting in the inventory opens the dialog and its stack/type and the
   generic fields apply (verified live). Fields that don't apply to it (e.g. pickaxe
   power) are still shown and editable but have no in-game effect.
6. A Refresh control re-reads the inventory into the grid.

### Technical

7. Type changes clone the pristine `ContentSamples` template (existing `set_item`
   behaviour) so a placed/changed item has real stats; same-type field edits do not
   re-template. Clearing a slot sets type 0 without templating.
8. All edits go through the existing service/CLI contract: the GUI issues
   `set-item`/`set-stack` via `gui/client.py` (`--json` reads), keeping CLI/GUI
   parity. `client.set_item_argv` is extended to cover use-anim, pick and tile-boost
   (already supported by the CLI). `ItemSlot`/`inventory --json` gains `use_anim` so
   the dialog can prefill it.
9. Grid/label/tooltip/section logic that is Qt-free lives in `gui/invgrid.py` and is
   unit-tested; the parity test covers the extended `set_item_argv`.

## Risks & Assumptions

- **Prefix (modifier) and defense are not yet mapped**, so the dialog cannot edit an
  accessory's Warding/Menacing tier or defense this spec. Those need offset discovery
  (CE/`/proc`) and are the top follow-up. The dialog is structured to add fields later.
- **Whole-inventory writes.** Edits apply to every player copy (existing behaviour) so
  the live copy is always hit while paused. Unchanged by this spec.
- **Rollback.** GUI-layer change plus two additive data fields and one CLI-client
  extension; `git revert`. No memory offsets change.

## Acceptance Criteria

- [x] Inventory tab is a grid grouped Hotbar / Inventory / Coins / Ammo; slot 58 hidden
- [x] Cells show abbreviated name + stack badge; empty cells are visibly empty; tooltip shows full detail
- [x] Clicking a filled cell opens a prefilled modal dialog; Apply writes the edits
- [x] Clicking an empty cell opens the dialog with an item picker; placing puts a statted item in that slot
- [x] Dialog edits type/stack/damage/auto/use-time/use-anim/pick/tile-boost and can clear the slot
- [x] An accessory in the inventory opens the dialog and edits apply (live-verified: placed Hermes Boots #54 into slot 26 with template stats, edited use_time/tile_boost, cleared the slot)
- [x] `client.set_item_argv` covers use-anim/pick/tile-boost; `inventory --json` includes use_anim
- [x] The old table view and its cell-edit code are removed
- [x] `gui/invgrid.py` helpers unit-tested; parity test green; 66 tests pass headless; lint clean

## Executive Summary

Replaces the sortable inventory table with a grid that mirrors Terraria's own
layout (Hotbar / Inventory / Coins / Ammo). Each cell is a text tile (abbreviated
name + stack badge, full-detail tooltip); clicking a cell opens a modal dialog that
edits every mapped Item property, places a fully-statted item into an empty slot, or
clears a slot. A type change (including placement) lets the ContentSamples template
supply real stats; same-item edits send the full field set. Accessories carried in
the inventory edit through the identical path (live-verified). Reviewers: start at
`gui/main_window.py` (`_inventory_tab`, `_on_cell_clicked`, `_apply_item_edit`),
`gui/item_dialog.py`, and the Qt-free `gui/invgrid.py`.

## Testing

`tests/test_invgrid.py` covers the Qt-free helpers (slot→section, abbreviation,
stack badge, tooltip field selection incl. an accessory-like item). The parity test
covers the extended `set_item_argv` (use-anim/pick/tile-boost and the clear-slot
argv). Headless behavioural smoke: 58 cells build; grid fill renders labels/badges;
the dialog resolves a same-item edit to a full-field argv and a type change/placement
to a type+stack argv; clear sets the cleared flag. Live: placed Hermes Boots (#54, an
accessory) into an empty slot with real template stats, applied a same-type edit
(use_time 30, tile_boost 5), and cleared the slot — all through the CLI the GUI uses.
66 tests pass headless; lint clean.
