# Spec 004: Code-patch cheats (patcher + CE Patches tab)

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

Some cheats can't be held by writing a value: the game recomputes the field every
frame in `Player.ResetEffects` and reads it same-frame, so an external write loses
the race. The fix is a *code* patch — remove the reset (single-reset fields) or
force a constant at the read site (entangled fields). The patch sites/offsets were
derived with Cheat Engine's mono dissector (spike, see `ce/README.md`), but applying
them is a byte-write, so this runs entirely through `/proc` — **no CE at runtime**.
CE is only the research tool for re-deriving AOBs after a game update.

## Requirements

### Functional

1. A catalog of code-patch cheats: mining speed (`pickSpeed`), placement reach
   (`blockRange`), fast placement (`ApplyItemTime` timing).
2. Locate each cheat by a unique AOB anchor in executable memory (JIT address moves
   per run) and apply/restore the patch bytes, plus set/restore the associated value.
3. Toggle state persists across CLI invocations (per-pid state file); a new game
   process starts with everything off (a restart un-patches the code).
4. `patch status [--json]` / `patch enable <cheat>` / `patch disable <cheat>`.
5. A GUI "CE Patches" tab with a checkbox per cheat, reflecting live state.

### Technical

6. AOBs (1.4.5.7): `reset_block` anchor covers blockRange (reset NOP @+0) and
   pickSpeed (fstp→`fstp st(0)`+nop @+12), which are adjacent in `ResetEffects`;
   `place` anchor patches `ApplyItemTime`'s `max(edi,1)` → `mov edi,4`+nop @+20.
7. `fstp` neutralized with `DD D8` (fstp st(0), balances the x87 stack) + NOP.
8. `patcher.py` is toolkit-free (fits the shared-layer neutrality rule); the GUI
   reaches it through the CLI `--json` contract via `gui/client.py` (parity-tested).

## Risks & Assumptions

- **Build-specific AOBs.** Patterns break on a game update; the patcher raises a
  clear "re-derive with CE" error when an anchor is missing or non-unique. The `ce/`
  tools regenerate them. Version gate still applies (`--force` to override).
- **Second writers.** `pickSpeed` has a minor secondary writer (Mining Potion) that
  can nudge the value; the patch removes the dominant reset, so it stays fast. Not a
  correctness issue for the cheat.
- **Rollback.** `disable` restores the exact original bytes; a game restart clears
  all patches (fresh JIT). The patcher writes only a small state file (addresses),
  nothing that affects a save.
- **entangled vs single-reset.** `tileSpeed`/`wallSpeed` are NOT patched at the reset
  (multiple writers + autoplacement depends on it); placement speed is done at the
  read site instead. Rule recorded in `ce/README.md`.

## Acceptance Criteria

- [x] `patcher.py` catalog + AOB resolve + patch/value + per-pid state
- [x] `patch status/enable/disable` (+ `--json`) work live; enable/disable round-trips
- [x] Enabling patches the code bytes and sets the value; disabling restores both
- [x] Shared `reset_block` anchor: mining and reach toggle independently
- [x] State persists across CLI invocations; a new pid resets to all-off
- [x] Missing/non-unique anchor raises a clear PatchError
- [x] GUI "CE Patches" tab: checkboxes reflect and drive `patch` via the client
- [x] `gui/client.py` patch contract is parity-tested against the CLI
- [x] 52 tests pass headless; lint clean

## Executive Summary

Adds the code-patch half of the trainer: global mining speed, item-independent
placement reach, and fast placement — cheats a value-write alone can't hold, applied
by patching the game's JIT'd code through `/proc` (no CE at runtime; CE was the
research tool). A `patcher` module (AOB-anchored, self-restoring, state-tracked), a
`patch` CLI verb, and a GUI "CE Patches" tab. Reviewers: start at `patcher.py` and
`ce/README.md` (how the sites were derived).

## Testing

`tests/test_patcher.py` (AOB resolve, patch/value round-trip, shared-anchor
independence, state persistence, error paths) plus the parity/neutrality guards — 52
tests total, headless. Live-verified against the running game: all three cheats
enable/disable cleanly with the code bytes restored to stock.
