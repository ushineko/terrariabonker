# Validation Report — the CLR injection set: pickup, spawn rate, drop floor

**Date**: 2026-10-09
**Commit**: uncommitted (working tree on `main` at b77dfdb)
**Status**: PASSED
**Scope**: spec 052, slice 9.

- **Arena allocation is now a module.** The mono set uses the springboard. The .NET
  Framework set uses `allocate`, which calls `VirtualAllocEx` directly.
- **New .NET Framework stub module** (`injections_netfx.go`) with pickup, spawn_rate and
  loot.
- **`proc.Mem.AllocateAt`** added on Windows.

## How the hooks were found

The work was read-only against the live game, using `winrecon -aob`, `clrfields`,
temporary tools that were deleted afterwards, and the x86 decoder vendored in the Go
toolchain.

| Cheat | Method | Found by | Hook | Body |
| --- | --- | --- | --- | --- |
| pickup | `Player.GetItemGrabRange` | the only reads of `goldRing` (0x91C), next to the load of `defaultItemGrabRange` | `mov eax, edi; pop ebx; pop esi; pop edi` at the exit | `imul edi, edi, N`, then the displaced bytes |
| spawn_rate | `GetSpawnRate` | the only reads of `calmed` (0x72F) and `enemySpawns` (0x959), both in one method | `lea esp,[ebp-0C]; pop ebx; pop esi` at the exit | mono's `ForceSpawn`, then the displaced bytes |
| loot | `TryDroppingItem` and 3 twins | the compare `cmp eax, [esi+18]` (`chanceNumerator`) right after a call, 4 matches | `mov esi, ecx; mov edx, [esi+0C]` | the same two instructions, with the denominator in edx capped |

**Check for jumps into each hook.** For every hook, every byte position in the
surrounding code was read as a possible relative jump or call. None lands inside the
displaced bytes. The only jumps found land on the first displaced byte, which the
hook's own jump replaces.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | all packages `ok` |
| Linux (njv-cachyos): a fresh checkout of b77dfdb plus this tree | `make test` | 27 packages `ok` |
| Windows, live game | `build-check` | pickup 1 site, spawn_rate 1, loot 4 |
| Windows, live game | enable pickup 10, loot 100, spawn_rate 30 | the arena was allocated with no frames needed. The maintainer confirmed all three in play |
| Windows, live game | disable all three | every site read back as its original instruction, including all four loot twins |

**New tests**

- `TestAllocateAtMapsExecutableMemoryWhereAsked` (`internal/proc`, Windows): uses a real
  process, the test's own, at a free low address. It checks:
  - the new region is writable and executable;
  - a write to it lands;
  - allocating over an existing mapping fails.
- `TestUnderTheCLRTheStubsAreInstalled` (`internal/cli`): uses the synthetic image with
  an allocating fake. It checks:
  - exactly one arena is allocated;
  - each site jumps to a stub whose body is the CLR body, and the stub jumps home past
    the displaced bytes;
  - both loot twins are hooked;
  - disabling restores every site.

**Mutation checks**

Every mutant was killed:
- the CLR set using the springboard;
- `imul` on the wrong register;
- the spawn hook one byte early;
- loot hooking only one twin;
- the cap's `jle` turned into `jge`;
- the allocation landing elsewhere than the address chosen.

## Phase 4: Code quality

- Lint: 0 issues natively on Windows and for `GOOS=windows`. `make lint` on Linux: 0
  issues.
- The temporary tools were removed from the tree.
- Status: PASSED.

## Phase 5: Security review

- No dependency changes, and no network calls.
- `VirtualAllocEx` goes through the existing process handle, which already has
  `PROCESS_VM_OPERATION`.
- The new memory is read-write-execute. That is what an arena is on Linux too: it holds
  stubs and their data.
- Status: PASSED.

## Phase 5.5: Release safety

- **Linux:** unchanged. The mono set names the springboard, and the springboard's error
  text moved with it.
- **Windows:** gains three cheats.
- **Rollback:** revert the commit. An arena already allocated in a running game is
  harmless and is reused by its stamp.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped.
