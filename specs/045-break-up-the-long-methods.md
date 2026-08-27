# Spec 045: Break up the over-long methods

**Status**: INCOMPLETE — proposed for review, not started. This is a plan to agree the
seams before any code moves; nothing here has been implemented.

> **Note**: This work has no associated issue tracker ticket (personal utility).

A pure, behaviour-preserving refactor of the longest multi-job methods. The mid-project
review (`docs/2026-08-26-mid-project-review.md`) named the god classes they live in (§2.1)
and the wall-clock timing some of them carry (§3.5), but compressed the per-method
breakdown out of the final document; the line counts and seams in the table below were
measured directly for this spec. The tests are the contract: the suite passes unchanged at
every commit, and each extracted piece becomes independently testable.

## Context

The review named three god classes (`Service`, `Patcher`, `MainWindow`) and, inside them,
a handful of methods doing several jobs at once. The highest-value class-level slice —
lifting the per-cheat arena protocol off `Patcher` into `arena_state.py` — is already done
(commit `b4a76d7`). This spec is the **method-level** work only.

**The god classes themselves are deliberately out of scope.** The review put them last and
called splitting them "a design decision about where the seams go, not a mechanical move".
That deserves its own spec (a future 046) with its own argument about module boundaries;
folding it in here would turn a safe, test-backed refactor into an architectural one.

**Why refactor at all, given they work.** Three of these methods write into a live game or
edit the save (`arena`, `spawn_npc`, `extract_vein`). A 100-line method mixing policy,
memory writes, timing and result-formatting is one where a future edit — the routine
game-update re-derivation this project is built around — cannot be reviewed in one screen,
and a mistake corrupts a world rather than failing a test. The value is not tidiness; it is
making the dangerous paths reviewable and their pieces unit-testable.

**Why it is safe to do now.** Each target already has a test harness: `test_tiles.py`
drives `extract_vein` against a `FakePatcher`, `test_auto_catch.py` drives `catch_tick`,
`test_patcher.py` drives `arena` and `_enable_injection`, `test_gui_construct.py` builds
the panel. The net that makes a behaviour-preserving refactor checkable is in place.

## The methods, tiered by value

| Method | File | Lines | Jobs it fuses | Tier |
|---|---|---|---|---|
| `extract_vein` | `service.py:707` | 107 | flood, gravity re-find (nested closure), batch loop, arm, poll, timing, disarm, result dict | **1** |
| `catch_tick` | `service.py:1070` | 79 | locate-cache, cast-gate, bite path (3 nested deadline loops), recast path, result dict | **1** |
| `arena` | `patcher.py:1248` | 73 | adopt-or-allocate, export resolve, springboard assemble, hook, poll, unhook, verify, stamp | **2** |
| `_enable_injection` | `patcher.py:1660` | 49 | body build (3 modes), idempotency, site/cave resolve, safety checks, write, edits, persist | **2** |
| `set_item` | `service.py:344` | 50 | stale-snapshot guard, re-template decision, then 8 `if x is not None` field writes | **2** |
| `_patches_group` | `main_window.py:568` | 49 | section-tab state machine + checkbox/spinbox/combo build + signal wiring in one loop | **3** |
| `spawn_npc` | `service.py:461` | 45 | template lookup, free-slot, position math, field copy | **3** |

Tier 1 are the real wins: long, deeply nested, and on the dangerous paths. Tier 2 are worth
doing and low-risk. Tier 3 are borderline (45–49 lines); included so the decision to leave
them is explicit rather than an oversight — a reviewer may reasonably say "not worth the
churn" for these, and that is an acceptable outcome.

### Tier 1

**`extract_vein` → a small `_VeinDig` helper (or free functions) plus a thin orchestrator.**
The seams the code already draws with comments:
- `_flood_initial(tm, x, y, want, cap)` → the first flood + the "not whitelisted" early return.
- the `still_standing()` closure → a method on a small object carrying `(tm, top, tid, rows)`.
  This is the delicate one: it encodes the hard-won fix for "mines non-contiguous sections
  across the screen" (stop at the first foreign solid tile in a column). Lifting it must not
  change that walk. **Characterise it with a test that pins the stop-at-ground behaviour
  before touching it.**
- the batch loop → `_drain(seed_fn, budget, timeout)` returning `(mined, batches, waits, stalled)`.
- the result dict → `_vein_result(...)`, so the orchestrator reads as flood → drain → report.

**`catch_tick` → a bite path and a recast path, each its own method.**
- `_ensure_projectile_array()` — the locate-and-cache preamble (also removes a repeated
  `if self._proj_arr is None` shape).
- `_take_bite(arr, bite, end)` → the reel: arm, wait-for-consume, wait-out-window, event.
- `_try_recast(arr, end)` → the gated cast: the `holding_rod` / `CAST_SETTLE` guard, the
  confirm-or-close-gate `while/else`. The three separate deadlines (`end`, `end + 0.2`,
  `end + 0.5`, `CAST_CONFIRM`) become named waits inside these two methods rather than four
  bare arithmetic expressions in one body.
The loop body then reads: update gate → bite? take it → else recast? try it.

### Tier 2

**`arena` → `_bootstrap_arena()`** for the springboard assemble/hook/poll/unhook block
(the `try/finally` that writes and always restores), leaving `arena()` as adopt → bootstrap
→ verify → stamp. The RWX verification and the "game is paused" diagnostic stay in `arena`.

**`_enable_injection` → `_build_stub_body(inj, value)`** (the three-mode body construction)
and `_resolve_sites(inj, stub_len)` (the first-enable vs. re-apply site/cave decision),
leaving the write loop in `_enable_injection`.

**`set_item` → a `SlotEdit` dataclass applied through a field→setter table.** The 8-way
`if x is not None` block becomes one loop over a table of `(attr, Inventory.setter)`. The
stale-snapshot guard becomes `_verify_expected(slot, expect_type)`. **The CLI boundary does
not change**: `cmd_set_item` and `client.set_item_argv` keep their keyword arguments;
`set_item` collects them into `SlotEdit` internally. (See Alternatives for the boundary
option.)

### Tier 3

**`_patches_group`** → `_patch_page(section)` builder + `_patch_row(grid, row, name, info)`,
so the section-change state machine is separated from widget construction.

**`spawn_npc`** → `_npc_spawn_position(player, distance)` (the facing/clamp math) leaving the
field-copy sequence in `spawn_npc`. Marginal; may be left as-is.

## Requirements

- **Behaviour-preserving.** No observable change to any CLI output, GUI behaviour, or bytes
  written to the game. The suite passes unchanged after each commit.
- **Test-first on the dangerous paths.** Before splitting `extract_vein`, `catch_tick`,
  `arena` or `spawn_npc`, add characterization tests for any behaviour currently covered
  only incidentally — specifically `still_standing`'s stop-at-ground walk, `catch_tick`'s
  gate-close-on-failed-cast, and `spawn_npc`'s behind-the-player clamp. A refactor of a
  memory-writing path is only as safe as the test that pins it.
- **One method per commit, each independently revertible.** No commit leaves the suite red.
- **Surgical.** Edit only what the split needs; no reformatting adjacent code, no renaming
  unrelated locals, no docstring rewrites beyond moving text to the method it now describes.
- **No new public signatures that callers must chase**, except a deliberate, documented
  `SlotEdit` if that alternative is chosen — and even then the CLI/GUI boundary stays flat.
- **Extracted helpers are private** (`_name`) unless a test needs them, in which case the
  test's need is the justification and is noted in the test.

## Acceptance criteria

- [ ] `extract_vein` is split so its body reads as flood → drain → report, with the
      gravity re-find isolated and its stop-at-ground behaviour pinned by a test that fails
      if the walk runs past the first foreign solid tile.
- [ ] `catch_tick` is split into a bite path and a recast path; the four ad-hoc deadlines
      are named; the gate-closes-on-unconfirmed-cast behaviour keeps its existing test and
      gains one asserting the bite path and recast path cannot both fire in one tick.
- [ ] `arena`'s springboard assemble/hook/poll/unhook block is a named method; the
      `try/finally` restore is preserved exactly (a leaked hook is a live-code corruption).
- [ ] `_enable_injection`'s three-mode body build and its site/cave resolution are separate
      methods; the idempotent-re-apply path (no re-resolve) still has its test.
- [ ] `set_item`'s field writes go through one table, not eight branches; every field still
      round-trips (a test per field, or one parametrized over the table); the CLI argv
      contract is unchanged (verified by `test_view_parity`).
- [ ] Each tier-3 method is either split or explicitly recorded here as "left as-is, and
      why" — no silent skips.
- [ ] The full suite passes (`pytest`, headless) at every commit; `flake8` clean on changed
      files; no method in the table exceeds ~40 lines afterwards, or carries a note saying
      why it must.
- [ ] No behaviour verified in-game (spec 040 vein mining, spec 043 catch, the arena
      bootstrap) changed — argued from the unchanged tests, not re-run, and stated as such.

## Risks & Assumptions

- **The three memory-writing methods are the hazard.** A bug in a refactored `arena`,
  `spawn_npc` or `extract_vein` corrupts a live game or a world, and the existing tests use
  synthetic memory, so they cannot catch a mistake that only shows against the real game.
  Mitigation: characterization tests first (above), split in the smallest steps, and treat
  any test that needed changing during a "behaviour-preserving" split as a red flag that
  behaviour did change.
- **`extract_vein`'s `still_standing` closure captures four values and is called in a loop.**
  Lifting it to a method means threading that state explicitly; getting the fall-tracking
  wrong reintroduces the "mines non-contiguous sections" bug, which was expensive to find
  and has no cheap in-game re-test. This is the single riskiest extraction in the spec.
- **`set_item` and `fishing_buff_tick` signatures reach the CLI.** `set_item`'s keywords are
  produced by `client.set_item_argv` and consumed by `cmd_set_item`; changing the *boundary*
  ripples to three files and `test_view_parity`. This spec keeps the boundary flat and only
  restructures the internals, which contains the blast radius. If the boundary is changed
  later, that is its own decision.
- **`catch_tick`'s timing is wall-clock.** The tests already pass small `budget`/`CAST_*`
  values; the split must keep those injectable, or the suite regains the ~1.5s of real
  sleeping the review flagged (§3.5). Prefer passing the deadlines in over reading
  `self.CAST_*` deep in a helper.
- **Rollback**: each commit is a self-contained method split, revertible with `git revert`.
  Nothing touches the save format, the state file, or the on-disk caches.
- **Assumption**: the current test suite genuinely covers the observable behaviour of these
  methods. Where it does not (the characterization gaps above), the spec adds the test
  before the split rather than after — so a green suite after the split means something.

## Alternatives considered

- **Do nothing.** Defensible: they work, and a refactor bug in a machine-code-writing path
  is worse than a long method. Rejected for tier 1 only — 107 and 79 lines on the dangerous
  paths are past the point where a game-update edit can be reviewed safely. Tier 3 doing
  nothing is an accepted outcome.
- **A `SlotEdit` dataclass all the way to the CLI**, replacing `set_item`'s keywords and
  `set_item_argv`'s. Cleaner call sites, but it ripples through the argv contract and its
  parity test for a method that is only awkward, not dangerous. Rejected in favour of an
  internal-only dataclass; the boundary stays flat.
- **Split the god classes in the same pass** (`Service` ~1310 lines into `InventoryOps` /
  `FishingOps` / `MiningOps`, etc.). Rejected: out of scope by the review's own ranking, and
  it is a boundary-design decision that wants its own spec and its own review, not a
  tail-end of a mechanical refactor.

## Executive summary

*(Populate at MR time, per the Phase 6.5 reconciliation gate.)*
