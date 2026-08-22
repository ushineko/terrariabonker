# Spec 014: Real crafting-station names in the recipe browser

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

The recipe browser (spec 012) derived station names from each output item's
`createTile`, which was ~70% right and mislabeled several rarer stations (e.g. tile
228 showed "golf balls" instead of "Dye Vat"). Terraria's authoritative tile display
name — what the map and crafting UI show — is
`Lang._mapLegendCache[ MapHelper.tileLookup[tileType] ]._value`. This spec extracts
those real names during recipe extraction.

## Requirements

1. Station names in `data/recipes.json` are Terraria's own tile display names, exact
   for every station tile that appears in a recipe (no more collisions / "Tile #N" for
   real stations).

### Technical

2. `recipes._tile_names(mem, tiles)` resolves two class statics by AOB (operand of a
   `mov eax,[abs]`): `MapHelper.tileLookup` (ushort[]: tileType -> map-legend index),
   from `TileToLookup`'s `lea eax,[eax+esi*2+10]; movzx eax,word[eax]; add eax,edi`; and
   `Lang._mapLegendCache` (LocalizedText[]), from `GetMapObjectName`'s double-read +
   index. For each tile: `name = _mapLegendCache[tileLookup[tile]]._value` (String at
   LocalizedText+0xC). Fails safe to `{}` (→ "Tile #N") if the AOBs are missing.
3. Replaces the `createTile` derivation. `station_name` reads the cached map, else
   "Tile #N" (the hardcoded supplement is removed — no longer needed).

## Risks & Assumptions

- **Build-specific AOBs.** The two `mov eax,[abs]` patterns and offsets are for 1.4.5.7;
  re-derive with `ce/poc_tilename.lua`. A missing pattern degrades to "Tile #N".
- **Rollback.** `git revert` + re-extract; the shipped `recipes.json` is regenerated.

## Acceptance Criteria

- [x] Station names in the cache are Terraria's tile display names (Anvil/Furnace/Work Bench/Dye Vat/Autohammer/Ancient Manipulator/…), 0 "Tile #N" for real stations (live-verified)
- [x] `_tile_names` resolves `tileLookup` + `_mapLegendCache` by AOB and reads `_value`
- [x] `createTile` derivation and the hardcoded supplement removed
- [x] 84 tests pass headless; lint clean; `recipes.json` regenerated

## Executive Summary

Fixes recipe-browser station names by using Terraria's own tile display names
(`Lang._mapLegendCache[MapHelper.tileLookup[tile]]`) instead of an item-`createTile`
heuristic that mislabeled rarer stations. Two class statics are AOB-resolved at
extraction. Reviewers: `recipes._tile_names` and `ce/poc_tilename.lua`.

## Testing

`tests/test_recipes.py` covers `station_name` (cache hit + "Tile #N" fallback). Live:
re-extraction named all 35 station tiles correctly (Iron Anvil @ Work Bench, Mythril
Anvil @ Anvil, Frostspark Boots @ Tinkerer's Workshop, Ale @ Keg, Dye Vat, Ancient
Manipulator), 0 unknowns. 84 tests pass; lint clean.
