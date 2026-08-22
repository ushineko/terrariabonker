# Spec 018: Recipe browser overhaul — icon grid with live filter and ingredient popup

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: This work has no associated issue tracker ticket. Personal utility in a
> script monorepo. Item sprites are extracted from the user's own game install into a
> local cache and are **never committed** (copyrighted game assets; also keeps the repo
> small and the cache reconstitutable per machine).

## Context

The Recipes tab is a plain text list with a search box. Two problems: it has no visual
identity (the crafting UI is icon-driven), and its name autocomplete lists **all 6195
item names**, so it offers items that have no recipe at all (e.g. "Place Above the
Clouds", a painting) and then reports "no recipe found".

The overhaul makes it look and behave like Terraria's crafting panel: a scrollable grid
of item **icons** (craftable outputs only), a search box that filters in real time, and a
click that opens a popup detailing the recipe's ingredients (icon + name + count) and its
crafting station.

Item icons live in `Content/Images/Item_<id>.xnb` — XNB v5, **LZX-compressed** (XNA
Windows), wrapping a `Texture2D` (SurfaceFormat.Color = RGBA). No maintained python
package decodes these, so a self-contained decoder is vendored (`terrariabonker/xnb.py`,
already spiked and proven: decodes real sprites at ~1.9 ms/file, ~6–12 s for the full
referenced set). This keeps the cache reconstitutable on any machine with just
`pip install -r requirements.txt` plus the user's own game files — no node, no binaries.

## Requirements

1. **Self-contained sprite decode** — a pure-python XNB container parser + LZX
   decompressor + Texture2D (SurfaceFormat.Color) reader. No external tools; only Pillow
   (imaging) as a new dependency (numpy already present).
2. **Local icon cache**, keyed by game version (the tool currently supports only 1.4.5.7).
   Extracted lazily: on first use, or when the cache is missing / for a different game
   version. Reuses/extends the existing "Re-extract from game" action. Extraction reads
   the game's `Content/Images` from disk **unprivileged** (no sudo). Cache lives under
   `~/.cache/terrariabonker/sprites/<version>/` and is **gitignored**.
3. **Icon grid of craftable outputs** — one icon per unique recipe output (~3214),
   mirroring the crafting panel. Not the full 6195-item list, so non-craftable items no
   longer appear.
4. **Real-time filter** — typing in the search box filters the grid as-you-type by item
   name (case-insensitive substring) or exact ItemID. No "Search" button press needed.
5. **Ingredient popup** — clicking an item's icon opens a dialog showing the output
   (icon, name, "makes N"), each ingredient (icon + name + count), and the crafting
   station. When an item has multiple recipes, all are shown.
6. **Repeatability** — a `requirements.txt`; `install.sh` documents the pip step; the
   cache rebuilds from the local game files on any machine, so nothing icon-related is
   committed.

### Technical

7. **`terrariabonker/xnb.py`** (done in spike): `decompress_xnb(raw)` handles the XNB LZX
   chunk framing (`0xFF` explicit-frame marker vs default 0x8000 frame; 64 KB window);
   `LzxDecoder` is a port of libmspack `lzxd` / MonoGame `LzxDecoder`;
   `read_item_texture(path) -> PIL.Image` parses the reader manifest and the
   `Texture2DReader` payload, decoding SurfaceFormat.Color (0) and raising `XnbError`
   (logged, skipped) on any other format.
8. **`terrariabonker/sprites.py`** (new): resolve the game `Content/Images` dir (from the
   running game's exe path via `mem.exe_path()`, persisted so extraction works while the
   game is closed); `extract(version, item_ids, progress=…)` decode → save
   `Item_<id>.png` into the version cache; `cache_dir(version)`, `is_cached(version)`,
   `icon_path(version, item_id)`. Extract the **union of recipe outputs + ingredient
   itemIDs** (covers grid + popup icons). A `--extract-sprites` CLI subcommand runs it
   headless with progress to stdout.
9. **GUI** (`gui/main_window.py`): replace the Recipes tab list with a `QListView` in
   `IconMode` backed by a model + `QSortFilterProxyModel` (scales to thousands of icons
   smoothly). Items are the recipe outputs, each with its icon (from cache) and name; the
   filter proxy matches name/ItemID from the search box's `textChanged`. Double-/single-
   click opens the ingredient popup (`QDialog`) built from `recipes.by_output`. If the
   cache is absent/stale on tab open, prompt+run extraction first (worker/QProcess so the
   UI doesn't freeze; a few seconds).
10. **Fallbacks**: an item whose sprite failed to decode shows a neutral placeholder icon
    (not a crash). Extraction never blocks the rest of the app.

## Risks & Assumptions

- **SurfaceFormat coverage.** Item icons are SurfaceFormat.Color (verified on a sample).
  DXT/other formats raise `XnbError` and fall back to a placeholder; not expected for item
  sprites, logged if hit.
- **Content dir discoverability.** Needs the game's `Content/Images` path. Derived from the
  running game (`mem.exe_path()`) at extraction time and persisted; if the game has never
  been seen and no path is known, extraction reports a clear message.
- **LZX correctness.** The decoder is a port of the canonical algorithm; validated by
  decoding real sprites (correct dimensions, recognizable images) and by honoring the
  XNB-declared decompressed size. A malformed/unsupported file raises `XnbError` and is
  skipped, never crashing extraction.
- **Extraction cost.** ~1.9 ms/file → seconds for the full referenced set; run off the UI
  thread with progress. One-time per game version.
- **Assets/licensing.** Sprites are extracted from the user's own install into a gitignored
  local cache; none are committed or redistributed.
- **Rollback.** `git revert`. The cache is disposable (`rm -rf ~/.cache/terrariabonker/`);
  no game memory is touched by this feature (pure disk read + GUI).

## Acceptance Criteria

- [x] `terrariabonker/xnb.py` decodes `Item_<id>.xnb` (LZX + SurfaceFormat.Color) to an
      RGBA image; unit-tested against a synthetic uncompressed XNB and a real-file LZX case
      (skipped when the game is absent); unsupported formats raise `XnbError`
- [x] Sprite cache builds from the local game files, keyed by version; `extract-sprites`
      CLI runs it headless with progress; re-run is idempotent (skips existing PNGs).
      Cache lives in `~/.cache/terrariabonker/` (outside the repo, so nothing is committed)
- [x] Recipes tab shows an icon grid of **craftable outputs only** (non-recipe items like
      paintings no longer appear — the grid is built from recipe outputs); icons from cache
- [x] Search box filters the grid **in real time** by name or ItemID (`textChanged` →
      proxy filter; no button press)
- [x] Clicking an item opens a popup with the output, each ingredient (icon + name +
      count), and the crafting station; multiple recipes all shown; Makes/Uses toggle kept
- [x] First-run / stale-cache triggers extraction off the UI thread (unprivileged
      `QProcess`, streamed progress) reusing the re-extract action; a missing/failed sprite
      shows a placeholder icon, never a crash
- [x] `requirements.txt` added (numpy, PyQt6, Pillow); `install.sh` checks Pillow and
      points at it; the icon cache is outside the repo so nothing icon-related is committed
- [x] Tests pass headless (101 total, 9 new); flake8 clean on new files; README + version
      (0.14.0, user-approved) updated

## Alternatives Considered

- **Node `xnbcli` for extraction**: rejected. Battle-tested but adds a node/npm dependency
  that undercuts the "reconstitute on any machine" requirement; a pure-python decoder needs
  only `pip install -r requirements.txt`.
- **The `xnb` PyPI package**: rejected — it is an unrelated machine-learning library
  ("eXplainable Naive Bayes"), not an XNB content tool. No maintained python XNB package
  exists.
- **Commit pre-extracted sprites**: rejected — copyrighted game assets in a public repo,
  and it bloats the repo; extraction from the user's own files is cleaner and legal.
- **Keep the text list, just add icons inline**: rejected — the goal is the crafting-panel
  look; an icon grid + live filter is the point.

## Executive Summary

Overhauls the Recipes tab into a Terraria-crafting-style **icon grid** of craftable
items with a live search filter and a click-through ingredient popup. Item sprites are
decoded from the game's own `Content/Images/*.xnb` by a vendored, self-contained decoder
(`xnb.py`: XNB container + a port of the libmspack/MonoGame **LZX** decompressor +
Texture2D SurfaceFormat.Color) — no external tools, so the icon cache is reconstitutable
on any machine with just `pip install -r requirements.txt` plus the user's game files.
`sprites.py` caches the decoded PNGs under `~/.cache/terrariabonker/<version>/` (never
committed; copyrighted assets) and learns the game's content path from the running
process. The grid is built from recipe outputs, which fixes the old bug where the name
autocomplete offered non-craftable items (e.g. paintings). Reviewers: `xnb.LzxDecoder`,
`sprites.extract`, and the GUI `_recipes_tab`/`RecipeDialog`.

## Testing

`tests/test_xnb.py`: `_read_7bit_int`, a deterministic uncompressed-XNB Texture2D
round-trip (verifies pixel values), unsupported-format raises `XnbError`, non-XNB raises,
and a real-file LZX decode (skips when the game isn't installed). `tests/test_sprites.py`:
referenced-item union (outputs + ingredients), version-scoped cache paths, and
content-dir persistence/reload. 101 tests pass headless; flake8 clean on new files. Live:
`extract-sprites` decoded **3697 of 3757** referenced icons (the 60 skips are item IDs
with no `Item_<id>.xnb` on disk — placeholder-covered, zero decoder errors); an offscreen
render confirmed the 3214-item grid, live filter ("sword" → 32), and the ingredient popup
(Torch → Gel + Wood) all display real sprites correctly.
