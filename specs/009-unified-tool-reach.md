# Spec 009: Unified tool/interaction reach (GetRanges code-cave injection)

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

The existing "reach" cheat only extends **placement** (it NOPs `blockRange`, which is
placement-only). Mining/tool use, chest/sign interaction, and crafting-station range
did not extend. Reverse-engineering (see `ce/REACH_FINDINGS.md`) showed 1.4.5.7
computes those through `TileReachCheckSettings.GetRanges`, which reads `tileRangeX`
but then **clamps** it — so a value-write to `tileRangeX` only moves placement. The
community fix (FearLess "ReGrind") forces the *output* of `GetRanges`, past the clamp.

Forcing the output needs to write more bytes than the site has room for, so this is
the trainer's first **code-cave injection**: a jump from the method into a run of
executable padding that runs a small stub and jumps back.

## Requirements

### Functional

1. A `tool_reach` code-patch cheat that forces the two outputs of the 2-output
   `GetRanges` to a tunable value, extending mining, tool use, chest/sign interaction,
   and crafting-station range together. Enable/disable/status via `patch`, and a GUI
   checkbox + value spinbox, like the other code patches.
2. Disable restores the original bytes exactly; a game restart clears it.

### Technical

3. The patcher gains **wildcard AOB support** (`Pattern` with a mask): the `GetRanges`
   anchor spans ASLR'd immediates (the mono type-init thunk and the `tileRangeX`
   static address), so the anchor is the fixed prologue + wildcarded reads, scanned by
   its longest fixed run and verified with the mask.
4. **Code-cave injection**: anchor `GetRanges` at its base, overwrite 5 bytes at the
   epilogue (`lea esp,[ebp-0C]; pop esi; pop edi`) with `jmp <cave>`. The cave stub is
   `mov [esi],N; mov [edi],N` (esi/edi are the out-param pointers, still live at the
   epilogue), then the 5 original bytes, then `jmp` back to `pop ebx`. The cave is a
   run of ≥ stub-size executable padding (int3 / zero) found in an exec region. State
   (inject addr, cave addr, stub length) is persisted per-pid for clean restore.
5. A single `PATCH_CATALOG` merges the value cheats and the injection so the CLI and
   GUI iterate one ordered list with uniform value specs.

## Risks & Assumptions

- **Live code patch.** The injection edits executing code; a wrong anchor could crash
  the game. Mitigated by a long, unique wildcard anchor (verified to resolve to the
  real `GetRanges`) and a byte-perfect, live-verified disable/restore.
- **Code cave reuse.** The stub is written into JIT padding; if the JIT reclaimed that
  padding the stub could be clobbered. Low risk for inter-method padding; a game
  restart clears everything and re-derivation is by AOB.
- **Build-specific.** Anchor/offsets are for 1.4.5.7; re-derive with the `ce/` probes
  after an update. `disable` restores exact bytes; `git revert` for rollback.

## Acceptance Criteria

- [x] `tool_reach` enable forces the `GetRanges` output (code-cave jmp + stub); disable restores exact bytes
- [x] Wildcard `Pattern` anchor resolves uniquely to the real `GetRanges` (0x22F8AE30 live)
- [x] `patch enable/disable/status tool_reach` (+ `--value`) work; GUI shows a checkbox + spinbox
- [x] Live-verified: mining, tool use, chest/sign interaction, and crafting-station range all extend; disable/re-enable is byte-perfect
- [x] `PATCH_CATALOG` drives CLI choices + GUI; parity test covers the `tool_reach` argv
- [x] 75 tests pass headless (new: injection round-trip, status set); lint clean

## Executive Summary

Adds unified tool/interaction reach — mining, tool use, chests, signs, and crafting
stations all extend together — by forcing the output of 1.4.5.7's
`TileReachCheckSettings.GetRanges` past its clamp. This is the trainer's first
code-cave injection (jump to a stub in executable padding), plus wildcard AOB support
so the anchor can span ASLR'd immediates. Reviewers: `patcher.py`
(`Pattern`/`_resolve`, `_find_cave`/`_enable_injection`) and `ce/REACH_FINDINGS.md`.

## Testing

`tests/test_patcher.py` covers the injection round-trip against a synthetic image
(jmp installed, cave stub bytes + forced value, byte-perfect restore) and the widened
status set; the parity test covers the `tool_reach` argv. Live: `GetRanges` resolved
to 0x22F8AE30; `tool_reach --value 40` extended mining/interaction/crafting on the
running game and disable restored the exact original bytes. 75 tests pass; lint clean.
