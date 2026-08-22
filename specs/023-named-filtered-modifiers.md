# Spec 023: Named, class-filtered item modifiers + inventory indicators

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: No issue tracker ticket (personal utility). Modifier names/categories are
> factual game data (modifier IDs, names, and which weapon class they roll on), extracted
> from / derived against the user's own game — analogous to the existing ItemID map.

## Context

The item editor showed the prefix (modifier) as a raw integer, offered every value
regardless of item type, and the inventory gave no sign an item was modified. Terraria's
modifiers are class-specific (melee, ranged, magic, summon weapons and accessories each
have their own pools), so an integer tier is both unreadable and error-prone.

## Requirements

1. The editor shows the modifier by **readable name** (e.g. 85 → "Fabled"), not an integer.
2. The modifier chooser is **filtered to the item's damage class** — a summon weapon offers
   summon + universal modifiers, an accessory offers only accessory modifiers, etc.
3. A modified item shows the modifier **in its name** (e.g. "Fabled Slime Staff") and a
   **colour-coded corner dot** in the inventory grid (green = beneficial, red = detrimental,
   gray = neutral).

### Technical

4. **Names** — `tools/extract_item_names --prefixes` reads `Terraria.ID.PrefixID` consts +
   the `Prefix` localization section into `data/prefixes.json` (97 modifiers).
5. **Applicability + quality** — `prefixes.py`: `valid_prefixes(flags)` returns the modifier
   IDs for an item's class flags (universal `36–61` for weapons; class pools melee `1–15`+81,
   ranged `16–25`+82, magic `26–35`+83, summon `84–97`; accessory `62–80`); `quality(id)`
   returns good/bad/neutral for the dot.
6. **Item class flags** — `Item.accessory` (`0x7D`), `melee` (`0x15D`), `magic` (`0x15E`),
   `ranged` (`0x15F`), `summon` (`0x160`), found via CE recon (`ce/poc_itemcat.lua`), read
   into `inventory.Slot.flags` and carried through `service.ItemSlot.flags` (JSON to the GUI).
7. **Editor** — the prefix `QSpinBox` becomes a `QComboBox` of valid modifier names (current
   value kept visible even if off-pool; a placed-new item offers all; a non-weapon/-accessory
   item offers none).
8. **Inventory** — the cell tooltip/name uses the modifier-prefixed name; `_cell_icon` draws
   a small quality dot; the tooltip's prefix line shows the modifier name.

## Risks & Assumptions

- **Category/quality curation.** The class pools and good/bad sets are Terraria's documented
  modifier categorisation (by ID range/set). Verified live: a summon Slime Staff offers
  summon + universal (not melee-size or accessory) with its actual modifier (Fabled/85)
  selected; 65=Warding classifies as accessory; 39=Broken as detrimental. A rare
  mis-categorisation only mislabels/misfilters a modifier — it never corrupts an item; the
  current value is always kept selectable.
- **Class flags.** Read from the live item object; `noMelee`/whip nuances are not modelled
  (melee always offered size modifiers) — acceptable for an editor.
- **Rollback.** `git revert`. Data + GUI only; the write path (`set-item --prefix N`) is
  unchanged.

## Acceptance Criteria

- [x] Editor shows the modifier by name in a dropdown (verified: Slime Staff → "Fabled (85)")
- [x] Dropdown filtered to the item's class — summon offers summon+universal, excludes
      melee-size and accessory; accessory offers accessory-only (unit-tested + live)
- [x] Inventory tooltip/name shows the modifier-prefixed name ("Fabled Slime Staff"); the
      cell shows a green/red/gray quality dot (verified: Fabled=green, Broken=red)
- [x] `data/prefixes.json` (97 modifiers) extracted via `--prefixes`; class flags read into
      the Slot and carried to the GUI
- [x] 114 tests pass (7 new); flake8 clean; README + version 0.16.0 (user-approved) updated

## Executive Summary

Item modifiers are now shown by readable name, the editor's modifier chooser is filtered to
the item's damage class (melee/ranged/magic/summon/accessory), and a modified item shows its
modifier in the name plus a colour-coded corner dot in the inventory. Modifier names come
from `data/prefixes.json` (extracted from Terraria.exe via `--prefixes`); `prefixes.py`
holds the class pools and good/bad quality; the item's class flags are read from memory
(offsets from CE recon) and flow to the GUI. Reviewers: `prefixes.py`, the `Item` flag reads
in `inventory.py`, and the editor/inventory changes in `gui/`.

## Testing

`tests/test_prefixes.py`: name lookup, quality, and applicability (summon excludes
melee-size/accessory; accessory is accessory-only; melee has size+universal not magic).
`tests/test_invgrid.py` updated for the named modifier line. 114 tests pass; flake8 clean.
Live: class-flag offsets validated on the player's items (Slime Staff/Whip summon, pickaxe/
axe melee); offscreen renders confirmed the filtered dropdown and the inventory dots.
