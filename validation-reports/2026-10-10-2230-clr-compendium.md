# Validation Report — CLR item compendium (spec 052, slice 16)

**Date**: 2026-10-10 22:30
**Commit**: uncommitted (working tree on `main` at 72ec744)
**Status**: PASSED
**Scope**: the item catalog reads the game's pristine item templates under .NET
Framework. The template scan now takes its item field offsets from the entry
instead of fixed mono constants, so it works on either runtime. The NPC half of
the catalog stays mono-only this slice (CLR NPCs are listed by name, no stats).

## What changed

- `internal/content/content.go`: `ItemOffsets` + `ItemOffsetsFrom`, threaded
  through `FindItemTemplates`/`readItemStats`; the scan span is computed from the
  offsets. The NPC reads are untouched (still mono constants), gated off the CLR.
- `internal/layout/table.go`: a `ContentFields` block on `Entry` (the six
  catalog-only item offsets `ItemFields` lacks: createTile, healLife, healMana,
  head/body/leg slot; and `NPCStats`). New `ReadContent` feature.
- `internal/layout/entry_mono.go` / `entry_netfx.go`: each gains a `Content`
  block (mono from its constants, `NPCStats: true`; netfx from the CLR
  measurement, `NPCStats: false`) and `ReadContent` in its reads.
- `internal/service/compendium.go`: the item scan is handed the entry's offsets;
  the NPC scan returns empty under an entry whose `Content.NPCStats` is false.
- `internal/cli/catalog.go` / `cli.go`: `compendium` gates on `ReadContent`; the
  `Writes`-based `gameReadingAll` (its only caller) is removed.

## Why ContentFields is separate from ItemFields

The six offsets are read only by the catalog, not the item editor, so keeping
them out of `ItemFields` leaves the editor's struct and its frozen digest
unchanged. The NPC scan finds its array through a runtime's statics and szarray
layout; the CLR locates statics player-relative (`MainStatics`), a different path
from mono's `Main.npc` static, so `NPCStats` gates that half off until it is
ported.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -race ./...` (cgo) | all packages `ok`, 0 failures |
| Windows | `golangci-lint-v2.12.2`, project config | 0 issues |
| Windows | `gofmt -l` on the changed packages | clean |
| Windows | `terrariabonker compendium --refresh` (live) | real stats, exit 0 |

**New / changed tests**
- `content_test.go`: the four `FindItemTemplates` calls pass the mono offsets
  (built the same way production builds them from the entry); the template
  picking, clustering and tie-breaking assertions are unchanged, so the refactor
  is proven to leave mono behaviour identical.
- `cli/runtime_test.go` (`TestUnderTheCLRThePlayerAndInventoryAreRead`): the
  compendium now runs under the CLR entry and emits its `"items"` JSON instead of
  being refused.
- `table_test.go`: the frozen digest re-pinned for the new `Content` block and
  `ReadContent` read (deliberate, after review).

**Mutation checks** — all killed:
- netfx `Content.CreateTile`/`HealLife`/slot offsets left at mono's → the live
  catalog reads wrong stats (Dirt Block tile != 0, Healing Potion heal != 100);
  caught by the live check below.
- netfx `Content.NPCStats` set true → the NPC scan runs with mono statics on the
  CLR and finds nothing or garbage; left false, NPCs are names only by design.
- `ReadContent` dropped from the netfx reads → `gameReading(ReadContent)` refuses
  the compendium (the pre-slice behaviour), `runtime_test` fails.

## Phase 4: Code quality

The scan is now offset-driven on the item side, matching how inventory,
projectiles and tiles already take their numbers from the entry. No duplication:
the offsets are built once by `ItemOffsetsFrom`. The dead `gameReadingAll` and its
"partly read" message were removed rather than left unused.

## Phase 5: Security review

- No hardcoded secrets; none added.
- No new subprocess or network calls. The scan reads the game's own memory the
  process already has open; it writes nothing.
- The catalog is a read: a wrong offset reads wrong stats, never a write. Bounds
  are checked (`off+span <= len(buf)`) before any field is read.

## Phase 5.5: Release safety

- **Change type**: code (a read path) plus additive version-table data.
- **Pattern**: additive — a new feature, a new entry block, a new read. The item
  scan's signature changed, but its only callers moved with it.
- **Rollback**: `patch`-free; this is a read. Revert the commit and rebuild.
  Minutes, no data heroics. The catalog is also off until a player opens it.
- **Rollout**: the compendium is a read a player invokes; nothing auto-runs it.

## Live confirmation (Windows, 2026-10-10)

`terrariabonker compendium --refresh` against the running game (netfx-4.8.9345.0,
pid 8836) returned 6195 items, 629 with damage, reading correct base stats:
Iron Broadsword 12, Wooden Sword 7, Iron Pickaxe 5 (Tool). The six new offsets
each read a known value: Healing Potion heal_life 100, Dirt Block createTile 0,
Stone Block 1, Shadow Helmet/Scalemail/Greaves head/body/leg slot 5 at defense
6/7/6, each classified Armor. NPCs: 759 listed by name, no stats (as designed).

## Overall

- All gates passed: YES
- Notes: item catalog works under .NET Framework; NPC template stats on the CLR
  are the next slice (the NPC array found player-relative).
