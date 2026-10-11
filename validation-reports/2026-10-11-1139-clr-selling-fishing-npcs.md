# Validation Report — selling, fishing gear and NPC stats on the CLR (spec 052, slices 19-21)

**Date**: 2026-10-11 11:39
**Commit**: uncommitted (working tree on `main` at ede89d1)
**Status**: PASSED headless; the two live *writes* (a sale, a bait top-up / rod
power change) are not yet made on Windows — see "Live confirmation".
**Scope**: under .NET Framework the compendium reads NPC stats, `sell` /
`sell-tick` run, and `fishing` runs. Each took mono's numbers or sat behind the
every-write gate before.

## What changed

- `internal/layout`: `NPCShape` and `SellFields` on the entry, two statics
  distances (`NPCFrameCountFromPlayer`, `WorldNameFromPlayer`), two write
  features (`WriteFishingGear`, `WriteSelling`). `ContentFields.NPCStats` (an
  on/off flag) is gone: an entry with no NPC resolver has no NPC stats.
  `CLRFields` gains `Item.value` and `Chest.item`.
- `internal/content`: the NPC array and the frame-count table are found by a
  resolver named by the entry ("mono", "clr"); the template reader takes the
  entry's NPC offsets.
- `internal/selling`: `Container` and `Bank` take an entry's numbers
  (`ShapeOf`); a slot is read field by field.
- `internal/service`: the compendium, the live-NPC exclusion and selling pass the
  entry through; `WorldID` reads `Main.worldName` under the CLR.
- `internal/cli`: `fishing` gates on `WriteFishingGear`; `sell` and `sell-tick` on
  `WriteSelling`.
- `docs/clr-fields-1.4.5.8.txt`: the `Terraria.Chest` measurement appended.

## Measurements (Windows, live game, 2026-10-11)

| What | How | Result |
| --- | --- | --- |
| `Chest.item` | `go run ./cmd/clrfields Terraria.Chest` | `inst 0x00004`, isStatic agrees 17/17 |
| the whole CLR table | `go run ./cmd/clrfields --verify` | every field agrees with the runtime, exit 0 |
| `Item.value` 0x0D8 | read the carried coins | 5, 500, 50000, 5000000 — the documented five-times-copper values |
| `Player.bank` + `Chest.item` | read the Piggy Bank | a chest of 40: 77 platinum, 25 gold, 76 silver, 39 copper |
| `Item.favorited` 0x10C | read the inventory | true on the hotbar and the favorited potions, false elsewhere |
| `Main.worldName` slot | `status --json` | "Royal Brewery of Maggots", 8401 x 2401 |
| NPC offsets, `Main.npc` slot | `compendium --refresh` | 754 of 759 NPCs with stats; Blue Slime 25/7/2, Green Slime (-3) 14/6/0, Eye of Cthulhu 2800/15/12 boss, King Slime 2000/40/10, Wall of Flesh 8000, Empress of Light 70000, Merchant 250 town |
| `Main.npcFrameCount` slot | the published `npcframes.json` | 697 counts: slime 2, Eye of Cthulhu 6, Merchant 25, Guide 26; 11 tints |

The item and bank rows were read with a throwaway read-only program built on
`selling.Container`, deleted afterwards; nothing was written to the game.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -race ./...` | 27 packages `ok` on the second run; see the flake below |
| Windows | `golangci-lint-v2.12.2`, project config, `GOTOOLCHAIN=go1.26.0` | 0 issues |
| Windows | `gofmt -l cmd internal` | clean |

**One failure on the first `-race` run, not from this change**:
`TestATickRunsSeveralRoundsAndStopsOnAResult` (`veinwatch_test.go:204`) expects
more than one round inside a 50 ms budget and got one, with every package's tests
running in parallel under the race detector. It passed 15 of 15 runs alone and on
the package's rerun. It is a timing assumption in an existing test; untouched.

**New tests** (literal offsets from the measurement, not `internal/layout`'s):
- `layout`: `TestTheCLRNPCShapeIsTheTable`, `TestTheCLRSellFieldsAreTheTable`,
  `TestTheCLRFrameCountAndWorldNameSlotsAreTheTable` — the entry's numbers are
  `CLRFields`' by name, and `CLRFields` is the measurement file. Both frozen-table
  digests changed with the table.
- `content`: the CLR array found through the player slot; rejected at the wrong
  length, with unrelated elements, from the wrong slot, or with no resolver; the
  CLR frame counts; NPC templates read at the CLR offsets; and the mono pinned and
  fallback paths, which the refactor touched and nothing covered end to end.
- `selling`: the CLR bank read, the wrong-length chest, the checked write and the
  template block landing at 0x018, and each entry's numbers finding no bank in the
  other runtime's player.
- `service`: a CLR sale pays the bank then clears every copy's stack and leaves
  the favorited one; a dry run writes nothing; a chest of 39 is not the bank; a
  world change re-asks whether a bank is placed; bait top-up, rod power raise and
  restore, and the kit; the catalog's NPC stats and draw data.
- `cli`: `fishing`, `sell` and `sell-tick` run under netfx; `spawn-npc` is still
  refused (existing test).

**Mutation checks** — 23 mutants, all killed:
- CLR numbers moved by one field: `Item.value`, `Player.bank`, `Chest.item`,
  `NPC.lifeMax`, NPC size, the frame-count slot, the world-name slot.
- Runtime paths swapped: the world name read from mono's block; read as a mono
  string; the NPC base taken the mono way; the frame counts read from the NPC
  slot; mono's array offsets in the container; mono's copy start for the block.
- Checks removed: the live-NPC exclusion; the CLR array's length check; its
  shared-MethodTable check; favorited read from another field; mono's
  frame-count fallback scan.
- Gates reverted: `fishing`, `sell`, `sell-tick` back to the every-write gate; the
  two features dropped from the entry.

No equivalent mutants were found.

## Phase 4: Code quality

- The two ways to the NPC array are resolvers in a registry, as `tiles` and
  `projectile` do it; no shared routine branches on the runtime.
  `npcBase`/`worldNameSlot` follow `tileBase`: an entry with no distance from the
  player slot goes through the static block.
- `spawn-npc` uses mono's copy spans and field constants throughout, so it asks
  for the array with the mono entry explicitly and stays behind the every-write
  gate; a comment says so.
- Pre-existing, noticed, not changed: `SetFishingPower` writes the live copy only,
  where the bait top-up writes every copy of the live character.

## Phase 5: Security review

- No hardcoded secrets, no new subprocess, no new network call, no new
  dependency (`govulncheck` not re-run: `go.mod` is unchanged).
- Selling's safety properties are unchanged and now tested under the CLR: pay
  before take, the favorited override, and a write that re-resolves the slot and
  checks type and stack before touching it.
- A CLR slot is believed to hold `Main.npc` only at the game's length with
  elements sharing a MethodTable; the bank only at 40 slots. A wrong number reads
  nothing rather than something else.

## Phase 5.5: Release safety

- **Change type**: code and version-table data.
- **Pattern**: additive — two new write features on the CLR entry; mono's numbers
  are the same constants, checked by the mono halves of the table tests.
- **Rollback**: revert the commit and rebuild, or drop `WriteSelling` /
  `WriteFishingGear` from the CLR entry to refuse those two commands again. A
  completed sale is permanent once the world saves, as on Linux; that is the
  feature's nature, not this change's.
- **Rollout**: nothing runs on its own; selling needs a whitelist the player
  builds per item type.

## Live confirmation (Windows, 2026-10-11)

Done, reads only: everything in "Measurements"; `sell --dry-run` returned
`destination: bank`, `reachable: placed` at exit 0; `fishing` passed the gate and
reported "you already have a rod and bait" / "bait ok".

**Live sale, done later the same day** at the maintainer's request: Rain Coat
(1136) whitelisted; the dry run listed the one in slot 31 at 200 copper,
destination bank; `sell` reported it sold and 2 silver paid; the slot then read
empty and a second dry run found nothing to sell. The bank was not read back by
the tool; the maintainer confirmed the result in-game.

**Not done**: no bait or rod has been written on the CLR. Spec 052 keeps that box
open.

## Overall

- All gates passed: YES headless; live sale confirmed; live fishing-gear write pending
- Notes: the paths still behind the every-write gate are the projectile editor,
  `spawn-npc`, `extract-recipes`, `freeze`/`godmode` and `extract-tick`. Until
  the flip `build-check` reports `read_only` under the CLR.
