# Spec 028: Fast-placement presets + GUI About dialog and titlebar version

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: No issue tracker ticket (personal utility).

## Context

Two GUI enhancements. (1) Fast placement was on/off only (itemTime forced to 4); offer
presets **Fast / Faster / Hyper** (itemTime 4 / 2 / 1; "Fast" == the previous behaviour).
(2) The GUI had no About dialog and didn't show its version — add an About button and put
the version in the titlebar.

## Requirements

1. Fast placement offers Fast/Faster/Hyper presets; the choice persists and restores like
   other cheat values.
2. An About dialog (name, version, description, ReGrind attribution) and the version shown
   in the window titlebar.

### Technical

3. `fast_place` becomes a tunable `make_patched` cheat: the patch is `mov edi,N; nop*5` where
   N is the itemTime (lower = faster). `ValueSpec` gains a `presets` field
   `(("Fast",4),("Faster",2),("Hyper",1))`.
4. The GUI renders a `QComboBox` of preset labels (data = value) when a cheat's `ValueSpec`
   has `presets`, instead of a spinbox; `_patch_value` reads `currentData()`; `_render_patches`
   selects the preset matching the current value. The value flows through the existing
   value/profile machinery (so it persists and auto-restores).
5. Titlebar: `setWindowTitle(f"terrariabonker v{__version__}")`. About button →
   `QMessageBox.about` with the description and the FearLess "TerrariaReGrind" credit.

## Risks & Assumptions

- **Preset values.** itemTime 1 is the practical floor; `make_patched` clamps to ≥1.
- **Backward compat.** `fast_place` gains a value; the profile/values machinery already
  handles valued cheats, so auto-restore carries the chosen preset.
- **Rollback.** `git revert`. GUI + one cheat definition change; no new write behaviour
  beyond the (already-supported) tunable immediate.

## Acceptance Criteria

- [x] Fast placement offers Fast/Faster/Hyper (itemTime 4/2/1); enable/re-tune/disable and
      ground-truth `is_enabled` verified (unit test `test_fast_place_presets`)
- [x] GUI shows a preset dropdown for `fast_place` (Fast/Faster/Hyper) instead of a spinbox;
      other valued cheats keep their spinboxes (offscreen-verified)
- [x] Titlebar shows `terrariabonker v<version>`; an About button opens a dialog with the
      description and ReGrind attribution
- [x] 119 tests pass; flake8 clean; README + version 0.19.0 (user-approved)

## Executive Summary

Fast placement is now a preset cheat (Fast/Faster/Hyper = itemTime 4/2/1) via the tunable
`make_patched` mechanism and a new `ValueSpec.presets`; the GUI renders it as a dropdown and
the value persists/auto-restores like any cheat value. The GUI also gains an About dialog
(with the FearLess "TerrariaReGrind" attribution) and shows its version in the titlebar.
Reviewers: the `fast_place` cheat + `ValueSpec.presets`, the GUI preset-widget rendering, and
`_about`/titlebar.

## Testing

`tests/test_patcher.py::test_fast_place_presets` (Fast default, Hyper re-tune, disable
restores orig, ground-truth is_enabled); enumeration tests updated for the new value. 119
tests pass; flake8 clean. Offscreen: titlebar shows the version, `fast_place` renders a
Fast/Faster/Hyper combo (default Fast), other cheats keep spinboxes.
