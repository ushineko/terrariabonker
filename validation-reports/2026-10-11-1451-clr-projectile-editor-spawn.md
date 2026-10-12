# Validation Report — the projectile editor and spawn-npc on the CLR (spec 052, slices 22-23)

**Date**: 2026-10-11 14:51
**Commit**: uncommitted (working tree on `main` at a5364c6)
**Status**: PASSED headless; the two live *writes* (a projectile edit, an NPC
spawn) are not yet made on Windows -- see "Live confirmation".
**Scope**: under .NET Framework `projectile-tick` / `projectile-stop` /
`projectile-of` and `spawn-npc` run. Each sat behind the every-write gate and
took mono's numbers before.

## What changed

- `internal/layout`: `ProjectileShape` gains the seven fields the editor reads and
  writes; a new `SpawnFields` block (`Entry.Spawn`); `ItemFields.Shoot`; two write
  features (`WriteProjectiles`, `WriteNPCSpawn`), both allowed on the CLR entry.
- `internal/projectile`: `Editor.Sweep` takes the entry. `Field` no longer carries
  an offset; `offsetOf` maps a field's name to the shape's number.
- `internal/service`: `SpawnNPC` and its helpers take everything from the entry;
  `ProjectileTick` passes the entry to the sweep; `ProjectileOf` reads
  `Item.Shoot` from the entry.
- `internal/cli`: `spawn-npc` gates on `WriteNPCSpawn`; the three projectile
  commands on `WriteProjectiles`.
- **Behaviour change on mono**: a spawn's velocity clear is eight bytes and comes
  before the oldPosition write, where it was sixteen bytes after it. The sixteen
  reached oldPosition, so a spawned NPC started with oldPosition (0, 0). It now
  starts with oldPosition equal to its position. Not watched on Linux.

## Measurements (Windows, live game, 2026-10-11)

Read with a throwaway read-only program, deleted afterwards; nothing was written.

| What | Result |
| --- | --- |
| NPC MethodTable BaseSize | 0x210, so the object ends at 0x20C |
| `Entity.whoAmI` 0x004 | equal to the slot index in 201 of 201 `Main.npc` slots |
| NPC 0x050..0x087 | heap pointers or null in slots 0, 50 and 199; 0x040..0x04F and 0x088 on are not pointers |
| position 0x020, velocity 0x028, oldPosition 0x030 | in six active NPCs, position less velocity is oldPosition |
| the player's `Entity.direction` 0x00C | 1; position (33912.5, 4870.0) |
| Projectile MethodTable BaseSize | 0x140 |
| the one projectile in flight | type 211, penetrate -1, maxPenetrate -1, extraUpdates 0, scale 1.00 |
| the whole CLR table | `go run ./cmd/clrfields --verify`: every field agrees, exit 0 |
| `Item.shoot` 0x0BC | `projectile-of`: Terra Blade 985, Stardust Dragon Staff 625, Uzi 14, Solar Eruption 611, Golden Fishing Rod 364 |

The byte at tileCollide (0x112) read 0 on the one projectile in flight, a pet;
that is consistent with the field and does not establish it. The offset is the
runtime's own metadata.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -race ./...` | 27 packages `ok` on the final run |
| Windows | `golangci-lint-v2.12.2`, project config, `GOTOOLCHAIN=go1.26.0` | 0 issues |
| Windows | `gofmt -l cmd internal` | clean |

`TestATickRunsSeveralRoundsAndStopsOnAResult` failed once on an earlier full
`-race` run, as it did in the previous report: it expects several rounds inside
50 ms. It passed on the package rerun and on the final full run; untouched.

**New tests** (literal offsets from the measurement):
- `layout`: `TestTheCLRSpawnFieldsAreTheTable`;
  `TestTheCLRSpawnSpansHoldNoReference` -- against the measured Entity and NPC
  layouts, no reference is in a span, whoAmI and the four Vector2s are outside
  every span, every other instance field is inside one, and the last span ends at
  BaseSize less the sync block; the projectile shape test covers the seven new
  fields by name; `Item.shoot`. The version-table digest changed with the table.
- `projectile`: a CLR sweep writes each field at its offset and width, with the
  bytes either side of tileCollide checked; an inactive projectile is not
  written; the lifetime is applied once per object; each entry's numbers write
  nothing into the other runtime's array.
- `service`: a CLR spawn compared byte for byte -- template bytes in the spans,
  the slot's own bytes in the header, references and oldVelocity; the side it
  lands on; active written last; no template and no free slot; a template ending
  with the readable memory; a CLR projectile tick; `ProjectileOf`. The mono spawn
  test now checks oldPosition.
- `cli`: the four commands pass the gate under netfx; the debug `write` is the
  command still refused.

**Mutation checks** -- 48 mutants against the behaviour tests alone (the layout
package's table tests left out, so a survivor means the behaviour tests are
weak):
- 43 killed on the first run.
- 2 did not build as written; rewritten, both killed.
- 2 survived because a test was weak, and the tests were strengthened until they
  were killed: the sweep reading the array's elements from mono's +0x10 (no
  projectile was planted in the first two slots), and the template scan asking
  for mono's object size (no template was planted near the end of memory; the
  first fix used an NPC the fixture already had a second template of, and the
  mutant survived again until that was changed).
- 3 are equivalent in behaviour: the first span widened into position, the first
  span started at whoAmI, and the velocity clear widened to sixteen bytes. In
  each the extra bytes are overwritten by a later write of the same spawn. The
  first two are killed by `TestTheCLRSpawnSpansHoldNoReference`; the third leaves
  the same bytes.

## Phase 4: Code quality

- No shared routine branches on the runtime: the sweep and the spawn read the
  entry's numbers. The spawn's former `layout.Mono()` calls and the
  service-local `playerDirection` constant are gone.
- `clr_test.go`'s parse of the measurement file was needed a second time, so it
  became `measuredInstanceFields`, used by the Item span test and the new NPC one.
- Not changed: a spawn leaves the slot's reference arrays (ai, buffs) as the last
  NPC in that slot left them. This is the existing behaviour under mono.

## Phase 5: Security review

- No hardcoded secrets, no new subprocess, no new network call, no new
  dependency (`govulncheck` not re-run: `go.mod` is unchanged).
- A CLR spawn copies no reference and stops at the object's end, both checked
  against the measurement. `Main.npc` is believed only at the game's length with
  elements sharing a MethodTable (unchanged from slice 19).
- The editor's field set is unchanged: the five fields it could already write.

## Phase 5.5: Release safety

- **Change type**: code and version-table data.
- **Pattern**: additive -- two new write features on the CLR entry. One behaviour
  change on mono, described above.
- **Rollback**: revert the commit and rebuild, or drop `WriteProjectiles` /
  `WriteNPCSpawn` from the CLR entry to refuse those commands again. Nothing
  either command writes outlives the projectile or the NPC it was written to.
- **Rollout**: nothing runs on its own.

## Live confirmation (Windows, 2026-10-11)

Done, reads only: everything in "Measurements"; `projectile-stop` ran through the
gate; the debug `write` was refused as read-only.

## Addendum, 15:40 -- the first live spawn found a bug

The maintainer asked for the live writes. `spawn-npc 443` (commit 199c1d5) was
refused: "no template for NPC 443 (Gold Bunny)". Nothing was written.

- **Cause, measured**: the spawn took the inactive object with the net id asked
  for as the template. A read-only scan of the live game found 2405 NPC objects
  outside `Main.npc` with a plausible type; 2402 read active at 0x18C, every copy
  of net ids 1, 17, 50 and 443 among them. The compendium, which does not ask for
  inactive, had a Gold Bunny template (life 5, 18 x 20).
- **Why the tests passed**: the CLR fixture's template was planted with active 0.
  That byte was an assumption, not a measurement, and the report above called
  the suite sufficient. It was not.
- **Fix**: the template is the object the compendium's table holds for the net id
  (`content.NPCTemplateAddrs`: not in `Main.npc`, one per net id). It is re-read
  and checked for the NPC class and the net id before use. The copy's active
  byte is cleared, because 0x18C is inside the last copy span and the template's
  is 1 -- copied as it was, the slot would have gone live before its position was
  written.
- **Live, after the fix, reads only**: the scan chose 762 templates; 761 read
  active; whoAmI was 0 in all; the Gold Bunny's was at the low end of the range
  with the Blue Slime's, Merchant's and King Slime's. One read as a bestiary icon
  dummy, which was not looked into.
- **Tests added**: the fixture's templates are active; the slot's active byte is
  read after every write and is zero until the last; a live NPC sharing the net
  id is not taken for the template; a template that changed between the scan and
  the read (another class, another net id, cut short) is refused with nothing
  written.
- **Mutation checks**, 5 more: the active byte copied, the live NPCs not excluded,
  and each of the three re-read checks removed. All killed; the three re-read
  mutants survived until the last test above was written.
- `go test -race ./...`: 27 packages `ok`. Lint: 0 issues.

**The live spawn**: `spawn-npc 443` then reported slot 33 at tile (1818, 226).
Read back from the game: slot 33 active, type and net id 443, life 5/5, whoAmI
33, position (29089.6, 3622.0) with the player at (29489.6, 3622.0) -- 400 pixels,
25 tiles -- velocity zero, not a bestiary dummy. Whether it is on screen and
behaves as a Gold Bunny is the maintainer's to say.

**The live projectile edit**: see the spec; not confirmed at the time of writing.

## Overall

- All gates passed: YES headless, after the fix in the addendum; the live spawn
  was made and read back; the live projectile edit is pending
- Notes: still behind the every-write gate are `extract-recipes`,
  `freeze`/`godmode`, `extract-tick`/`extract-stop` and the debug `write`.
