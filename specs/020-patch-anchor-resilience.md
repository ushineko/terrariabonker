# Spec 020: Code-patch anchor resilience — wildcard patched bytes; ground-truth status

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: No issue tracker ticket (personal utility). Bug fix to the code-patch cheats.

## Context

After a game restart, `fast_place` failed with `[ERROR] anchor 'place' not found`, and the
GUI checkbox could desync from the real cheat state (checkbox off while the cheat was
actually applied). Root cause: the `place` and `reset_block` AOB anchors **included the
very bytes the cheat overwrites**, so once the cheat was applied the anchor no longer
matched. That was masked by the per-pid site cache — until the cache was lost. A race
between the GUI's periodic `patch status` (every 500 ms) and an enable/disable, both
writing the shared per-pid state file, could drop a cached site entry while the site stayed
patched; the next enable/disable then re-scanned, failed to match the (now-patched) anchor,
and raised "anchor not found". Diagnosis on the live game confirmed the `place` site held
the fast_place patch (`bf 04 00 00 00 90*5`) while the state file listed neither the site
nor the enabled flag.

## Requirements

1. The `place` and `reset_block` anchors resolve **whether or not** the cheat is applied.
2. `is_enabled` (and thus `patch status`) reflects the **actual memory**, so a racy toggle
   self-heals on the next refresh instead of leaving the checkbox desynced.
3. No change to what the cheats do or to their patch/orig bytes.

### Technical

4. **Wildcard the patched bytes** in both anchors:
   - `place`: wildcard the 10 bytes at `+20` (`mov eax,1; cmp edi,eax; cmovl edi,eax` ->
     `mov edi,4; nop*5`); keep the invariant prefix (`fmulp…mov edi,ecx…jle`) and add the
     downstream store (`mov [esp+4],edi; mov eax,[ebp+8]; mov [esp],eax`) for uniqueness.
   - `reset_block`: wildcard the reach region (`+0`, 10 bytes) and the mining region
     (`+12`, 6 bytes); keep the invariant `fld1` (`+10`) and extend with the downstream
     field-clear run (`mov byte [edi+866],0; [edi+870],0; [edi+871],1`) for uniqueness.
   Patch offsets are unchanged (place `+20`, reach `+0`, mining `+12`); the anchors still
   start at the same base.
5. **Ground-truth `is_enabled`**: resolve the anchor (cached; scans once on a cold cache —
   now safe because the anchor is patch-invariant) and compare the bytes at the patch site,
   instead of returning False whenever the anchor isn't cached. Returns False if the method
   isn't JIT-compiled yet (`PatchError` caught).

## Risks & Assumptions

- **Uniqueness.** Both wildcarded patterns were verified to match exactly one site on the
  live game (patched), and the fixed regions (place: 30 bytes; reset_block: 22 bytes) are
  distinctive. A game update degrades to "anchor not found", never a bad write.
- **Cold-cache scan cost.** `is_enabled` may scan once per uncached anchor on the first
  status after a new game pid, then uses the cache; the 500 ms GUI refresh stays cheap.
- **State race.** The wildcarded anchors make a dropped cache entry recoverable (resolve
  works patched-or-not), so the race can no longer strand a cheat; a full fix of the
  shared-state write race is out of scope here.
- **Rollback.** `git revert`. Anchors/AOBs only; no behavior change to the cheats.

## Acceptance Criteria

- [x] `place` and `reset_block` resolve when the site is pristine AND when patched
      (regression test `test_anchor_resolves_whether_or_not_patched`); verified live
      (resolve `place` succeeded on the patched game that previously errored)
- [x] `is_enabled` reads ground truth without relying on the cache
      (`test_is_enabled_reads_ground_truth_without_cache`); live `is_enabled(fast_place)` =
      True on the orphaned patch that the state file had lost
- [x] Patch/orig bytes and patch offsets unchanged; mining/reach/fast_place still
      enable/disable correctly (existing tests green)
- [x] 103 tests pass headless (2 new); flake8 clean; version 0.15.1 (user-approved)

## Executive Summary

Fixes `[ERROR] anchor 'place' not found` after a restart and the checkbox desync. The
`place`/`reset_block` anchors previously spanned the bytes the cheat overwrites, so once
applied (and the per-pid site cache lost to a state-file race) a re-resolve failed. The
fix wildcards the patched bytes (keeping invariant prefixes + downstream context for
uniqueness) so the anchor resolves whether or not the cheat is applied, and makes
`is_enabled` read ground truth from memory so status self-heals. Reviewers: the `place`
and `reset_block` entries in `ANCHORS`, and `Patcher.is_enabled`.

## Testing

`tests/test_patcher.py`: `test_anchor_resolves_whether_or_not_patched` (both anchors
resolve pristine and patched, for fast_place/reach/mining) and
`test_is_enabled_reads_ground_truth_without_cache`. 103 tests pass; flake8 clean. Live:
on the restarted game that had errored, `_resolve('place')` now succeeds and
`is_enabled('fast_place')` / `reach` / `mining` all correctly report True from memory.
