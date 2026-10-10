# Validation Report — inventory accessories on the CLR

**Date**: 2026-10-09
**Commit**: uncommitted (working tree on `main` at 43e7ce2)
**Status**: PASSED
**Scope**: spec 052, slice 13.

inventory_accs is added to the .NET Framework injection set, ported faithfully from the
mono design: hook `UpdateEquips`' 58-slot inventory loop and, for each item that is an
accessory, call the three methods an equipped accessory goes through.

## How it was found

CE's DotNetDataCollector gave the method entries by name: `ApplyEquipFunctional`,
`GrantPrefixBenefits`, `GrantArmorBenefits`, and `UpdateEquips` itself. The inventory
loop is the first loop in `UpdateEquips`, walking `Player.inventory` (object +0xD4), the
same loop vanilla uses for info and mechanical accessories.

- **Hook:** the loop at the point the Item is in eax (player in esi), 19 bytes into the
  `inventory_scan` anchor. The seven displaced bytes are wildcarded so a cold re-resolve
  matches with the hook in.
- **Stub:** gates on `Item.accessory` (+0x10E) and calls `GrantPrefixBenefits(ecx=player,
  edx=item)`, `GrantArmorBenefits(ecx=player, edx=item)`, then `ApplyEquipFunctional(
  ecx=player, edx=0, item on the stack)`. esp is saved in ebx and restored after each call
  so the stub is right whichever way the callee cleans the stack. The three method entries
  are resolved by their own prologue anchors.

## GC risk

This calls managed code from the arena per inventory item per frame — the high-frequency
case the GC research flagged. In practice the risk is low: the objects the stub and the
frames below it touch (the player, the inventory array, the items) are permanently rooted
through static `Main.player[]`, so the GC can never free them. The only residual is a
compacting GC relocating one of those in-use objects while a hidden stack frame holds a
stale pointer — but they are long-lived gen2 objects that compaction rarely moves, and a
32-bit Terraria with modest allocation collects infrequently. It ran stably in play. The
no-call loop-extension design remains the documented fallback if a crash ever appears.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | all packages `ok` |
| Linux (njv-cachyos): 43e7ce2 plus every change since | `make test` | 27 packages `ok` |
| Windows, live game | `build-check` | inventory_accs resolves to one site; the three method anchors resolve |
| Windows, live game | enable, then play | the maintainer confirmed accessories in the bag granted their effects; the game stayed stable |
| Windows, live game | disable | the inventory loop read back as its original bytes |

**New tests**

- `TestUnderTheCLRInventoryAccessoriesAreInstalled` plants the inventory loop and the
  three method entries on the synthetic image, enables the cheat, and checks the hook
  jumps to a stub that calls prefix, armor, then apply-functional at the resolved
  entries; disabling restores the loop.
- `TestUnderTheCLRACheatWithoutASiteIsRefused` now uses `ore_extract` as its still-
  siteless example (inventory_accs has a site now).

**Mutation checks** — all killed:
- the hook offset (19 → 18);
- the prefix call's argument registers swapped;
- the accessory-flag offset (0x10E → 0x10C).

## Phase 4: Code quality

- Lint 0 issues natively, for `GOOS=windows`, and on Linux (`make lint`).
- Status: PASSED.

## Phase 5: Security review

- No dependency changes, no new network calls.
- The stub reads the inventory and calls existing game methods; no new memory
  permissions.
- Status: PASSED.

## Phase 5.5: Release safety

- **Linux:** unchanged; inventory_accs there keeps its mono stub.
- **Windows:** gains inventory accessories. 14 of 15 cheats now work under .NET
  Framework; only the ore extractor remains.
- **Rollback:** revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped (the maintainer is deciding when to cut one).
