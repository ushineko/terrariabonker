# Spec 026: Auto-restore cheats and item edits on a fresh game

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: No issue tracker ticket (personal utility).

## Context

There is currently **no** restore-on-launch. Cheats persist only for the life of a game
process (byte patches in JIT code); item stat-edits don't survive save/exit at all because
Terraria saves an item as just **type + stack + prefix** and regenerates the rest from the
type via `SetDefaults` on load. So when a fresh game starts (launched from TB or restarted
externally), the previously-applied cheats and item edits are gone.

This adds a cross-session **profile** of the desired config and re-applies it whenever a
fresh game is detected — auto-apply, for any newly-detected game (user-approved). Item
edits are included, which makes session-only item stats effectively persist *the TB way*.

## Requirements

1. Persist the **desired config** independent of game pid: which cheats are on + their
   values, and per-slot item edits.
2. When a game with a **new pid** is detected and its player/inventory is ready, **auto
   re-apply** the desired config (cheats + item edits), respecting the version guard.
3. Re-apply is **resilient to JIT timing**: methods JIT lazily (e.g. fast-placement only
   compiles when an item is first used), so restore retries as anchors become available and
   never crashes on a not-yet-ready anchor.
4. Item restore must **not clobber** legitimate in-game inventory changes.

### Technical

5. **Profile store** — `terrariabonker/profile.py`: `~/.config/terrariabonker/profile.json`
   = `{"cheats": {name: value|null}, "items": {slot: set-item-kwargs}}`, pid-independent,
   written atomically under a lock (same pattern as the patch state). Updated by the common
   layer on every mutating action: `patch enable/disable` records/removes the cheat + value;
   `set-item` records the slot's kwargs; clearing a slot records an empty marker.
6. **`restore` operation** (service + `terrariabonker restore` CLI, guarded): re-apply the
   profile to the running game. Cheats first (each `enable(name, value)`; an unresolved
   anchor is caught and reported, not fatal). Then items: for each recorded slot, re-apply
   **only if the slot currently holds the same item type** as the edit (re-applying stats to
   the same item) — a differing type means the player changed it in-game, so skip to avoid
   clobbering; an empty marker is applied only if the slot is empty-or-same-type. Returns a
   per-entry applied/skipped report.
7. **GUI trigger** — on the status poll, track the last-restored pid. When the detected pid
   differs and a player is present, invoke `restore` (via the sudo CLI). Re-invoke on
   subsequent polls until every desired cheat reads back on (handles lazy JIT), capped at a
   few attempts per pid; log what was restored/skipped. Unprivileged features unaffected.

## Risks & Assumptions

- **Item clobber.** The type-match guard means restore only re-applies stats to an item of
  the same type in that slot; it won't overwrite a slot the player changed. Trade-off: if the
  player legitimately re-obtained the same item type, restore still re-applies the old stats
  to it (acceptable — that's the "persist my edit" intent).
- **Lazy JIT.** Cheats whose method hasn't compiled yet (fast-placement until first use) are
  retried; if the anchor never appears within the attempt cap, restore logs it and moves on.
- **Auto-apply safety.** Restore runs only on a version-compatible build (the existing guard)
  and only re-applies what the user themselves last set; it logs every change.
- **Concurrency.** Profile writes reuse the atomic-write + flock pattern from the patch state.
- **Rollback.** `git revert`; delete `~/.config/terrariabonker/profile.json` to forget the
  saved config. No new game-write behaviour beyond re-applying the user's own edits.

## Acceptance Criteria

- [x] Enabling/disabling a cheat (`patcher`) and editing/clearing an item (`cmd_set_item`)
      update the pid-independent profile (`profile.py`)
- [x] `terrariabonker restore` re-applies the profile to the running game (cheats + matching
      item slots), guarded by the version check, with a per-entry report
      (`cheats`/`items`/`pending`/`skipped`); an unresolved anchor is reported `pending`, not
      fatal (live-verified: re-applied `reach` to a fresh game)
- [x] Item restore re-applies stats only to slots holding the same item type (no clobber);
      unit-verified it skips a slot whose item changed. Empty markers are **never
      auto-cleared** on restore (safest — restore never deletes inventory), a refinement of
      the spec's original wording
- [x] GUI auto-restores on a fresh in-world game (new pid), retrying (cap 5) for lazily-JIT'd
      cheats, once per pid, logging what was restored (gating verified offscreen)
- [x] Tests: profile round-trip + disable-removes + absent (`test_profile.py`); restore item
      type-match guard (`test_service.py`); patcher tests isolate the profile path; 119 tests
      pass; flake8 clean
- [x] README documents auto-restore + the item type-match/persistence note; version 0.18.0
      (user-approved)

## Alternatives Considered

- **Re-apply item edits unconditionally**: rejected — clobbers legitimate in-game inventory
  changes. The type-match guard is the safer default.
- **Hook Terraria's save/load to persist item stats natively**: rejected — far more invasive
  and fragile than a TB-side re-apply, for the same user-visible result.

## Executive Summary

Adds auto-restore: a pid-independent profile (`profile.py`) records the user's desired
cheats (+ values) and per-slot item edits, updated by the common layer on every
enable/disable and set-item; a `restore` operation (`service.restore` + `terrariabonker
restore`) re-applies it to the running game — cheats first (pending on not-yet-JIT'd
anchors), then item edits with a type-match guard so it never clobbers a slot the player
changed. The GUI auto-invokes restore when a fresh in-world game appears, retrying for
lazily-JIT'd cheats. This fixes both reported bugs: cheats now restore on any fresh game,
and item stat-edits (which Terraria can't save — only type/stack/prefix persist) effectively
persist because TB re-applies them. Reviewers: `profile.py`, `service.restore`, the profile
hooks in `patcher.enable/disable` and `cli.cmd_set_item`, and the GUI `_maybe_restore`.

## Testing

`tests/test_profile.py` (round-trip, disable-removes, absent-file). `tests/test_service.py::
test_restore_reapplies_matching_items_only` (type-match guard: same-type re-applied,
changed-type skipped). Patcher tests isolate `profile._PATH`. 119 tests pass; flake8 clean.
Live: `restore` re-applied `reach=30` to a freshly-restarted game; the GUI trigger gating
was verified offscreen (fires on a fresh in-world pid, not on the menu, once per pid).
