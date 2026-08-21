# Spec 007: Rarity color tinting for inventory slots

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

The grid inventory (spec 006) renders slots as neutral dark tiles. Terraria's own
UI colours an item's tooltip name by its **rarity** tier, a per-item property. Tinting
each slot by that rarity makes the grid read at a glance and matches the game's visual
language. This required mapping one new offset, `Item.rare`.

## Requirements

### Functional

1. Each filled slot is tinted by the item's rarity: a dark rarity-hued background and
   a brighter rarity-hued border, with light text kept readable. Empty slots stay
   neutral. The tooltip gains a "Rarity N" line.
2. The rarity→colour map follows Terraria's canonical rarity name colours (gray −1,
   white 0, blue 1, green 2, orange 3, light-red 4, pink 5, light-purple 6, lime 7,
   yellow 8, cyan 9, red 10, purple 11; amber/expert/master specials approximated).

### Technical

3. `Item.rare` is at offset **0xF8** (int), discovered by diffing ContentSamples
   templates of items with known rarities and validated across the spectrum (Old Shoe
   −1, commons 0, Aglet/Shackle 1, Muramasa 2, Excalibur 5, Terra Blade 8, Meowmere/
   Zenith 10). Read into `Slot.rare` / `ItemSlot.rare`; `inventory --json` carries it.
4. Colour logic (rarity→rgb, and the dark-bg/bright-border derivation) lives in the
   Qt-free `gui/invgrid.py` and is unit-tested; the GUI only applies it as a stylesheet.

## Risks & Assumptions

- **Build-specific offset.** 0xF8 is for 1.4.5.7; re-derive after an update (the
  discovery method is recorded in `docs/discovery.md`). A wrong offset only mis-colours
  a tile — it never affects a write.
- **Expert/Master rarities** (−12/−13) use rotating/rainbow colours in-game; these are
  approximated with a static colour. Cosmetic only.
- **Rollback.** Additive: one offset + one data field + colour helpers; `git revert`.

## Acceptance Criteria

- [x] `Item.rare` offset (0xF8) read into `Slot`/`ItemSlot`; `inventory --json` includes `rare`
- [x] Filled slots tinted by rarity (dark bg + bright border); empty slots neutral; text readable
- [x] Tooltip shows the rarity tier
- [x] Rarity→colour map + cell-colour derivation in `gui/invgrid.py`, unit-tested
- [x] Live-verified: inventory reads correct rarities (Slime Whip 2, Fallen Star/Shackle 1, commons 0)
- [x] 70 tests pass headless; lint clean

## Executive Summary

Tints each inventory slot by Terraria's item-rarity colour, matching the game's own
tooltip-name colours, so the grid reads at a glance. Adds the `Item.rare` offset
(0xF8, discovered by template diff and validated across the rarity spectrum) to the
slot data and a Qt-free rarity→colour map in `gui/invgrid.py`. Reviewers: start at
`invgrid.RARITY_RGB`/`cell_colors` and `main_window._render_cell`.

## Testing

`tests/test_invgrid.py` covers the rarity colour map (known tiers distinct, fallback,
dark-bg/bright-border invariant) and the rarity tooltip line. Headless render smoke:
different rarities produce different cell stylesheets; empty cells stay neutral. Live:
`inventory --json` reports correct `rare` values against the running game. 70 tests
pass headless; lint clean.
