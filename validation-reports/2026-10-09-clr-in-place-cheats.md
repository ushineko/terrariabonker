# Validation Report — the remaining in-place cheats on the CLR, and a site guard

**Date**: 2026-10-09
**Commit**: uncommitted (working tree on `main` at 638aa2e)
**Status**: PASSED
**Scope**: spec 052, slice 8.

- max_minions, fast_place and pylons now have .NET Framework sites. With mining and
  reach, all five in-place cheats work on Windows.
- Turning a cheat on or off now refuses to write unless the site holds the original
  bytes or the cheat's own patch. This applies to both runtimes.

## How the sites were found

The work was done read-only against the live game, using `winrecon -aob`, `clrfields`,
and a temporary disassembler that has since been deleted. The disassembler was built from
the x86 decoder vendored in the Go toolchain.

| Cheat | Method | Found by | Site |
| --- | --- | --- | --- |
| max_minions | `Player.ResetEffects` | the store `maxMinions = 1` at its CLR offset 0x30C, followed by `maxTurrets = 1` (0x5A8) | the 4-byte immediate |
| fast_place | `Player.ApplyItemTime(Item, float)` | the instruction shapes `cvttsd2si` then `test`/`jle`; this one loads `Item.useTime` (+0x60) | the 15-byte clamp, replaced by `mov eax, N` and ten nops |
| pylons | `TETeleportationPylon.PlacementPreviewHook_CheckIfCanPlace` | code that loads `Main.PylonSystem`'s static slot (0x48 past `Main.player`'s, from `clrfields Terraria.Main`) | the first 5 bytes, replaced by `xor eax, eax; ret 0x10` |

Each anchor matched exactly once in the live game.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | all packages `ok` |
| Linux (njv-cachyos): a fresh checkout of 638aa2e plus this tree | `make test` | 27 packages `ok` |
| Windows, live game | `build-check` | fast_place, max_minions, mining, pylons and reach each resolve to one site |
| Windows, live game | each cheat enabled, then disabled | the maintainer confirmed each in play: a minion cap of 10, faster placement, and a second pylon of a type already placed. After disabling, every site read back as its original bytes |

**New tests**

- `TestUnderTheCLRMinionsAndPlacementArePatched` and `TestUnderTheCLRPylonsArePatched`:
  on the synthetic image, the cheat's bytes are written with the value encoded, and
  disabling restores the code exactly.
- `TestUnderTheCLRAnUnexpectedSiteIsNotWritten`: an anchor matches, but the site holds a
  different instruction. Both enable and disable are refused, with the image unchanged.
- `TestEveryEncoderFillsItsSiteExactly` and `TestASiteHoldsItsOriginalOrItsPatch`.

**Mutation checks**

Every mutant was killed:
- the guard removed from enable;
- the guard removed from disable;
- the shape check off;
- the fixed-patch check accepting anything;
- the new encoder one byte short;
- fast_place's patch offset moved by one.

## Phase 4: Code quality

- `make lint` on Linux: 0 issues, after clearing a stale lint cache.
- Native and `GOOS=windows` lint on Windows: 0 issues.
- The temporary tools (`zdis`, `zslot`) were deleted from the tree.
- Status: PASSED.

## Phase 5: Security review

- No dependency changes.
- No network calls or subprocesses added.
- The guard closes a mis-write path. Before this change, a cheat's anchor matching
  unexpected code would have overwritten the bytes the anchor wildcards.
- Status: PASSED.

## Phase 5.5: Release safety

- **Linux:** behaviour is unchanged where the sites hold what they should. The only
  difference is a refusal where they don't.
- **Windows:** gains three cheats.
- **Rollback:** revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped.
