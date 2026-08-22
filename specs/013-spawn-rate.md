# Spec 013: Spawn-rate cheat

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

Control enemy spawns. `Spawner.GetSpawnRate(out spawnRate, out maxSpawns)` computes the
per-frame spawn odds and the active-enemy cap; forcing its outputs sets the spawn
intensity. It's the same `out esi / out edi`-at-the-epilogue shape as `GetRanges`, so it
reuses the code-cave injection (ported from the FearLess ReGrind table, re-derived for
1.4.5.7).

## Requirements

### Functional

1. A `spawn_rate` code-patch cheat that forces `maxSpawns` to a tunable value (active-
   enemy cap) with a low `spawnRate` (frequent). 0 = peaceful (no spawns). Enable/
   disable/status via `patch` (+ `--value`) and a GUI checkbox + spinbox.
2. Disable restores the original bytes; a game restart clears it.

### Technical

3. Inject at `Spawner.GetSpawnRate` +0x1EAA, overwriting `lea esp,[ebp-0C]; pop esi;
   pop edi` (5 bytes; `esi` = out spawnRate ptr, `edi` = out maxSpawns ptr, held from
   the prologue). The stub `mov [esi],6; mov [edi],N`, re-runs the 5 bytes, jumps back.
   Anchor is the method prologue (fixed) + the fldz/first-static-store (ASLR immediates
   wildcarded). Reuses the v0.8.0 injection machinery and the `make_body` generalization
   (`_force_spawn`).

## Risks & Assumptions

- **Build-specific.** The +0x1EAA offset and the anchor are for 1.4.5.7; re-derive with
  `ce/poc_spawner.lua`. A live code patch — guarded by a unique wildcard anchor (verified
  to resolve to `GetSpawnRate`) and a byte-perfect restore.
- **Intensity.** maxSpawns default 15 (noticeably more; vanilla surface cap ~5–8);
  range 0–200. High values are a genuine swarm.
- **Rollback.** Additive; `git revert`. disable restores exact bytes.

## Acceptance Criteria

- [x] `spawn_rate` enable forces `[esi]`=6 / `[edi]`=N at the GetSpawnRate epilogue; disable restores exact bytes
- [x] Wildcard anchor resolves uniquely to `GetSpawnRate` (0x1c6b3bf0 live)
- [x] `patch enable/disable/status spawn_rate` (+ `--value`) work; GUI shows checkbox + spinbox
- [x] Live-verified: enemies swarm at value 40 (well above vanilla); disable byte-perfect
- [x] Parity test covers the `spawn_rate` argv; 84 tests pass headless; lint clean

## Executive Summary

Adds a spawn-rate cheat — force `Spawner.GetSpawnRate`'s outputs (low spawnRate, tunable
maxSpawns) so enemies spawn much more (or, at 0, not at all). Reuses the code-cave
injection; ported from ReGrind and re-derived for 1.4.5.7. Reviewers: `INJECTIONS["spawn_rate"]`
and `_force_spawn` in `patcher.py`; `ce/poc_spawner.lua`.

## Testing

`tests/test_patcher.py` adds an injection round-trip test (stub is `mov [esi],6;
mov [edi],40` + the overwrite; byte-perfect restore) and the widened status set; the
parity test covers the argv. Live: the anchor resolved to `GetSpawnRate` (0x1c6b3bf0);
`spawn_rate --value 40` produced a heavy swarm and disable restored the exact bytes.
84 tests pass; lint clean.
