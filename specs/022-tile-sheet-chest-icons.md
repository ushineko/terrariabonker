# Spec 022: Icons for sprite-less placeable items (trapped chests) from tile sheets

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: No issue tracker ticket (personal utility). Completes the icon-coverage fixes
> (specs 018/021).

## Context

60 recipe-output items — all "Trapped … Chest" variants (IDs 3665+) — have **no**
`Item_<id>.xnb` sprite; they showed placeholders in the recipe grid. Terraria draws these
from the Containers tile-sheets at each item's `placeStyle` (the trapped chests use tiles
441 / 468, mirroring the regular chest styles). CE recon gave the field offsets
(`Item.createTile = 0xA0`, `Item.placeStyle = 0xA8`), and the composite formula was
verified live.

## Requirements

1. Sprite-less placeable items render a real icon from their tile sheet, cached like other
   icons; the trapped chests get correct per-style icons.
2. The tile + style data is captured during recipe extraction (game-derived, committed with
   `recipes.json`), so the icon build stays disk-only.

### Technical

3. **recipe extraction**: for each placeable recipe output (`Item.createTile >= 0`), record
   `tileicons[itemID] = [createTile, placeStyle]` (offsets `0xA0` / `0xA8`), added to
   `recipes.json`; `load()` defaults the key.
4. **tile-sheet icon**: `_composite_chest(sheet, style)` builds a 32x32 image from the four
   16x16 tiles of the 2x2 chest at `frameX = (style % (width//36))*36`,
   `frameY = (style // (width//36))*38`, placed adjacently to drop the 2px inter-tile
   padding. `_tile_icon` loads `Tiles_<createTile>.xnb` (cached per sheet) and composites.
5. **extraction**: when an item has no `Item_<id>.xnb` (or it fails to decode), fall back to
   its `tileicons` entry via `_tile_icon`. Cache scope bumped to `all-v2` so existing caches
   rebuild.

## Risks & Assumptions

- **Scope of the composite.** Only sprite-less items with a `tileicons` entry hit the
  fallback; today those are exclusively 2x2 chests, so the 2x2 composite is correct. A
  future non-chest sprite-less placeable of a different tile size would render wrong (none
  observed).
- **Style wrap.** Verified for the row-0 styles the trapped chests use (0-51); the wrap
  formula (row pitch 38px) is applied defensively for higher styles but unverified beyond
  row 0.
- **recipes.json growth.** Adds a `tileicons` map (~1923 placeable outputs); small integers,
  ~tens of KB. Committed like the rest of the recipe cache (game-derived numbers).
- **Rollback.** `git revert`; delete `~/.cache/terrariabonker/` to rebuild. Disk + GUI only.

## Acceptance Criteria

- [x] Trapped chests render correct per-style icons from the Containers sheets (verified:
      Trapped Chest/Gold/Shadow/Ivy/Frozen/Skyware all distinct and correct)
- [x] Extraction skip count dropped from 62 to 2 (the last 2 are non-chest IDs with neither
      a sprite file nor a tile entry)
- [x] `createTile`/`placeStyle` captured in `recipes.json` (`tileicons`, 1923 entries);
      `load()` defaults the key
- [x] `_composite_chest` assembles the four tiles with no padding gap (unit test); 108 tests
      pass; flake8 clean; version 0.15.3 (user-approved)

## Executive Summary

Gives the 60 sprite-less trapped-chest items real icons by drawing them from the Containers
tile-sheets, the way Terraria does. Recipe extraction now records each placeable output's
`createTile`/`placeStyle` (offsets found via CE recon) into `recipes.json`; the sprite
extractor composites the 2x2 chest from `Tiles_<tile>.xnb` at the item's style when no item
sprite exists. Extraction skips dropped from 62 to 2. Reviewers: `recipes.extract`
(tileicons), `sprites._composite_chest` / `_tile_icon`, and the extract fallback.

## Testing

`tests/test_sprites.py::test_composite_chest_assembles_four_tiles` (four coloured tiles
composite with no gap). 108 tests pass; flake8 clean. Live: CE recon confirmed
`createTile=0xA0`/`placeStyle=0xA8`; a full re-extract cached 6193/6195 icons; rendered
trapped chests (441 and 468 sheets) verified visually as correct per-style chests.
