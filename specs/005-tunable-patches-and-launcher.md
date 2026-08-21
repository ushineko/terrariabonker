# Spec 005: Tunable code patches, in-Trainer merge, and a game launcher

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

Follow-up QoL pass on the v0.3.0 code-patch feature. The patch cheats shipped with
their values baked in (mining `pickSpeed` 0.2, reach 20 tiles) and lived on a
dedicated "CE Patches" tab — but the runtime patcher needs no Cheat Engine at all
(patches are `/proc` byte-writes), so a whole tab overstated it. The window also
opened wider than the screen and could not be shrunk. This spec makes the patch
values user-tunable, folds the toggles into the Trainer tab, reserves the "CE" name
for future real CE instrumentation, adds a Steam launcher, and fixes the sizing.

## Requirements

### Functional

1. Patch cheats with a value (`mining`, `reach`) accept a caller-supplied value;
   omitting it uses the cheat's default. `fast_place` carries no value.
   - CLI: `patch enable <cheat> --value <n>`.
   - GUI: a value spinbox beside each valued cheat; changing it while the cheat is
     enabled re-applies the patch live.
2. The "CE Patches" tab is removed. Its toggles move into a **Code patches** section
   on the Trainer tab, labelled to state that no Cheat Engine is needed at runtime.
   The "CE" tab name is reserved for future actual CE instrumentation.
3. A **Launch Terraria** control starts the game through Steam
   (`steam://rungameid/105600`). It runs unprivileged and detached (Steam refuses to
   run as root) and does not go through the sudo CLI wrapper.
4. The inventory table defaults to **Slot ascending** to match the in-game order; a
   user's header-click sort still persists across refreshes.
5. The window opens at a usable size and can be resized smaller.

### Technical

6. Value threads `GUI spinbox -> client.patch_set_argv(cheat, on, value) -> CLI
   --value -> Patcher.enable(name, value) -> _set_value(override)`. `patcher.py`
   stays toolkit-free; the GUI reaches it only through the CLI `--json` contract.
7. The window-width regression was a non-wrapping `QLabel` establishing a large
   minimum width; fixed with `setWordWrap(True)` on the status/help labels.

## Risks & Assumptions

- **Value not read back.** `patch status` reports only on/off, not the live field
  value, so a spinbox shows the last-set/default rather than the game's current
  value. Acceptable: the field is set on enable and on spinbox change.
- **Launcher depends on Steam.** Falls back to `xdg-open`; logs a clear failure if
  neither handler starts. No effect on the memory features.
- **Rollback.** Pure GUI/CLI/plumbing change over v0.3.0; `git revert`. No memory
  behaviour changed except that enable can now use a non-default value.

## Acceptance Criteria

- [x] `patch enable <cheat> --value <n>` sets the given value; default when omitted
- [x] `Patcher.enable(name, value=None)` overrides `on_value`; `fast_place` ignores it
- [x] GUI: valued cheats show a spinbox; live change re-applies; toggles work
- [x] "CE Patches" tab removed; toggles present in a Trainer "Code patches" section
- [x] Launch Terraria starts Steam unprivileged/detached (no sudo), logs result
- [x] Inventory defaults to Slot ascending; user sort persists across refreshes
- [x] Window resizes smaller than its opening width (word-wrapped labels)
- [x] 53 tests pass headless; lint clean; GUI constructs offscreen

## Executive Summary

Makes the v0.3.0 code-patch cheats tunable (a `--value` on the CLI, spinboxes in the
GUI), folds their toggles into the Trainer tab (dropping the misnamed "CE Patches"
tab, reserved now for real CE work), adds a Steam launcher button, defaults the
inventory to slot order, and fixes a window that opened too wide to resize. Reviewers:
start at `gui/main_window.py` (`_patches_group`, `_launch_terraria`) and the value
thread through `patcher.enable`.

## Testing

Existing suite (53 tests) covers the value override via the patcher tests and the
`patch --value` argv via the parity samples. GUI verified by an offscreen
construction smoke test (checkboxes, valued spinboxes, launch button, Slot-ascending
sort indicator). Live behaviour (spinbox re-apply, launcher) exercised manually.
