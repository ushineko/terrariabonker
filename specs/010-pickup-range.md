# Spec 010: Item pickup range (GrabItems code-cave injection)

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

Follow-up to the reach work, reusing the code-cave injection the reach cheat added.
`Player.GrabItems` computes an item's grab range (a call returns it in `eax`, then
`mov [ebp-54],eax` stores it). Scaling that value scales the pickup radius. The
approach is ported from the FearLess "ReGrind" 1.4.5 table and re-derived for 1.4.5.7
(1.4.5.7 uses a direct `E8` call where 1.4.5 used an indirect `FF 15`).

## Requirements

### Functional

1. A `pickup` code-patch cheat that scales the item grab range by a tunable multiplier,
   so items are pulled from far off-screen. Enable/disable/status via `patch` (+
   `--value`), and a GUI checkbox + spinbox, like the other code patches.
2. Disable restores the original bytes exactly; a game restart clears it.

### Technical

3. Inject at `GrabItems`, at the grab-range store `mov [ebp-54],eax` (offset +164 this
   build), overwriting `mov [ebp-54],eax; lea eax,[ebp-50]` (6 bytes). The cave stub is
   `imul eax,eax,N` (`6B C0 nn` for N≤127, else `69 C0` + imm32), then the 6 original
   bytes, then a jump back. So the store writes the scaled range.
4. Reuses the v0.8.0 code-cave machinery; the `Injection` dataclass gained a
   `make_body(value) -> bytes` builder so different injections supply different stubs
   (`imul eax,N` here, the two `mov`s for `tool_reach`). Anchor is fixed bytes around
   the store with the two `get_Hitbox` call operands wildcarded.

## Risks & Assumptions

- **Live code patch.** Guarded by a unique wildcard anchor (verified to resolve to
  `GrabItems` at the grab-range store) and a byte-perfect, live-verified restore.
- **Multiplier semantics.** N scales whatever the grab-range call returns; 50 (the
  ReGrind default) pulls items from well off-screen. Range 2–500, tunable.
- **Build-specific.** Anchor/offset for 1.4.5.7; re-derive with `ce/poc_grabitems.lua`
  after an update. `git revert` for rollback; a restart clears the patch and cave.

## Acceptance Criteria

- [x] `pickup` enable injects `imul eax,N` before the grab-range store; disable restores exact bytes
- [x] Wildcard anchor resolves uniquely to `GrabItems` at the store (0x1c69d4b4 live)
- [x] `patch enable/disable/status pickup` (+ `--value`) work; GUI shows a checkbox + spinbox
- [x] Live-verified: pickup radius scales (items pulled from off-screen); disable is byte-perfect
- [x] `Injection.make_body` generalizes the stub; `tool_reach` refactored onto it
- [x] Parity test covers the `pickup` argv; 77 tests pass headless; lint clean

## Executive Summary

Adds an item-pickup-range cheat by injecting `imul eax,N` before the grab-range store
in `Player.GrabItems`, scaling the radius so items are pulled from off-screen. Reuses
the v0.8.0 code-cave injection, generalized so an `Injection` supplies its own stub
body (`make_body`). Ported from the ReGrind table and re-derived for 1.4.5.7.
Reviewers: `patcher.py` (`INJECTIONS["pickup"]`, `_imul_eax`, `make_body`) and
`ce/poc_grabitems.lua`.

## Testing

`tests/test_patcher.py` adds `test_pickup_injection_uses_imul_stub` (jmp installed;
cave stub is `6B C0 32` + the 6 overwritten bytes; byte-perfect restore) and the
widened status set; the parity test covers the `pickup` argv. Live: the anchor
resolved to `GrabItems+164` (0x1c69d4b4); `pickup --value 50` pulled items from far
off-screen and disable restored the exact bytes. 77 tests pass; lint clean.
