# Spec 016: Drop-chance floor cheat (guaranteed / minimum-% common drops)

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo. The cheat is ported from the FearLess "TerrariaReGrind"
> Cheat Engine table (credit in the README); the 1.4.5.7 sites were re-derived here.

## Context

Terraria resolves most enemy/grab-bag drops through `CommonDrop.TryDroppingItem`,
which rolls `rng.Next(chanceDenominator)` and drops when the roll `< chanceNumerator`.
Capping the denominator raises the effective drop chance without ever lowering an
already-better one, giving a tunable "minimum drop chance %" (100% = guaranteed). This
is a code patch: the roll is recomputed each attempt, so an external value-write can't
hold it.

On 1.4.5.7 the field layout and site differ from the ReGrind 1.4.5 table:
`chanceDenominator` is `this+0x10` (was `+0x0C`), loaded at `TryDroppingItem+0x26`
(`mov ecx,[esi+10]`). The build also JITs **four** structurally identical
CommonDrop-family resolvers (twins that differ only in call targets), all of which
must be patched for consistent coverage.

## Requirements

1. A `loot` code-patch cheat that floors the common-drop chance at a tunable percent
   (1–100; 100 = guaranteed), applied via `/proc` like the other injections and cleared
   by a game restart.
2. The value persists and is restorable (via the existing per-cheat value persistence,
   spec 015) and is live-tunable from the GUI without re-resolving the anchor.

### Technical

3. Injection at `TryDroppingItem+0x26` over `8B 4E 10 89 4C 24 04`
   (`mov ecx,[esi+10]; mov [esp+04],ecx`); the stub clamps the denominator to
   `cap = 100 // pct` (`mov ecx,[esi+10]; cmp ecx,cap; jle keep; mov ecx,cap; keep:
   mov [esp+04],ecx`) and does **not** re-run the displaced bytes (`rerun_overwrite=
   False`).
4. **Multi-site injections** (`Injection.multi`): the anchor matches every CommonDrop
   twin; the stub is installed at all of them, each with its own distinct code cave
   (`_find_cave` skips already-claimed ranges).
5. **Anchor placed downstream of the patched bytes.** The anchor is the distinctive
   tail at `+0x2D` (the `chanceNumerator [esi+1C]` / `itemId [esi+0C]` reads), with
   `inject_off = -7`, so the pattern is never corrupted by its own jump — disable and
   recovery stay scannable.
6. **Idempotent enable.** When already installed, `enable` reuses the recorded sites
   and rewrites only the stub (live value change); it does not re-resolve the anchor
   (a pristine re-scan would find nothing once the jump is in place, and previously
   raised "anchor not found").

## Risks & Assumptions

- **Coverage scope.** Floors `CommonDrop`-family rules only (the four twins). Rule
  types with a different body (e.g. `OneFromOptions`, expert-conditional) are not
  floored. Documented, not a regression.
- **Twin uniformity.** All four matched sites were verified to share the exact
  `mov ecx,[esi+10]; mov [esp+04],ecx` shape (checked the untouched `+0x2B..+0x2C =
  24 04` and the `+0x2D` tail), so the overwrite and disable-restore are safe on each.
- **Build-specific AOB.** The tail pattern and offsets are for 1.4.5.7; re-derive with
  `ce/poc_droploot_teleport.lua`. A game update degrades to "anchor not found", never a
  bad write.
- **Rollback.** `git revert`; disable restores the original bytes at every site; a game
  restart clears all patches.

## Acceptance Criteria

- [x] `loot` cheat floors common-drop chance at N% (100 = guaranteed), live-verified
      in-game (user-confirmed drops)
- [x] Stub clamps `[esi+10]` to `100 // pct` at every CommonDrop twin (4 sites live),
      each with a distinct cave; jmp-back lands at `inject+7`
- [x] Anchor sits downstream of the patched bytes (`inject_off = -7`); re-enable and
      live value change succeed without re-resolving (idempotent) — regression test
- [x] 90 tests pass headless (loot roundtrip, multi-site, idempotent re-enable); lint
      clean
- [x] README documents the cheat and credits the FearLess ReGrind table

## Executive Summary

Adds a tunable drop-chance floor (guaranteed at 100%) by capping the roll denominator
in `CommonDrop.TryDroppingItem`. The 1.4.5.7 site/field offsets were re-derived from the
ReGrind 1.4.5 table; the anchor was placed downstream of the patched bytes and enable
made idempotent so re-toggling and live value changes don't hit a self-corrupted scan,
and injections were generalized to patch all four CommonDrop twins. Reviewers:
`patcher._cap_drop_denom`, the `trydrop` anchor, and `_enable_injection` (multi-site +
idempotent path).

## Testing

`tests/test_patcher.py`: `test_loot_injection_caps_denominator`,
`test_loot_multi_site_patches_every_twin`,
`test_loot_reenable_is_idempotent_without_rescan`. Live: enabled on 4 CommonDrop twins
(distinct caves, cap verified), value change 100↔50 rewrote stubs with no re-scan, user
confirmed guaranteed drops in-game. 90 tests pass; lint clean.
