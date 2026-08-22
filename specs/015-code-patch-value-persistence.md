# Spec 015: Persist code-patch values + de-jargon the patch tooltips

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

Two user-reported issues with the code-patch cheats (Trainer tab):

1. **Values were not saved.** The patcher persisted each cheat's on/off state and
   injection records to `~/.config/terrariabonker/patches.json`, but not the *value*
   applied (mining speed, reach tiles, pickup multiplier, enemy cap, …). On a GUI
   refresh or restart the spinboxes reset to each `ValueSpec.default` even though the
   game still held the user's applied value — a confusing mismatch between the UI and
   the running game.
2. **Tooltips said "code cave."** The `tool_reach`, `pickup`, and `spawn_rate` checkbox
   tooltips (and the group tooltip) exposed the injection implementation term "code
   cave," which is meaningless to a user.

## Requirements

1. The value last applied for each valued cheat is persisted (keyed by the game pid,
   like the rest of the patch state) and restored across a fresh process — CLI or GUI.
2. The GUI restores each spinbox to the persisted value on `refresh_patches`, not to
   the `ValueSpec` default.
3. The code-patch checkbox tooltips and the group tooltip contain no implementation
   jargon ("code cave", "byte patch", "/proc", "Cheat Engine").

### Technical

4. `Patcher` gains a `_values: dict[name, number]`, loaded/saved in `_load_state`/
   `_save_state` (new `"values"` key, filtered to known valued cheats), and written by
   `enable()` via `_record_value()`. A `values()` accessor returns the persisted value
   per valued cheat, falling back to the `ValueSpec` default.
5. `patch status --json` emits `{"on": {name: bool}, "values": {name: number}}`.
   `gui.client.parse_patch_status` normalizes this (and the legacy flat `{name: bool}`
   shape) to `{"on": ..., "values": ...}`; the GUI `_render_patches` restores spinboxes
   from `values`.

## Risks & Assumptions

- **Pid-scoped, by design.** A game restart changes the pid, the state file no longer
  matches, and values reset to defaults — the same reset semantics as the patches
  themselves (a restart clears them). Not a regression.
- **Value survives disable.** A recorded value is kept when a cheat is toggled off so
  re-enabling restores it; it is only overwritten by the next `enable(value=…)`.
- **Rollback.** `git revert`. The added `"values"` state key is ignored by older code
  (`s.get(...)`), and the new JSON status shape is back-compatible via the parser.

## Acceptance Criteria

- [x] `patch status` (text + `--json`) reports each valued cheat's persisted value
- [x] A value applied in one process is restored in a fresh process (live-verified:
      `tool_reach=88` survived a new CLI invocation via the state file)
- [x] GUI `_render_patches` restores spinboxes from the `values` map (not defaults)
- [x] No "code cave" / "byte patch" / "/proc" / "Cheat Engine" in the patch tooltips
- [x] 87 tests pass headless (4 new: default values, record+restore, survive-disable);
      lint clean

## Executive Summary

Fixes two code-patch issues. Persistence: the patcher now records each valued cheat's
applied value in its pid-scoped state file and exposes it via `values()` /
`patch status --json`, so the GUI restores spinboxes to the real applied value instead
of resetting to defaults. Tooltips: dropped the "code cave" (and related) jargon from
the patch checkbox and group tooltips. Reviewers: `Patcher._record_value`/`values`,
`gui.client.parse_patch_status`, and the `INJECTIONS` notes.

## Testing

`tests/test_patcher.py` adds `test_values_default_before_any_apply`,
`test_applied_value_is_recorded_and_restored`, `test_value_survives_disable`. Live:
`patch enable tool_reach --value 88` then a fresh `patch status --json` reported
`values.tool_reach == 88` (cross-process restore from the state file). 87 tests pass;
lint clean.
