# Spec 021: Icon cache covers all items; animated sprites cropped to one frame

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: No issue tracker ticket (personal utility). Bug fix to the item-icon cache from
> spec 018.

## Context

Two icon bugs surfaced in the inventory grid:

1. **Missing icons for non-craftable items.** The cache was built only from recipe-referenced
   items (outputs + ingredients), so items that appear in the inventory but no recipe — e.g.
   Slime Staff (#1309), a monster drop — had no icon and fell back to abbreviated text.
2. **Wrong icons for animated items.** Animated items (Fallen Star #75, Souls, …) ship as a
   tall vertical sprite-sheet of stacked frames; the decoder returned the whole column, so
   the icon showed the entire strip instead of one frame.

## Requirements

1. The icon cache covers **every known item** (from the name map), not just recipe-referenced
   ones, so any inventory item renders.
2. Animated items are reduced to their **first frame** for display.
3. Existing (older-scope) caches rebuild automatically; both the Recipes and Inventory tabs
   trigger extraction when the cache is missing/stale.

### Technical

4. `all_item_ids()` = every ID in `names._NAMES` (∪ recipe-referenced); `extract` defaults to
   it. The `.done` marker records a `scope` (`"all-v1"`); `is_cached` requires the current
   scope, and `extract` treats a scope mismatch like `force` (rebuild rather than skip stale
   PNGs). ~6195 items, still seconds.
5. `_deanimate(img)`: for a vertical strip (`height >= 2×width`) split into N evenly-spaced
   content blocks (opaque-row runs separated by transparent rows), crop to the first frame
   (`height/N`). Single-frame items — even tall ones like a staff — have one content block and
   are returned unchanged. Applied in `extract` before saving. Self-contained (no game-memory
   frame-count data needed); validated against the actual sprite structure (Fallen Star
   208→26 px, Soul of Light 112→28 px).
6. GUI: the Inventory tab (like Recipes) kicks off extraction when `is_cached` is false; a
   `_sprites_extracting` guard prevents concurrent runs, and the icon/pixmap caches are
   cleared on completion so freshly-extracted icons replace any placeholders.

## Risks & Assumptions

- **De-animation false positives/negatives.** Guarded by the `height >= 2×width` gate plus a
  requirement of ≥2 evenly-spaced blocks dividing the height, so single-frame items (any
  aspect) are never cropped. An animation whose frames touch with no transparent separator
  would read as one block and show the full strip (not observed on sampled items); a
  non-animated item that is both very tall and has regular internal gaps could be mis-cropped
  (unlikely). A wrong crop is cosmetic and disposable (`rm -rf ~/.cache/terrariabonker`).
- **Extraction cost.** ~6195 vs ~3757 files; still seconds. One-time per scope/version.
- **Rollback.** `git revert`; delete the cache to rebuild. No memory access, no committed
  assets.

## Acceptance Criteria

- [x] Non-craftable inventory items get icons — Slime Staff (#1309) caches a real sprite
      (verified) instead of falling back to text
- [x] Animated items crop to one frame — Fallen Star (#75) 208→26 px, Soul of Light 112→28 px
      (verified visually: single star / single soul)
- [x] Cache covers all named items; a stale-scope cache auto-rebuilds (`is_cached` scope
      check + `extract` refresh-on-mismatch); Inventory tab triggers extraction when missing
- [x] `_deanimate` crops strips and leaves single-frame/wide images unchanged (unit tests);
      107 tests pass; flake8 clean; version 0.15.2 (user-approved)

## Executive Summary

Fixes missing inventory icons for non-craftable items and wrong icons for animated items. The
cache now covers every named item (not just recipe-referenced), and animated vertical
sprite-sheets are cropped to their first frame by detecting evenly-spaced content blocks — no
game-memory frame-count data required. A `scope` marker makes older caches rebuild
automatically, and the Inventory tab now triggers extraction like the Recipes tab. Reviewers:
`sprites.all_item_ids` / `_deanimate` / `extract`, and the GUI tab-change trigger.

## Testing

`tests/test_sprites.py`: `test_deanimate_crops_vertical_strip_to_first_frame`,
`test_deanimate_leaves_single_frame_tall_item`, `test_deanimate_leaves_non_strip_unchanged`,
`test_all_item_ids_superset_of_referenced`. 107 tests pass; flake8 clean. Live: a full
re-extract cached 6133/6195 icons; Slime Staff renders, and Fallen Star / Soul of Light crop
to a single frame (confirmed by rendered contact sheet).
