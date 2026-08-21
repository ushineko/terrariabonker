# Spec 002: Item editor, tools, and PyQt GUI

**Status**: COMPLETE
**Implementation Date**: 2026-08-20

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

Spec 001 delivered the /proc trainer core (godmode, HP/mana). This spec adds live
inventory editing, tool/weapon tweaks, and a PyQt6 control panel, all still within
the external-trainer boundary: persistent `Item` and player fields only. Frame-reset
fields (global `pickSpeed`, base tile reach, pickup range, true damage-immunity) are
out of reach for an external value-writer and are deferred to a companion Cheat
Engine table. Offsets were derived live; see docs/discovery.md.

## Requirements

### Functional

1. Reach `Player.inventory` structurally (a 59-slot `Item[]`), not by value-scanning
   a stack count (which only finds downstream caches).
2. Read every slot's type, stack, damage, auto-reuse, use-speed, pick power, tileBoost.
3. Edit a slot's stack, item type (ItemID), damage, auto-reuse, use-speed, pick, tileBoost.
4. `fast-mining`: set every pickaxe to a smooth fast use-speed and high pick power.
5. `long-reach`: extend placement reach on all items via `Item.tileBoost`.
6. Machine-readable `status --json` and `inventory --json` for the GUI.
7. A PyQt6 GUI with a Trainer tab (freezes, stats, tools) and an Inventory tab
   (editable table + give-by-name browser); items given go to the first empty slot.
8. Do not expose `Item.consumable` for editing (it eats stack-of-one items on use).

### Technical

9. Item field offsets (Terraria 1.4.5.7): type `+0x6C`, stack `+0x88`, tileBoost
   `+0x9C`, pick `+0x90`, axe `+0x94`, hammer `+0x98`, damage `+0xAC`,
   consumable `+0xBD`, autoReuse `+0xBE`.
10. The GUI runs unprivileged and shells each action out via sudo (QProcess, not
    QThread — no worker to keep alive, a real OS process to signal).
11. QProcess objects are tracked so none is GC'd mid-run, and cleaned up on close.
12. Item names come from a bundled `data/items.json` map with graceful fallback to
    `#<id>` for ids the (1.3.5-era) map does not cover.

## Risks & Assumptions

- **Item objects move.** Always re-resolve `array_addr()` + slot on each access; a
  cached item pointer goes stale (cost several probe rounds during discovery).
- **`consumable` is dangerous.** Not exposed; recovery relies on cloning a clean
  `ContentSamples` template of the item type from memory.
- **Name map is 1.3.5-era** (3,929 items). Covers a fresh inventory and most items;
  1.4-only ids show as `#<id>`. Regenerate from a newer `ItemID.cs` to fill in.
- **Rollback**: no persistent state; stopping the tool ends all effects. Uninstall
  removes only a symlink and desktop entry.
- **View coupling**: the GUI currently consumes the CLI's `--json` subprocess
  contract; a missing flag caused a runtime gap (fixed). A shared service layer with
  parity tests is planned (spec 003) to make such drift a test failure.

## Acceptance Criteria

- [x] Inventory reached structurally; dirt stack edit changes the on-screen count
- [x] All listed item fields read and written
- [x] `set-item` edits type/stack/damage/auto-reuse/use-speed/pick/tile-boost
- [x] `fast-mining` speeds every pickaxe; `long-reach` extends placement reach
- [x] `damage` offset corrected from the initial `prefix` mislabel (tooltip-verified)
- [x] `autoReuse` enables auto-attack; `consumable` identified and NOT exposed
- [x] `status --json` and `inventory --json` produce parseable output
- [x] GUI Trainer + Inventory tabs function; give-by-name lands in the first empty slot
- [x] GUI processes are tracked and cleaned up (no SIGABRT on quit, one-shots complete)
- [x] Unit tests pass headless with no game and no root

## Executive Summary

Adds a full live inventory/item editor and a PyQt6 control panel to the Terraria
trainer, all through persistent-field memory edits. The inventory is reached
structurally through `Player.inventory[]`; item fields (type, stack, damage,
auto-reuse, use-speed, pick, tileBoost) were derived live and are editable from
both the CLI and GUI. Reviewers should start at `inventory.py` (the Item field map)
and `docs/discovery.md`. A follow-up (spec 003) introduces a shared service layer
so the CLI and GUI cannot drift.

## Testing

`tests/test_inventory.py` plus the spec-001 suite — 21 tests, all passing, headless
against an in-memory fake process. GUI verified by offscreen instantiation and event
loop; item edits verified live against the running game.
