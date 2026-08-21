# Spec 008: Edit item prefix (modifier) and defense

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

The grid item editor (specs 006/007) could not touch two properties that matter for
accessories and armor: the **prefix** (modifier tier — Warding, Menacing, Legendary,
…) and **defense**. Mapping both makes "edit an accessory like a weapon/tool" complete.

## Requirements

### Functional

1. The edit dialog gains **Defense** and **Prefix (modifier tier)** fields, prefilled
   from the slot and applied on a same-item edit; the tooltip shows non-zero values.
2. CLI `set-item` gains `--defense` and `--prefix`; the GUI drives them through the
   client contract, keeping CLI/GUI parity.

### Technical

3. Offsets (1.4.5.7): `Item.defense` = **0xD4** (int, found by an armor tier ladder),
   `Item.prefix` = **0x15C** (byte). Prefix is not template-diffable (every
   `ContentSamples` template has prefix 0), so it was read from mono metadata with
   Cheat Engine (`ce/poc_item_fields.lua`), which also confirmed every existing offset.
4. `prefix` is written as a single byte; `defense` as an int32. Both apply to every
   player copy like the other item mutations. `Slot`/`ItemSlot`/`inventory --json`
   carry `defense` and `prefix`.

## Risks & Assumptions

- **Prefix names not mapped.** The dialog edits the raw prefix *number*; the canonical
  prefix names (Warding = …, Menacing = …) are not yet shown. A prefix-name map
  (extractable from the exe like item names) is the natural follow-up.
- **Build-specific offsets.** 0xD4 / 0x15C are for 1.4.5.7; re-derive with the mono
  dump after an update (`ce/poc_item_fields.lua`). A wrong offset mis-reads/writes a
  field but never escapes the item.
- **Rollback.** Additive: two offsets, two data fields, two CLI flags; `git revert`.

## Acceptance Criteria

- [x] `Item.defense` (0xD4) and `Item.prefix` (0x15C) read into `Slot`/`ItemSlot`; `inventory --json` includes both
- [x] `set-item --defense N --prefix N` writes them (byte for prefix, int for defense)
- [x] Dialog has Defense + Prefix fields, prefilled and applied on a same-item edit
- [x] Tooltip shows non-zero defense / prefix
- [x] `client.set_item_argv` covers defense/prefix; parity test green
- [x] Live-verified on an accessory: placed Hermes Boots, set defense 12 + prefix 65, read back
- [x] 73 tests pass headless; lint clean

## Executive Summary

Maps and exposes the two item properties the editor was missing — `Item.defense`
(0xD4) and `Item.prefix` (0x15C) — so accessories and armor edit as fully as weapons.
Defense came from an armor tier ladder; prefix (not template-diffable) was read from
mono metadata with Cheat Engine, which also cross-checked every existing offset.
Reviewers: `inventory.py` (offsets/setters), `service.set_item`, `gui/item_dialog.py`.

## Testing

`tests/test_invgrid.py` asserts the tooltip shows defense/prefix; the parity test
covers `set-item --defense/--prefix`. Live: placed Hermes Boots (#54) into an empty
slot (fresh defense 0 / prefix 0), set defense 12 + prefix 65 via the CLI the GUI
uses, read both back, then cleared the slot. 73 tests pass headless; lint clean.
