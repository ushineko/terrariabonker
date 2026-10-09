# Validation Report — tool reach and the smart cursor clamp on the CLR

**Date**: 2026-10-09
**Commit**: uncommitted (working tree on `main` at fe4c4e0)
**Status**: PASSED
**Scope**: spec 052, slice 10.

- **tool_reach and smart_cursor** are added to the .NET Framework injection set.
- **The smart cursor body is now shared.** It is generalised over the register and field
  offsets, and both sets use it. Mono's bytes are unchanged.

## How the hooks were found

The work was read-only against the live game, using `winrecon -aob`, `clrfields`, and
temporary tools.

| Cheat | Method | Found by | Hook | Body |
| --- | --- | --- | --- | --- |
| tool_reach | `TileReachCheckSettings.GetRanges` | `GetTileRegion` (an indirect call reached from placement code), which calls it directly | its exit, `lea esp,[ebp-0C]; pop ebx; pop esi`, where edi holds &x and [ebp+8] holds &y | `mov [edi],N; mov eax,[ebp+8]; mov [eax],N`, then the displaced bytes |
| smart_cursor | `SmartCursorHelper.SmartCursorLookup` | the one place that passes `SmartCursorUsageInfo`'s four box fields (CLR 0x14, 0x1C, 0x18, 0x20) to `GetTileRegion` | after the world-edge clamps: `mov [ebx+20],eax` and `cmp dword [ebp-3C],0` | mono's clamp, on ebx with CLR offsets. The store is replayed first; the compare is replayed last, for the `jz` that follows |

**Checks at each hook**

- **Jumps:** every byte position in the surrounding code was read as a possible jump.
  None lands inside the displaced bytes, except at their first byte.
- **Registers:** at the smart cursor hook, both paths that follow reload eax and ecx
  before using them.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | all packages `ok` |
| Linux (njv-cachyos): b77dfdb plus every change since it | `make test` | 27 packages `ok` |
| Windows, live game | `build-check` | tool_reach and smart_cursor resolve to one site each |
| Windows, live game | enable tool_reach 30 and smart_cursor 20 | the maintainer confirmed both in play |
| Windows, live game | disable both | both sites read back as their original instructions |

**New tests**

- `TestTheSmartCursorBodies` pins mono's output: a digest over eight values, recorded
  from the code before the refactor. It also checks the CLR body's length, its first
  bytes and its last bytes.
- `TestUnderTheCLRTheStubsAreInstalled` was extended:
  - tool_reach's body, and its jump home past the displaced bytes;
  - the smart cursor stub, against a body the test builds from the instruction encoding;
  - both sites restored on disable.

**Mutation checks**

Every mutant was killed:
- y written through the wrong argument;
- the CLR smart cursor on the wrong register;
- its Y edges swapped;
- the shared clamp's eax and ecx encodings swapped.

## Phase 4: Code quality

- Lint: 0 issues natively on Windows and for `GOOS=windows`. `make lint` on Linux: 0
  issues.
- **Duplication removed:** the smart cursor clamp exists once.
- Status: PASSED.

## Phase 5: Security review

- No dependency changes.
- No new memory permissions or calls.
- Status: PASSED.

## Phase 5.5: Release safety

- **Linux:** unchanged. Mono's smart cursor bytes are pinned by the digest.
- **Rollback:** revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped.
