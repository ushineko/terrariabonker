# Mid-project technical review — 2026-08-26

**At**: v0.37.0, 11,433 lines of source, 7,440 lines of tests, 546 tests passing.
**Method**: three independent read-only passes (core modules, support modules, test suite),
with every finding below re-checked by hand before it was written down. Two reviewer
findings were rejected on that check and are recorded at the end.

This is a health check, not a plan. Nothing here is a request to stop and refactor; the
purpose is to write down what a fresh reader would flag, so the decisions are deliberate
rather than inherited.

## What is working

Worth stating first, because the rest of this document is a list of problems.

- **Tests carry their reasons.** Nearly every test names the bug it guards in its
  docstring. That is why the mutation passes in the last two releases were able to tell a
  real gap from an equivalent mutant — the intent was written down.
- **Offsets are justified, not asserted.** `docs/discovery.md`, the build ledger, and the
  specs record how each number was derived and on which build it was seen working.
- **The synthetic memory image** means the whole suite runs headless, without the game and
  without root. That is the single best structural decision in the project.
- **Failures are recorded rather than tidied away.** The specs keep wrong turns, and the
  validation reports lead with what broke.

## 1. Correctness bugs found during the review

These are not smells. They are wrong today.

### 1.1 The inventory grid syncs against the wrong tab — `gui/main_window.py:1269`

```python
return self.tabs.currentIndex() == self.tabs.indexOf(self.tabs.widget(1))
```

`indexOf(widget(1))` is `1` by definition. Tab 1 is **Effects**; Inventory is tab 3
(`main_window.py:295-311`). So the 1 Hz grid sync runs only while the user is looking at
Effects, and never while they are looking at the grid it exists to keep fresh.

The same file already warns against this exact mistake at `:298-301` — *"dispatch on the
widget, never on the index"* — and `_on_tab_changed` was written correctly. This site was
not. **Fix: compare against `self.tab_inventory`.**

### 1.2 `fast_mining` and `long_reach` report the wrong slots — `service.py:1386-1397`

```python
hit = []
for inv in self._all_inventories():
    hit = inv.make_fast_mining(use_time, use_anim, pick)
return hit
```

`hit` is reassigned each iteration, so the return describes only the **last** player copy —
which is usually an inert load-time snapshot, not the live player. The CLI prints that
count to the user (`cli.py:576, 584`). The writes themselves are fine; the report is not.

### 1.3 A conditional with two identical arms — `version.py:240`

```python
banner = "[version] WARNING" if force else "[version] WARNING"
```

Dead as written, and it hides that an intended distinction (warn vs. warn-and-override)
was never implemented. Either implement it or drop the conditional.

## 2. Structural findings

### 2.1 Three god classes

| Class | Size | Jobs it owns |
|---|---|---|
| `Service` (`service.py:83-1396`) | ~1310 lines, ~55 methods | process attach, locate caching, build gating, item templates, NPC spawning, compendium caches, tilemaps, vein mining, fishing, potions, auto-catch, arena allocation, profile restore |
| `Patcher` (`patcher.py:1127-2014`) | ~890 lines | state persistence, /proc arithmetic, arena allocation, AOB scanning, cave finding, byte patching — **plus eight cheat-specific methods** (`auto_use_arm/disarm/armed/presses`, `ore_queue/armed/arm/disarm`, `patcher.py:1704-1786`) |
| `MainWindow` (`gui/main_window.py:158-1716`) | ~1560 lines, 38 attributes in `__init__`, 5 timers, 5 `_inflight` flags | every tab, every watcher, every reply parser |

The cheat-specific methods on `Patcher` are the sharpest of the three: per-cheat protocol
has leaked into the generic patch engine, so adding a cheat means editing the engine.
`CompendiumTab` (`gui/compendium.py`) is the precedent for how the GUI tabs should be
split — one tab already is its own widget class.

### 2.2 The same code, written five or six times

| Pattern | Copies | Where |
|---|---|---|
| JSON-line reply parsing in the GUI | **6** | `main_window.py:746, 775, 808, 842, 891, 954` — while `gui/client.py` exists to own exactly this contract |
| The `_inflight` tick guard | **5** | `main_window.py:741, 803, 839, 887, 1283`, with five parallel flags |
| watch/tick/stop triples in the GUI | **5** | `main_window.py:869-991` |
| Blocking watch loops in the service | **5** | `service.py:826, 1038, 1164, 1189, 1225` — all `while rounds is None or n < rounds` |
| `--watch` blocks in the CLI | **4** | `cli.py:328, 363, 389, 459` |
| `json.loads(raw.strip().splitlines()[-1])` | **6** | `gui/client.py:23, 34, 95, 119, 190, 217` |
| numpy region scans | **4**, subtly different | `locate.py:107`, `content.py:84` (the only generalised one), `service.py:265`, `service.py:431` |
| "read pinned Main offset, else brute-scan 0x4000" | **3** | `npcs.py:115`, `npcs.py:189`, `projectiles.py:230` — three different validators, three different failure returns |
| i32 setters differing only in an offset | **10** | `inventory.py:263-306` |

`Patcher.probe` and `Patcher.details` (`patcher.py:1939-1998`) are ~90% the same function,
differing only in output key names.

### 2.3 One game offset, five spellings

The mono szarray layout — length at `+0xC`, data at `+0x10` — is declared independently as
`inventory.ARR_LEN_OFF/ARR_DATA_OFF` (:29-30), `projectiles.ARRAY_LEN_OFF/ARRAY_DATA_OFF`
(:29-30), `recipes.ARR_LEN/ARR_DATA` (:34-35), `tiles._ENTRIES_OFF` (:26), and inline as a
bare `0x10` in `locate.py:217`. `buffs.py:20` does it correctly by importing from
`inventory`.

Related: `ITEM_TYPE`/`ITEM_STACK` are declared in both `inventory.py:35-36` and
`recipes.py:30-31`; `content.py:36` imports an *Item* offset from `recipes`, so the item
field set cannot be reviewed in one place. The `Main` static offsets are scattered across
five modules (`locate`, `recipes`, `npcs`, `tiles`, `projectiles`) with no shared home,
though they are all offsets into the same block and all pinned to the same build key.

**This is the finding with the most operational weight.** A game update is the routine
event this project is built around, and re-deriving one number currently means finding
every spelling of it.

Also: `inventory.py:10-11`'s docstring says the prefix is at `+0xAC`, but `ITEM_PREFIX` is
`0x15C` and `0xAC` is `ITEM_DAMAGE`. That docstring is what a future re-deriver reads first.

### 2.4 Encapsulation leaks

- `service.py:1287` — `if p._arena and p._arena_ok(p._arena)`: the service re-implements the
  arena's validity check through two privates. This is the path that decides whether cheats
  can be enabled at all.
- `service.py:235, 288` — `inv._item_addr(...)` to do raw item writes that belong on
  `Inventory`. The `except AttributeError` at `:238` is the tell.
- `recipes.py:21-22` — imports `_exec_regions` and `read_mono_string` from `locate` **and**
  `_pat` from `patcher`: two modules' privates became public API by accident.
- `gui/main_window.py:1474, 1489, 1663` — `recipes._CACHE = None` from outside, three times,
  because `recipes.load(path)` memoises into a module global while ignoring its own `path`
  argument (`recipes.py:173-187`). A second call with a different path returns the first
  file's contents.
- `sprites.py:176` — `set(names._NAMES)` where `names.all_names()` exists for this.
- `service.py:574` — the privileged side reaches for `sprites._NPC_FRAMES_FILE`.

### 2.5 Dead code and ignored parameters

- **`Anchor.unique` is never set** (`patcher.py:150`), so the "matched N sites but must be
  unique" branch at `:1474-1478` is unreachable. `Anchor.variants` is likewise never
  populated, making `candidates()`'s ordering logic (`:157-172`) dead, and `_ALSO_VERIFIED`
  (`:380`) is a permanently empty dict.
- **`_find_cave`'s `claimed`/`writable` parameters are unreachable** (`patcher.py:1503`):
  every injection sets `arena=True`, so the non-arena branch at `:1685` never runs.
  `Injection.writes_cave` therefore has no effect either.
- `inventory.fishing_gear(index=None)` (:208) never reads `index`; no caller passes it.
- `gui/client.py:303` — `restore_summary(report, _unused=None)`.
- `gui/compendium.py:314` — `_row(entry, icon)`; the only caller passes `icon=True`.
- `cli.py:43` — `_print_snapshot(svc, show_all=False)`; no caller passes it.
- `service.py:1451` — an import inside a 10 Hz loop, already marked `# noqa: F401`.
- Unused constants: `inventory.ITEM_AXE`, `ITEM_HAMMER`, `projectiles.COUNTER_THRESHOLD`
  (referenced only from a docstring), `invgrid.GRID_SLOTS`.

### 2.6 Inconsistencies worth settling

- `Inventory` stores its address as `self.life` (`inventory.py:112`) while every other type
  uses `life_addr`.
- Three error-reporting styles at the CLI boundary: `main()` catches `ServiceError`,
  `cmd_patch` catches `PatchError` itself, `cmd_freeze` uses `sys.exit("[ERROR] …")`,
  `cmd_fishing_buffs` prints and returns 1.
- **The GUI recovers errors by string-matching `"[ERROR]"` in merged stdout**
  (`main_window.py:846, 895, 1217, 1412`) rather than reading the worker's `ok` field,
  which `_serve_reply` already sends (`cli.py:168-170`). I wrote one of those four sites
  today, against a `{"error": ...}` contract I had invented; the string-matching version is
  what the worker actually provides, and neither is a typed contract.
- Bare `except Exception` at `patcher.py:81, 1382`, `service.py:1271, 1294, 1486`,
  `cli.py:189, 686` — `Service.frames_advancing` and `_VeinWatch.close` swallow programming
  errors along with expected ones.
- `service.py` imports `numpy` at the top **and** again inside `_npc_template_block:423`;
  `struct` likewise. ~25 function-level imports with no stated rule.

## 3. The test suite

546 tests, and the suite's core habit — a docstring naming the bug — is good enough that
these findings are about scaffolding rather than substance.

### 3.1 Tests that assert on source text

Four sites grep their own source instead of exercising behaviour:

- `test_build_ledger.py:524` — `assert 'entry["runtime"] = runtime' in src`. Passes if the
  line sits in a dead branch; fails on any rename. The behaviour is two calls and two
  assertions away.
- `test_tiles.py:325` — asserts `"SENTINEL" not in src`, i.e. that a *variable name* is
  absent.
- `test_gui_helper.py:45` — greps for `import threading`; defeated by
  `from threading import Thread`.
- `test_patcher.py:462` (removed today) was the fourth, and it broke on a refactor while
  the bug it guarded stayed possible — which is the failure mode of all four.

`test_view_parity.py` already shows the right technique (AST import extraction).

### 3.2 Scaffolding duplicated instead of shared

`conftest.py` is 59 lines and holds only `FakeMem`. Everything else was copy-pasted:

| Helper | Copies |
|---|---|
| Qt application fixture | **7**, under two names (`app`, `qt_app`) |
| `_window(monkeypatch)` | **4 helpers + 9 inlined repeats**, including four inside the file that defines the helper |
| Patcher-over-synthetic-game `game` fixture | **5**, with the same 5-line comment verbatim |
| `Service.__new__` fake | **9 sites**, each populating a different attribute subset |

The `Service.__new__` pattern is the one with teeth: because it skips `__init__`, adding a
required attribute to `Service` does not fail these fixtures — it fails later, in whichever
test happens to touch it.

**Qt headless setup is import-order-dependent.** `QT_QPA_PLATFORM=offscreen` is set in four
test modules, but `test_fishing.py`, `test_auto_catch.py` and `test_fishing_buffs.py`
construct real `MainWindow` widgets and set neither it nor an importorskip guard. They pass
in a full run only because another module's import ran first during collection. Running
`pytest tests/test_auto_catch.py` alone on a headless box is a coin flip. Both belong in
`conftest.py`.

### 3.3 The parity test does not cover the newest commands

`gui/client.py:243-271` — `COMMANDS` and `SAMPLE_ARGVS` are hand-maintained and stale.
Missing: `catch-tick`, `catch-stop`, `fishing-buffs`, `build-check`, `accept-build`,
`restore`, `extract-sprites`. So `test_view_parity.py` silently does not guard the argv
builders added in v0.36.0 and v0.37.0 — the exact drift the module docstring says it
prevents. `cli.SERVE_OPS` (`cli.py:159`) is a second hand-maintained duplicate of the
subcommand list with the same exposure.

### 3.4 Weak assertions

- `test_content.py:165` — `assert len(unresolved) <= 10`: a tolerance with no basis; a
  regression from 3 to 10 passes silently.
- `test_invgrid.py:31` — named "multiword name abbreviates", asserts only that a `.` is
  present and the string got shorter. `"C."` passes.
- `test_view_parity.py:31` — named "parses against the CLI", asserts
  `getattr(args, "func", None) is not None`.
- `test_smart_cursor.py:43-96` — 7 of 12 tests assert byte counts (`body.count(...) == 2`),
  which pass on a stub emitting the right opcodes in the wrong order.
  `test_inventory_accs.py:118` decodes a `je` displacement and checks where it lands; that
  is the same constraint tested structurally, and it is the pattern to follow.
- `test_patcher.py:47` hardcodes all 15 cheat names; `set(p.status()) == set(PATCH_CATALOG)`
  says the same thing without breaking on every new cheat.

### 3.5 Wall-clock sleeps inside unit tests

`test_auto_catch.py:134` runs `watch_catch(rounds=3)` at the default `budget=0.30` — about
0.9 s of real spinning to assert that a gate never arms. Fifteen further sites pass
`budget=0.05`. One file spends well over 1.5 s sleeping to test synchronous logic.

> **Update, same day:** the three bugs in §1 are fixed (`8db0e0c`..), with regression
> tests for the two that have observable behaviour. §1.3 is a code deletion with no
> behavioural surface, so it has no test — noted rather than papered over with a
> source-grepping assertion, which is the anti-pattern §3.1 flags.

## 4. Suggested order of work

Ordered by (harm × cheapness), not by section:

1. **Fix the three bugs in §1.** All small, all currently wrong.
2. **Un-stale `COMMANDS`/`SAMPLE_ARGVS`** (§3.3), and make the list derive from the argv
   builders rather than be maintained beside them. A test that has quietly stopped covering
   new code is worse than no test, because it reads as coverage.
3. **One home for the szarray and Item offsets** (§2.3). This is the one that will cost real
   time on the next game update.
4. **Move the Qt fixture and headless setup into `conftest.py`** (§3.2). Cheap, and it fixes
   a real "passes only in a full run" fragility.
5. **Collapse the GUI reply parsing into `client.py`** (§2.2, six copies) and give the
   worker replies a typed contract instead of `[ERROR]` string-matching (§2.6).
6. **Delete the dead branches in `patcher.py`** (§2.5) — `Anchor.unique`, `variants`,
   `_find_cave(claimed, writable)`. Dead code in a module that writes machine code to a
   live process is a specific hazard: it invites a future reader to trust a path nothing
   exercises.
7. Replace the four source-grepping tests with behavioural equivalents (§3.1).
8. The god classes (§2.1) last. They are the largest and the least urgent, and splitting
   `Patcher`'s per-cheat methods out is the highest-value slice of it.

## 5. Candidates to lift into project policy

These are conventions the project has already learned the hard way and currently keeps only
in commit messages and specs. `AGENTS.md` is where they belong.

1. **A game offset is declared once and imported.** No re-spelling a constant in a second
   module. Where a module needs another's offset, import it rather than repeat it. *(§2.3;
   the review found five spellings of one szarray layout.)*
2. **The arena is shared memory: read it before writing it.** Placement is by index in an
   append-only registry, never by sorted order; a stub is never written into a slot without
   establishing what is in it; a data offset is checked against the extents of every other
   allocation. *(Two collisions in v0.36.0: a stub written over a live one crashed the game,
   and an overlapping arm word made mining press the use button.)*
3. **Never assert on source text in a test.** Test behaviour, or use AST inspection where an
   architectural rule genuinely needs enforcing. *(§3.1; one such test broke on a refactor
   this week while the bug it guarded remained possible.)*
4. **A reported success must be observed, not inferred.** Do not report that an action
   happened because the request to perform it succeeded. *(Auto-catch reported "cast the
   line" on the strength of having armed the stub; the maintainer was the one casting.)*
5. **An instrument carries its own liveness control.** A probe that samples a paused game
   reports a clean, confident, meaningless result. Every probe verifies the game is
   advancing frames before it reports anything. *(Recorded in spec 042, applied since,
   never written as policy.)*
6. **State the rule for deferred imports, or stop deferring them.** ~25 function-level
   imports in `service.py` with no stated rule, two of them duplicating a top-level import.
7. **Mutation-check new tests, and record equivalent mutants as equivalent.** Already
   practised in the last two validation reports; it found a test passing for the wrong
   reason both times. It should be written down as expected rather than remembered.
8. **Shared test scaffolding lives in `conftest.py`.** A fixture copied into a third file is
   a fixture that belonged in conftest two files ago.

## 6. Reviewer findings rejected on inspection

Recorded so the same two are not re-raised.

- **`_clamp_vanity_slot(_value: int = 0)` "ignores its parameter"** (`patcher.py:747`). It
  does, deliberately: the leading underscore is the convention for a parameter kept to
  satisfy a call signature. Correct as written. `fishing_gear(index)` (§2.5) is the genuine
  version of this, because it is *not* underscore-prefixed and so reads as meaningful.
- **`test_auto_catch.py`'s reaches into `svc._seen_cast`** were flagged as fragile coupling.
  They are justified: the cast gate is internal state with no public accessor, and those
  tests are specifically about the gate's lifecycle. The lazy-shortcut version of this
  finding — `p._inj, p._sites = {}, {}` in `test_build_ledger.py:226` — stands.
