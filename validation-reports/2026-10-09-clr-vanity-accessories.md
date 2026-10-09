# Validation Report — vanity accessories on the CLR; edits checked before any write

**Date**: 2026-10-09
**Commit**: uncommitted (working tree on `main` at 21e3d8b)
**Status**: PASSED
**Scope**: spec 052, slice 11.

- **vanity_accs** is added to the .NET Framework injection set.
- **A cheat's byte edits are now checked before anything is written.** This covers the
  loop bounds it widens, on enable and on disable, under every entry. Before, they were
  written unchecked, and only after the stub and its jump were already in place.

## How the hooks were found

`UpdateEquips` was reached in three steps:
1. `GrantPrefixBenefits` was found as the only code that compares an item's `prefix`
   (CLR Item +0x12E) with 62.
2. Its address is stored in one call slot (0x090677D8), and the slot has one caller:
   `UpdateEquips`' benefit loop.
3. `UpdateEquips`' effects loop follows the benefit loop.

| Piece | Where | Change |
| --- | --- | --- |
| hook | the effects loop, before `ApplyEquipFunctional`: `push eax; mov edx, ebx; mov ecx, esi` | the same three instructions, with the slot clamped in edx (13..19 becomes 3..9) |
| edit | the effects loop's `k < 10` | 10 becomes 20 |
| edit | the benefit loop's `k < 10` | 10 becomes 20 |

No jump lands inside the hook's bytes. Both anchors are unique.

## Not ported yet

inventory_accs, teleport and ore_extract are not ported. Their mono stubs call managed
methods from this program's arena.

The CLR's garbage collector walks thread stacks precisely. A managed frame whose return
address lies outside managed code ends the walk at that point. That would leave the GC
roots of the frames above it (`UpdateEquips`, `Player.Update`, and so on) unreported:
objects the game still uses could be moved or collected.

Mono scans such frames conservatively, which is why these stubs are safe there. Every CLR
stub ported so far runs inline and calls nothing. These three need a design in which
managed code makes the calls.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | all packages `ok` |
| Linux (njv-cachyos): b77dfdb plus every change since it | `make test` | 27 packages `ok` |
| Windows, live game | enable vanity_accs | the maintainer confirmed accessories in vanity slots take effect. The game had been restarted for an unrelated reason; the cheat was re-resolved and re-applied in the new process |
| Windows, live game | disable | both loops matched their fully literal original bytes, including the hook and both bounds |

**New tests**

- `TestUnderTheCLRTheStubsAreInstalled` was extended with vanity_accs:
  - the clamp body, and its jump back to the call;
  - both bounds set to 20;
  - both loops restored on disable.
- `TestUnderTheCLRAnUnexpectedEditRefusesTheWholeCheat`: when a bound holds neither 10
  nor 20, enabling is refused and nothing is written anywhere in the image.

**Mutation checks**

Every mutant was killed:
- the early edit check removed;
- the edit check turned off;
- the clamp on eax instead of edx;
- the benefit-loop edit one byte off.

## Phase 4: Code quality

- Lint: 0 issues natively on Windows and for `GOOS=windows`. `make lint` on Linux: 0
  issues.
- Status: PASSED.

## Phase 5: Security review

- No dependency changes.
- The edit check closes a partial-apply path. Before, an edit refused mid-way left a stub
  installed.
- Status: PASSED.

## Phase 5.5: Release safety

- **Linux:** mono's vanity edits are now checked before they are written. This changes
  nothing where the bytes are as expected.
- **Rollback:** revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped.
