# Validation Report — CLR auto-fishing (auto_use, write side)

**Date**: 2026-10-10 20:04
**Commit**: uncommitted (working tree on `main` at 9937efd)
**Status**: PASSED
**Scope**: spec 052 — auto-fishing part 2 of 2 (the write side). The auto_use
stub now presses the use button under .NET Framework, and the `catch` command
reels bites and recasts the line. Part 1 (the projectile reads it needs) landed
in 9937efd.

## What changed

- `internal/patch/injections_netfx.go`: the `auto_use` injection, a per-frame
  stub that sets two control bytes on the player when the trainer arms it
  (`NetfxAutoUseBody`, unchanged from the first draft — consume the armed flag,
  then `mov byte [ecx+0x7E4],1` / `mov byte [ecx+0x7F1],1`, then tally). Its
  anchor is now `item_check` (Player.ItemCheck's entry), replacing the earlier
  `player_update` (Player.Update's entry). `this` is in ecx at both; the body did
  not change, only where it is hooked.
- `internal/layout/entry_netfx.go`: `SelectedItemFromLife` measured
  (`0x9F4 - 0x470`). This build has no plain `selectedItem` int; the hotbar index
  is the `selected` word of the inlined `SelectedItemState` struct at Player+0x9F0.
  `internal/layout/clr.go` gains the `selectedItemState` field (0x9F0) so the
  table verifies against metadata.
- `internal/cli/catch.go`: `catch`, `catch-tick`, `catch-stop` gate on the
  `auto_use` cheat via `gameWriting`, so they run on either runtime.

## Why the hook moved (the bug the live test found)

The first draft hooked `Player.Update`'s entry. It installed cleanly and the
press counter climbed, but no fish reeled and the character sat idle: a write to
`controlUseItem` at Update's entry is overwritten by the game's own input latching
later in the same frame, before the control is read. `Player.ItemCheck` is where
the control is read — it compares `controlUseItem` at ItemCheck+0x3D9 and touches
nothing that clears it since entry — so a write at ItemCheck's entry is the last
word before the read. This mirrors the mono build, which hooks Update's call to
`BordersMovement`, ~50 IL before `ItemCheckWrapped` reads the control. Addresses
and the read site were found with the DotNetDataCollector (CE 7.7) and confirmed
against the live game (`Player.ItemCheck` native prologue, `controlUseItem`/
`releaseUseItem` offsets from `cmd/clrfields`).

Recast was then still gated off because `HoldingRod()` could not read the held
slot on the CLR (`SelectedItemFromLife` was 0, unmeasured). Measuring the
`SelectedItemState.selected` word turned recast on.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -race ./...` | all packages `ok`, 0 failures |
| Windows | pinned `golangci-lint-v2.12.2` (go1.27 build), project config | 0 issues |
| Windows | `gofmt -l internal/ cmd/` | clean |
| Windows | `clrfields --verify` (live game) | every CLR field agrees with the runtime |

**New / changed tests**

- `TestNetfxAutoUseBody` (patch): the stub's exact bytes — armed-flag consume,
  both control writes at +0x7E4/+0x7F1, the press tally, the displaced prologue.
- `TestUnderTheCLRAutoUseIsInstalled` (cli): re-pointed to the ItemCheck prologue
  fixture; asserts the jump lands at ItemCheck's entry, the stub sets both control
  bytes, and disabling restores the prologue.
- `TestUnderTheCLREveryCheatHasASite` (cli): auto_use, the last catalogue cheat,
  now resolves a CLR site; a cheat whose anchor does not resolve is still refused
  without writing.
- `TestTheCLRHeldSlotReadsSelectedItemState` / `TestTheCLRHeldSlotRejectsOutOfRange`
  (inventory): the held slot is the `selected` word; an in-range slot reads back
  and a rod there reads as holding a rod; an out-of-range value says "cannot tell"
  so auto-catch never presses against it.
- `TestTheCLRItemFieldsAreTheTable` (layout): the CLR selected-item offset is tied
  to metadata — `(selectedItemState + 0x04) − statLife`.
- Two frozen-table digests re-pinned deliberately after review (the version table
  for the new field; the CLR field table for `selectedItemState`).

**Mutation checks** — all killed:
- auto_use anchor left at `player_update` → the live game installs but never reels
  (the original bug; confirmed by hand before the fix).
- `SelectedItemFromLife` left 0 → `TestTheCLRHeldSlotReadsSelectedItemState` fails
  (held slot unreadable), and recast never fires.
- held-slot range guard widened past 0..9 → `TestTheCLRHeldSlotRejectsOutOfRange`
  fails.

## Phase 4: Code quality

The stub body and `catch` logic are shared across runtimes; the only
runtime-specific additions are table data (an anchor, an offset) and a
runtime-named body, matching the project's version-table architecture. No dead
code, no duplication introduced. The obsolete "held slot is unmeasured" test was
replaced rather than left asserting a condition that is no longer true.

## Phase 5: Security review

- No hardcoded secrets; none added.
- No new subprocess calls and no new network calls in shipped code. (Cheat Engine
  was used only for offline method discovery, not in the binary.)
- Writes are two fixed-offset control bytes on the player object the game already
  owns; the held-slot read is bounded to 0..9. No untrusted input reaches either.
- The cheat ships OFF; nothing presses the use button unless the trainer arms it.

## Phase 5.5: Release safety

- **Change type**: code (a cheat port) plus additive version-table data.
- **Pattern**: additive — a new anchor and a new measured offset; the auto_use
  injection re-points to a different anchor but the body and overwrite are
  unchanged. No schema/API/index.
- **Rollback**: `patch disable auto_use` restores the ItemCheck prologue in place
  (the disable path writes back the recorded site). To undo the build, revert the
  commit and rebuild. Minutes, no data heroics.
- **Rollout**: the cheat is off by default; the player enables it.

## Live confirmation (Windows, 2026-10-10)

Enabled auto_use on the running game; with a line in the water, `catch --recast`
reeled a run of fish — Flounder, Oyster, Rock Lobster, and Mythril/Mirage/Titanium
crates — and cast the line again after each (`reeled in …` then `cast the line`,
repeatedly). The maintainer watched the character reel and recast on screen.
Disabled; the ItemCheck prologue read back and the game stayed responsive.

## Overall

- All gates passed: YES
- Notes: part 2 of 2 for auto-fishing; spec 052 slice 15 marked done.
