# Validation Report — CLR world-tile reads (ore extractor, read side)

**Date**: 2026-10-09
**Commit**: uncommitted (working tree on `main` at 4e1a7bc)
**Status**: PASSED
**Scope**: spec 052, ore extractor part 1 of 2 (the read side). The tile map now
reads under .NET Framework; the `vein` dry-run works on Windows. Mining (the drain
stub + per-frame hook) is the next part.

## What changed

- `layout.TileShape` (new): per-runtime tile layout as entry data — a Tile object's
  type and header offsets, the active bit, the allocation stride, the array's data
  offset, and a resolver name. Added `MainStatics.TileFromPlayer` and
  `PlayerFields.PositionFromLife`.
- `internal/tiles`: made entry-aware. The two array-resolution strategies (`mono`,
  `clr`) are a registry keyed by the entry's resolver name — no runtime branch in the
  shared readers. All tile-object reads go through the entry's `TileShape`.
- `internal/service`: `TileMap` passes the entry; `tileBase` resolves the base per
  runtime (mono's static block, or the CLR's `Main.tile` reference static at the player
  slot + the entry's tile offset). `PlayerTile` reads position via
  `PlayerFields.PositionFromLife`.
- `layout.ReadTiles` feature, granted to both entries; the read-only `vein` command is
  gated on it (was gated on a full-write entry, which excluded the CLR).

## CLR tile model (measured, [[clr-tile-layout]])

`Main.tile` is a `Tile[,]` reference static (slot = `Main.player − 0x68`). The array
object keeps lenX at +0x08, lenY at +0x0C (the stride), lower bounds 0, and the data —
one Tile pointer per cell — at +0x18. A Tile object has `type` (ushort) at +0x04 and
`sTileHeader` (active bit 0x20) at +0x08. Element `[x,y] = *(data + 4·(x·lenY + y))`,
column-major, as on mono (whose offsets are type +0x08, header +0x0E, via a bounds
sub-object).

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | all packages `ok` |
| Linux (njv-cachyos): 4e1a7bc plus this tree | `make test` | 27 packages `ok` |
| Windows, live game | `vein` (dry run) | world dims `[8401,2401]`, player tile `(1667,226)`, the tile at the player read as id 0 — all correct, where before the change the player tile read as garbage |

**New tests**

- `TestCLRTileReads`: a 3×2 CLR world planted at the measured offsets, reached through
  the reference-static slot; `TypeAt`/`ActiveAt` read back what was planted, including an
  inactive cell. Mono's own tile tests are unchanged and still pass (the mono path is
  identical).

**Mutation checks** — all killed:
- the CLR type offset set to mono's 0x08;
- the CLR header offset set to mono's 0x0E;
- the CLR dimension offset moved;
- the CLR data offset moved.

## Phase 4: Code quality

- Lint 0 issues natively, for `GOOS=windows`, and on Linux.
- The two tile resolvers are distinct modules selected by name, per the architecture
  rule; no shared reader branches on runtime.
- Status: PASSED.

## Phase 5: Security review

- No dependency changes, no new network calls. Read-only tile access.
- Status: PASSED.

## Phase 5.5: Release safety

- **Linux:** unchanged — the mono resolver reproduces the previous behaviour, pinned by
  the existing tile tests.
- **Windows:** gains world-tile reads (the `vein` dry run). No write path added here.
- **Rollback:** revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped.
- **Next:** the ore extractor's write side — the drain stub (`PickTile` per queued tile,
  `ecx=player`) and a per-frame hook — then live mining.
