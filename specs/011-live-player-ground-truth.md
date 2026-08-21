# Spec 011: Read the live player by ground truth (Main.player[myPlayer])

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

Bug: the trainer's inventory/status could show a **stale player copy** — a snapshot
that diverged from the live player during play. Root cause: `pick_live` guesses the
live copy by sampling `statLife` for activity, which is static at full HP, so it
returns `None`; the service then fell back to "the copy with the most non-empty
inventory slots," and a frozen snapshot can outnumber the live player's items (seen
live: live copy = 15 items, stale copy = 37, so the wrong one was chosen). The
activity heuristic is fundamentally unfit here: the trainer is used while its window
has focus, so Terraria's pause-on-focus-loss freezes every copy.

## Requirements

### Functional

1. Reads that need the live player (status, inventory display) resolve the **actual**
   live player — `Main.player[Main.myPlayer]` — regardless of pause state.
2. If ground-truth resolution fails (e.g. game update moved the pattern), fall back to
   the existing scan + heuristic rather than error.

### Technical

3. `resolve_local_player(mem)` AOB-finds `Main.get_LocalPlayer` by its JIT tail
   (`39 48 0C 0F 86 07 00 00 00 8D 44 88 10 8B 00 C3` — cmp/jbe/lea/mov/ret index
   pattern), reads the `Main.player` and `Main.myPlayer` static addresses from the two
   preceding `mov reg,[abs]` operands, indexes the `Player[]` szarray
   (`arr + myPlayer*4 + 0x10`), and adds `Player.statLife`'s object offset
   (`STATLIFE_FROM_OBJ = 0x738`) to get the live `statLife` address. Verified live.
4. `Service._select_live` tries ground truth first, then `pick_live`, then richest
   inventory. `live_block` and `snapshot` both use it. Writes still target every copy
   (harmless; a safety net).

## Risks & Assumptions

- **Build-specific.** The `get_LocalPlayer` tail and `0x738` are for 1.4.5.7; the AOB
  fails safe (returns None → scan fallback) if a game update moves them. Re-derive with
  `ce/poc_localplayer.lua`.
- **`/proc` safety.** `_exec_regions` returns empty on OSError, so a missing
  `/proc/<pid>/maps` (tests) makes `resolve_local_player` return None, not crash.
- **Rollback.** Additive read-path change; `git revert`. Writes/freezing unchanged.

## Acceptance Criteria

- [x] `resolve_local_player` returns the live `Main.player[myPlayer]` (live-verified: 0xbd6b780, not the stale copy)
- [x] Inventory/status now match the in-game inventory (Torch ×213 etc.), not a snapshot
- [x] Falls back to scan + heuristic when the pattern is absent (returns None safely)
- [x] `_select_live` centralizes the choice; `live_block` + `snapshot` use it
- [x] Unit tests: resolve from a planted `get_LocalPlayer`; missing-pattern returns None
- [x] 79 tests pass headless; lint clean

## Executive Summary

Fixes the trainer reading a stale player copy by resolving the live player through
ground truth — `Main.player[Main.myPlayer]`, via an AOB on `Main.get_LocalPlayer` —
instead of an activity heuristic that fails while the game is paused (which it is
whenever the trainer window has focus). Reviewers: `locate.resolve_local_player` and
`service._select_live`.

## Testing

`tests/test_localplayer.py` plants a `get_LocalPlayer` + `Main.player`/`myPlayer` +
player object in a fake image and asserts the resolver returns the right block, and
that a missing pattern returns None. Live: `resolve_local_player` returned the live
copy (0xbd6b780) and the CLI inventory matched the in-game inventory (Torch ×213,
Wood, Campfire, Abigail's Flower) instead of the stale snapshot. 79 tests pass; lint
clean.
