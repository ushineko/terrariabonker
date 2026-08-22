# Spec 012: Recipe browser

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

Vanilla Terraria has no recipe browser (you can't look up "how do I make X" or "what
uses X"). Unlike item names, recipes are **not** a static table in the exe — they're
built imperatively by `Recipe.SetupRecipes()`, so they can't be extracted offline. But
the fully-built list lives at runtime in `Main.recipe : Recipe[]`, which we can read
over `/proc` (reusing the mono-array-from-a-static primitive from the LocalPlayer fix).
Extract once, cache to JSON, browse offline — the same shape as `items.json`.

## Requirements

### Functional

1. Extract crafting recipes from the running game to `data/recipes.json`
   (`extract-recipes` CLI). Each recipe: output item + count, ingredient item ids +
   counts, and the crafting-station tile id (if any).
2. A GUI **Recipes** tab: search an item and list recipes that **Make** it or that
   **Use** it (ingredient), showing ingredient counts and the station name. Browses the
   cache offline; a **Re-extract from game** button regenerates it.
3. Station names are shown where known, else "Tile #N".

### Technical

4. `recipes.extract(mem)` walks `Main.recipe[]` via `locate.main_static_base` (Main.player
   static addr − 0xA7C) + `Main.recipe` @ +0xA68. Recipe offsets: `createItem` 0x8,
   `requiredItem` 0xC (Item[]), `requiredTile` 0x1C (scalar int, <0 = hand-crafted);
   Item type/stack at 0x6C/0x88. Station names are derived from each output item's
   `createTile` (0xA0) — an item whose createTile is a station tile *is* that station —
   which is exact and build-specific; a small supplement covers non-crafted stations
   (Demon Altar, Hellforge, …), else "Tile #N".
5. Browsing (`load`/`by_output`/`using`/`station_name`) reads the cached JSON in-process
   — no memory access, so the GUI browses without sudo; only re-extract shells to the
   CLI. `client.extract_recipes_argv` keeps CLI/GUI parity.

## Risks & Assumptions

- **Build-specific offsets.** Recipe/Main offsets are for 1.4.5.7; re-derive with
  `ce/poc_recipe.lua` and re-extract after an update.
- **Station names best-effort.** The createTile derivation is correct for the common
  stations (Work Bench, Anvil, Furnace, Sawmill, Mythril Anvil, Ancient Manipulator, …);
  a few furniture-tile collisions mislabel rarer stations. Ingredients are exact.
- **Rollback.** Additive: a new module, data file, CLI verb, and GUI tab; `git revert`.

## Acceptance Criteria

- [x] `extract-recipes` reads `Main.recipe[]` and writes `data/recipes.json` (~3,600 recipes, live-verified)
- [x] Recipe output + ingredient item ids/counts are exact (Torch = Gel+Wood; Iron Anvil = 5 Iron Bar @ Work Bench; …)
- [x] Station names derived from output items' createTile; unknowns show "Tile #N"
- [x] GUI Recipes tab: Makes/Uses search with ingredient counts + station; Re-extract button
- [x] Browsing reads the cache in-process (no sudo); parity test covers `extract-recipes`
- [x] Unit tests: by-output / uses / station-name against a fixture; 82 tests pass; lint clean

## Executive Summary

Adds a recipe browser — search what an item is made from or used in — sourced from the
running game's `Main.recipe[]` (recipes are code, not a static table, so there's nothing
to read offline). Extract once to `data/recipes.json`, browse offline in a new Recipes
tab. Station names are derived exactly from each output item's `createTile`. Reviewers:
`recipes.py`, `locate.main_static_base`, and `gui/main_window._recipes_tab`.

## Testing

`tests/test_recipes.py` covers by-output/uses queries and station naming against a
fixture. Live: `extract` read 3,603 recipes with exact outputs/ingredients (Torch,
Wooden Sword, Iron Anvil, colored torches, …) and correct common stations; the GUI tab
searches Makes/Uses and renders ingredient counts + station. 82 tests pass; lint clean.
