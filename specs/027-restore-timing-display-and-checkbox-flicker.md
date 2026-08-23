# Spec 027: Auto-restore timing/display fixes + checkbox no-flicker

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: No issue tracker ticket (personal utility). Fixes/refinements on top of spec 026
> (auto-restore) plus a GUI toggle-flicker fix, validated by a clean-room relaunch test.

## Context

After spec 026, a live launch-from-TB test showed: cheats restored but item edits did not,
and value spinboxes appeared reset. Root causes: (1) at world-entry the inventory isn't
populated yet, so item edits were skipped and the retry only re-fired for *pending cheats*,
not skipped items; (2) the GUI's `values()` reads the pid-keyed live state, which is empty on
a fresh game, so spinboxes showed spec defaults instead of the saved values (looking
unrestored, and risking a toggle re-applying the default). Separately, mass-toggling
checkboxes showed a flicker: clicking cheat B while cheat A was applying let an in-flight
status refresh momentarily un-check B before B was applied.

## Requirements

1. Item edits restore even when the inventory loads a few seconds after world-entry.
2. Value spinboxes reflect the user's saved values on a fresh game.
3. A queued/mid-apply checkbox must not be flipped by an in-flight status refresh.

### Technical

3. **Retry on skipped items** — the GUI auto-restore retries (cap 8, ~16 s) while anything
   is unresolved (pending cheats OR skipped items), so item edits skipped because the
   inventory wasn't loaded get re-applied once it is. A genuinely moved/absent item stays
   skipped and just exhausts the budget.
4. **Profile-value display** — `Patcher.values()` prefers the live per-pid value, then the
   cross-session profile value, then the spec default. On a fresh game the spinboxes show the
   saved values (e.g. reach 75), not defaults.
5. **Checkbox no-flicker** — track the in-flight cheat; `_render_patches` skips checkboxes for
   cheats that are queued (`_cheat_pending`) or mid-apply (`_cheat_inflight`), leaving them as
   the user set them until the operation confirms.

## Risks & Assumptions

- **Retry budget.** Permanently-skipped items (moved slots) cause a few wasted retries per
  launch, then stop — negligible.
- **Display precedence.** `values()` prefers the live value over the profile, so an in-session
  change still shows correctly; the profile only fills the gap on a fresh game.
- **Rollback.** `git revert`. GUI + one common-layer read-precedence change; no new writes.

## Acceptance Criteria

- [x] Auto-restore re-applies item edits after the inventory loads (retry on skipped);
      clean-room relaunch restored both edited items (Slime Whip, Boomstick) with exact stats
- [x] Spinboxes show saved values on a fresh game (`values()` profile fallback); verified
      reach/tool_reach/pickup read 75, spawn 2, loot 2 after relaunch
- [x] A queued/in-flight checkbox is not flipped by an in-flight refresh (offscreen-verified:
      stays checked while in-flight, reflects ground truth once confirmed)
- [x] 119 tests pass; flake8 clean; version 0.18.1 (user-approved)

## Executive Summary

Three fixes surfaced by a clean-room launch-from-TB test of auto-restore: (1) auto-restore
now retries while item edits are still skipped (the inventory populates a few seconds after
world-entry), (2) `Patcher.values()` falls back to the saved profile so value spinboxes show
the user's values on a fresh game instead of defaults, and (3) `_render_patches` no longer
flips a queued/mid-apply checkbox from an in-flight status refresh. Verified end-to-end: a
relaunch restored all 9 cheats with correct values and both edited items with exact stats.
Reviewers: `_do_restore` retry condition, `Patcher.values`, and the `_cheat_inflight`/busy
skip in `_render_patches`.

## Testing

119 tests pass; flake8 clean. Offscreen: the flicker fix (in-flight checkbox stays set,
confirms to ground truth). Live clean-room relaunch: profile saved correctly (9 cheats +
75/2/2 values + two item edits), and after launch-from-TB all cheats applied with correct
values and both items (Slime Whip slot 0, Boomstick slot 1) re-applied exact stats.
