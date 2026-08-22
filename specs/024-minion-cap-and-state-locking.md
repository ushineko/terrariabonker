# Spec 024: Minion-cap cheat + concurrency-safe patch state

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: No issue tracker ticket (personal utility).

## Context

Two changes shipped together. (1) A new **raise-minion-cap** cheat. Terraria recomputes
`Player.maxMinions` every frame in `ResetEffects` (base = 1, then accessories add), so a
value-write loses the race — it needs a code patch, like the other stat cheats. (2) A
**concurrency fix** for the patch-state file: the GUI reaches the common layer
(`Service`/`Patcher`) across a sudo subprocess boundary (Qt can't run as root), and each
CLI invocation load-modify-**saves** the shared `patches.json`. Toggling many checkboxes at
once spawned concurrent processes whose writes clobbered each other, scrambling the state —
the checkbox/state desync the user reported (distinct from the earlier cold-cache bug).

## Requirements

1. A tunable `max_minions` cheat that raises the summon cap; a game restart clears it.
2. Concurrent enable/disable operations must not lose each other's records; the fix belongs
   in the **common layer** (so any caller — GUI-via-CLI or CLI scripts — is safe), not in
   GUI-only orchestration.

### Technical

3. **Minion cap** — CE recon (`ce/poc_minion.lua`) found `maxMinions` at Player `+0x3F8` and
   its reset in `ResetEffects`: `mov [edi+3F8], 1` (`C7 87 F8 03 00 00 01 00 00 00`). The
   cheat rewrites that immediate to N. New `Cheat.make_patched(value)` builds the patched
   bytes from the value (a **tunable in-place immediate patch** — a new mechanism);
   `is_enabled` is true when the site differs from `orig`. The anchor wildcards the immediate
   (resolves patched-or-not) and gains the adjacent `mov [edi+A60],1` reset for uniqueness.
   The second `maxMinions=1` write elsewhere is SetDefaults/init (not per-frame) — verified
   it needn't be patched.
4. **State locking** — `Patcher._locked()`: an exclusive `flock` on `patches.json.lock` held
   across a mutating op, re-loading the latest state under the lock so concurrent writers
   serialize instead of clobbering; `enable`/`disable` run inside it. `_save_state` writes
   atomically (temp + `os.replace`) so status readers never see a torn file.
5. **GUI** — enable/disable toggles are queued and run **one at a time** (coalescing repeated
   toggles of the same cheat), refreshing the checkboxes to ground-truth memory once the
   queue drains — a UX measure to avoid spawning many elevated processes; correctness is the
   common-layer lock.

## Risks & Assumptions

- **Single reset site.** Only `ResetEffects`' per-frame `maxMinions=1` is patched; verified
  live that patching the init/SetDefaults site makes no difference and the cap holds.
- **Lock scope.** The lock serializes mutating ops (enable/disable); read-only `status`
  needs no lock (atomic writes prevent torn reads). Proven: 20 concurrent locked writers
  lost 0 updates; the minion cap holds live at the set value.
- **Game-pid resets.** A game restart changes the pid; `_load_state` then correctly discards
  the dead process's state (its patches died with it). Not a data loss.
- **Rollback.** `git revert`; disable restores the reset immediate; a restart clears patches.

## Acceptance Criteria

- [x] `max_minions` cheat raises the cap (live: `maxMinions` holds at the set value, e.g. 10,
      with accessory bonuses on top); tunable via CLI/GUI value; a game restart clears it
- [x] `make_patched` tunable in-place patch: enable rewrites the immediate to N, live re-tune
      works, disable restores `1`, `is_enabled` is ground-truth (unit-tested)
- [x] Concurrent enable/disable no longer clobber the state file — an exclusive `flock` in
      the common layer serializes them (20-way isolation test: 0 lost); atomic writes
- [x] GUI serializes toggles and re-syncs checkboxes to memory after they settle
- [x] 115 tests pass (1 new); flake8 clean; README + version 0.17.0 (user-approved) updated

## Executive Summary

Adds a raise-minion-cap cheat (rewrite `ResetEffects`' `maxMinions=1` immediate to a tunable
N via a new `make_patched` in-place-patch mechanism) and fixes the multi-checkbox toggle
desync by making the patch-state file concurrency-safe in the common layer: an exclusive
`flock` around each mutating op (re-loading fresh state under the lock) plus atomic writes,
so concurrent CLI invocations serialize instead of clobbering. The GUI also queues toggles
to avoid a process storm and re-syncs to ground-truth memory. Reviewers: `patcher`'s
`reset_minions` anchor / `max_minions` cheat / `make_patched` handling, `Patcher._locked` +
atomic `_save_state`, and the GUI `_pump_cheats` queue.

## Testing

`tests/test_patcher.py::test_max_minions_tunable_immediate_patch` (immediate rewritten to
the cap, live re-tune, disable restores 1, ground-truth is_enabled). 115 tests pass; flake8
clean. Live: CE recon found the offset/site; `maxMinions` holds at the set cap; a 20-way
concurrent `flock` isolation test lost 0 of 20 updates (the lock serializes correctly).
