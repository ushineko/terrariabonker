# Validation Report — the ore extractor on .NET Framework (all 15 cheats)

**Date**: 2026-10-09
**Commit**: uncommitted (working tree on `main` at a0d3545)
**Status**: PASSED
**Scope**: spec 052 — the ore extractor's write side. With it, all fifteen cheats work
under .NET Framework.

## What changed

The extractor's read side landed earlier (entry-aware `internal/tiles`). This adds the
write side:

- **Drain stub** (`OreExtractBodyCLR`): hooked at `Player.GrabItems`' entry — a
  once-per-frame method — it drains the arena queue (a count then (x,y) int pairs,
  consumed before the work) and calls `WorldGen.KillTile(x, y, false, false, false)` for
  each tile: break it, really remove it, spawn the drop. `KillTile` is static (x in ecx,
  y in edx, three bools pushed); esp is saved in ebx and restored after each call.
- **Anchors** `grabitems_entry` (the hook; the five displaced prologue bytes wildcarded)
  and `kill_tile` (the call target). Both unique on the live game; no jump lands inside
  the displaced bytes.
- **Gating**: `CheatFeature("ore_extract")` on the CLR entry; the `extract` command now
  attaches through the per-feature write gate so it runs under the read-only CLR entry.

`PickTile` was the mono choice but its four-int CLR signature has an ambiguous parameter
order; `KillTile` is what every vein-mining mod uses (VeinMiner research), unambiguous,
and it drops the item at the tile — which `GrabItems`, running right after the hook,
then collects.

## GC note

This calls a managed method from the arena per tile — the high-frequency case — but only
while a vein is armed (a few frames just after the player mines an ore), and the only
objects involved are the permanently-rooted world tiles and int coordinates. Collection
is impossible; a relocation would need a compacting GC in that narrow window moving a
long-lived tile object. It ran stably in play.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | all packages `ok` |
| Linux (njv-cachyos): a0d3545 plus this tree | `make test` | 27 packages `ok` |
| Windows, live game | enable, mine one gold block by hand, `extract --watch` | the watcher armed the vein and the drain broke the rest: 20/20 tiles at (2173,677), then an adjacent 3-tile pocket — 23 gold to the maintainer, counts correct, game stable |
| Windows, live game | disable | `GrabItems`' entry read back as its original prologue |

**New tests**

- `TestUnderTheCLROreExtractorIsInstalled`: plants `GrabItems`' entry and `KillTile`'s
  entry on the synthetic image, enables the cheat, and checks the hook jumps to a drain
  that sets up and calls `KillTile` at the resolved entry (x in ecx, y in edx, three
  false bools); disabling restores the prologue.

**Mutation checks** — all killed:
- the hook offset (0 → 1);
- the x load (`[esi]` → wrong);
- the y load (`[esi+4]` → wrong);
- the loop counter register.

## Phase 4: Code quality

- Lint 0 issues natively, for `GOOS=windows`, and on Linux. (A De Morgan hint on the
  test's call-finder was resolved by matching the exact per-tile byte sequence instead.)
- Status: PASSED.

## Phase 5: Security review

- No dependency changes, no new network calls. The drain calls the game's own
  `KillTile`; the write path is the existing arena mechanism.
- Status: PASSED.

## Phase 5.5: Release safety

- **Linux:** unchanged; the mono extractor keeps its `PickTile` stub.
- **Windows:** gains the ore extractor — the last of the fifteen cheats.
- **Rollback:** revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped (the maintainer is deciding when to cut one; all fifteen cheats and
  the full read/write path now work under .NET Framework).
