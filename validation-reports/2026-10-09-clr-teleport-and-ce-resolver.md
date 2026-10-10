# Validation Report — Cheat Engine as a scriptable .NET resolver; map-ping teleport

**Date**: 2026-10-09
**Commit**: uncommitted (working tree on `main` at 8339dc4)
**Status**: PASSED
**Scope**: spec 052, slice 12.

- Installed Cheat Engine 7.7 (Chocolatey) and scripted it headlessly to resolve CLR
  methods and statics by name. This replaces blind AOB searching for the remaining
  cheats.
- Added map-ping teleport to the .NET Framework injection set.

## Cheat Engine, scripted

CE's `DotNetDataCollector` (the `getDotNetDataCollector` Lua API: `EnumDomains` →
`EnumModuleList` → `EnumTypeDefs` → `GetTypeDefMethods` / `GetTypeDefData`) gives each
managed method's JIT `NativeCode` and each static field's runtime `Address` by name. A
Lua dropped in CE's `autorun/` attaches to Terraria, logs these, and closes CE — the
Windows counterpart of the Linux `ce/ce-terraria.sh` spike.

One run resolved `Main.TriggerPing`, `Player.Teleport`, `Player.PickTile`,
`WorldGen.KillTile`, `Player.UpdateEquips`, `GetItemGrabRange`, `ApplyEquipFunctional`,
`GrantPrefixBenefits`, `GrantArmorBenefits`, and the `Main` statics. It confirmed the
static-layout research: reference-type statics (`player`, `tile`, `PylonSystem`) and
primitive/value statics (`mapFullscreen`, `mapFullscreenPos`) live at different bases —
why the earlier single-base guess was wrong.

## Teleport

- **Hook:** `Main.TriggerPing` at offset 3, after `push ebp; mov ebp,esp`, where the
  ping's Vector2 is readable at `[ebp+0C]/[ebp+10]`. The six displaced prologue bytes
  (`push esi; sub esp,0C; xor eax,eax`) are wildcarded in the anchor so a cold
  re-resolve still matches once the hook's jump is in — the same rule the mono anchors
  follow.
- **Body:** calls `Player.Teleport(newPos, Style=0, extraInfo=0)` (entry = the
  `player_teleport` anchor less 0x21), instance in ecx, Style in edx, `newPos`/`extraInfo`
  on the stack. The warp therefore gets the game's sound and dust, matching Linux.
- **Coordinates:** `TriggerPing` delivers tiles; `Teleport` wants world pixels at 16 per
  tile, so each coordinate is scaled ×16 via `f32Times16`.
- **GC:** this is the one CLR cheat that calls a managed method from the arena. It is
  GC-unsafe in principle (no transition frame), but fires once per ping — a rare user
  action — so the exposure is tiny and the worst case is a crash, not save corruption.
  The ore extractor, which would call per tile continuously, will not take this path.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | all packages `ok` |
| Linux (njv-cachyos): b77dfdb plus every change since | `make test` | 27 packages `ok` |
| Windows, live game | `build-check` | teleport resolves to one site |
| Windows, live game | enable, place a map ping, disable | the maintainer confirmed warping onto the ping with sound and dust; after disabling the hook read back as the original prologue |

Two coordinate-convention bugs were found in play and fixed: first the tile→pixel ×16
scale (player landed at the top-left corner), then the `newPos` stack slot (`Teleport`
reads it at `[ebp+0C]`, one slot above where the first version pushed it).

**New tests**

- `TestTeleportCallBody` pins the exact stub bytes: ×16 scaling, the push order,
  Style in edx, the player and Teleport-entry immediates, the esp save/restore, and the
  reproduced prologue. No player is an error, not a silent no-op.
- `TestUnderTheCLRTeleportIsInstalled` plants `TriggerPing` and the `Player.Teleport`
  anchor on the synthetic image, enables teleport, and checks the hook jumps to a stub
  whose call target is the anchor less 0x21; disabling restores the prologue. This
  caught the wildcard bug (the anchor failed to re-resolve after the hook went in).

**Mutation checks** — all killed:
- the Teleport entry offset (−0x21 → −0x20);
- the hook offset (3 → 4);
- the push order of newPos.X/newPos.Y;
- the ×16 scale turned into a subtract.

## Phase 4: Code quality

- Lint 0 issues natively, for `GOOS=windows`, and on Linux (`make lint`).
- The CE discovery Lua and the temporary disassembly tools live in the session
  scratchpad, not the repo.
- Status: PASSED.

## Phase 5: Security review

- No dependency changes in the module. Cheat Engine is a developer tool on the
  maintainer's machine, not a project dependency or anything shipped.
- CE's collector and our reads are read-only resolution; the teleport write path is the
  same arena mechanism as the other stubs.
- Status: PASSED.

## Phase 5.5: Release safety

- **Linux:** unchanged; teleport there keeps its own mono stub.
- **Windows:** gains teleport. The one managed-call cheat; crash-not-corruption risk,
  documented.
- **Rollback:** revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped.
- **Note:** `.tag` is unchanged; this is the maintainer's call. A fair amount of the
  Windows port (eleven of fifteen cheats, now twelve with teleport) has landed without a
  version bump — worth deciding when to cut one.
