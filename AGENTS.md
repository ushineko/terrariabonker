# AGENTS.md — engineering conventions for terrariabonker

Guidance for anyone (human or AI agent) changing this repo. It is self-contained: it does
not depend on any external policy files. `CLAUDE.md` points here; `CONTRIBUTING.md` is the
PR-process summary for external contributors.

This project was extracted from a personal monorepo (`ag-scripts`) that used the "Ralph"
methodology. The **conventions** below are retained and enforced. The **Ralph workflow**
itself (spec files, validation reports) is a maintainer convention and is optional for
external contributors — see `CONTRIBUTING.md`.

---

## What this project is

A from-scratch live-memory trainer and item editor for **Terraria 1.4.5.7** (the Windows
build under Proton/wine-mono). It locates the player in `/proc/<pid>/mem` with no hardcoded
addresses, then reads and edits player state, inventory, and applies code-patch cheats. A
PyQt6 control panel and a CLI share one common layer. Cheat sites are derived with Cheat
Engine's mono dissector (see `ce/README.md`); nothing needs CE at runtime.

Some code-patch cheats are ported from the FearLess Forums "TerrariaReGrind" Cheat Engine
table. **Keep that attribution** in the README and About dialog for any ported cheat —
reverse-engineering credit for those hooks belongs to the ReGrind authors.

---

## Architecture (respect these boundaries)

- **Common layer** — `terrariabonker/service.py` (`Service`) and the modules it uses
  (`locate`, `player`, `inventory`, `patcher`, `recipes`, `sprites`, `profile`, `version`).
  All game logic lives here so the CLI and GUI cannot drift.
- **CLI** — `terrariabonker/cli.py`: a thin argparse front end; `--json` is the contract the
  GUI consumes. Memory operations self-elevate via `sudo` (`proc.elevate`).
- **GUI** — `terrariabonker/gui/`: runs **unprivileged** (Qt must not run as root) and reaches
  the common layer by shelling each action to the CLI under sudo (`gui/client.py` builds the
  argv and parses the JSON). Do not add in-process root memory access to the GUI.
- **State** — per-pid live patch state (`~/.config/terrariabonker/patches.json`) is
  concurrency-safe (flock + atomic write). The pid-independent desired-config profile
  (`~/.config/terrariabonker/profile.json`) drives auto-restore. Sprite icons cache under
  `~/.cache/terrariabonker/`.
- **Offsets are build-specific (1.4.5.7).** Locate by AOB/signature; never hardcode a JIT
  address. Re-derive with the `ce/poc_*.lua` recon scripts after a game update. A moved
  layout must fail safe ("anchor not found"/no match), never mis-write.
- **An offset is declared once and imported.** `terrariabonker/layout.py` owns the mono
  szarray shape and `Main`'s static offsets; item field offsets live in `inventory.py`. A
  module that needs another's offset imports it. Never re-spell a constant in a second
  module — a game update is the routine event here, and re-deriving one number should be one
  edit, not a hunt for every spelling of it. (It was five spellings under four names once.)

---

## Writing to a live game (patches, stubs, probes)

These are rules the project learned by breaking something. Each one has a scar.

- **The arena is shared memory: read it before you write it.** Stub slots are assigned by
  position in an append-only registry (`_SLOT_ORDER`), never by sorted name — appending must
  not move an existing slot. Never write a stub into a slot without establishing what is
  already there (`_check_slot`), and never write a jump over bytes that are not what will be
  restored (`_check_site`). Data offsets must be checked against the extents of every other
  allocation, with a test that computes them rather than trusting a comment.
  *Two collisions came from skipping this: a stub written over a live one crashed the game,
  and an overlapping arm word made mining press the player's use button.*
- **A probe carries its own liveness control.** Terraria pauses in single-player when its
  window loses focus, and a paused game rewrites nothing — so a probe that samples one gets a
  clean, confident, meaningless result. Every probe verifies the game is advancing frames
  before it reports anything, and says "aborting: the game is paused" rather than reporting a
  measurement it did not take.
- **Report what you observed, not what you requested.** A successful write is not a
  successful action: arming a stub is not a press, and a press is not a cast. Confirm the
  effect (a bobber appeared, a tile is gone, the region is mapped) before reporting success.
  *Auto-catch logged "cast the line" on the strength of having armed the stub; the maintainer
  was the one casting.*
- **Locate by identity on every access, never by a cached address.** mono's GC moves objects,
  and a stale pointer writes into whatever now lives there. Treat an unexpected pre-value as a
  reason to abort, not a value to record.

---

## Establishing a fact about the game (recon)

**Measure first, then reason.** Every rule below is a model that was reasoned into place,
held confidently, and turned out to be wrong. Reasoning is for deciding what to measure and
for explaining a measurement — not for standing in place of one.

- **Ask the game before inferring.** The runtime knows its own field offsets
  (`tools/monofields.py`), and the assembly knows its own behaviour (`tools/ilrecon`).
  Prefer either over declaration order, template diffs, value-signature matching, or
  watching a number change. Run `sudo python3 tools/monofields.py --verify` when touching
  offsets; it exits non-zero on disagreement.
  *`Projectile.active` was inferred from the shared `Entity` layout and read `Entity.wet`
  for eight releases.*
- **A constant cannot be validated by a test that reads it from that constant.** A fixture
  planted through an offset and read back through the same offset proves only that the
  offset equals itself. Offsets derived from the game are pinned as **literals** in a test,
  with their provenance in the docstring.
  *617 tests passed against the wrong `active`; a mutation moving `crit` one field along
  passed the whole suite in v0.39.0.*
- **A tie among candidates is not a weak answer, it is no answer.** Scoring offsets against
  a field the game almost always declares the same way ranks coincidences.
  *`hostile` tied three ways and was written down as `0x030`. It is `0x0C8`, and all three
  tied candidates were other fields entirely.*
- **The field that names the behaviour may not govern it.** Read the AI, not the field list.
  *The Book of Skulls crosses only a few tiles despite `tileCollide = 0`, because `AI_001`
  drains 33 ticks of `timeLeft` per tick spent inside solid tile. `AI_001` writes
  `tileCollide` in 13 places and reads it in none.*
- **Record the before-value.** Enforcing a value something already holds is a null
  operation that is indistinguishable from a field that does nothing.
  *An afternoon of A/B runs forced `tileCollide = 1` on a projectile the game had already
  set to 0.*
- **Widths matter as much as offsets.** Many of these fields are single-byte bools packed
  against neighbours (`reflected` sits immediately after `hostile`), so a 4-byte write
  corrupts the field next door.
- **When a measurement disagrees with the person watching the screen, suspect the
  instrument.** Report it as "the probe saw nothing", never as "nothing happened".
  *The maintainer was told twice that they were not firing. They were firing both times;
  the probe was filtering on the wrong byte.*

---

## Coding standards

**Python**
- Target the system interpreter (`/usr/bin/python3`), not conda/miniforge. Python 3.10+.
- Runtime deps: `numpy`, `PyQt6`, `Pillow` (see `requirements.txt`). Ask before adding a new
  runtime dependency; prefer the standard library.
- Style: PEP 8, 4-space indent, ~100-col soft limit, double-quoted strings to match the
  codebase. Keep changes surgical — edit only what the task needs; do not reformat unrelated
  code or remove pre-existing (unrelated) dead code.
- Lint with `flake8` before committing changed files.
- **Imports go at the top of the module.** Defer one into a function only to break an import
  cycle or to keep an optional/expensive dependency out of a fast path — and say which in a
  comment. Without a stated rule they accumulate: `service.py` currently has 10 top-level
  imports and **59** deferred ones, some duplicating a top-level import of the same module.
  New code follows the rule; the existing 59 are not a licence to add the 60th.

**Bash / shell**
- `set -euo pipefail` in scripts; quote expansions; prefer explicit argv over shell strings.

**System integration** — prefer stable programmatic contracts over brittle CLI parsing:
D-Bus interfaces (BlueZ, NetworkManager, UPower, KWin) or typed bindings first; JSON-output
CLIs (`pactl --format=json`) next; human-readable CLI output only as a documented last resort.

**KDE Plasma / Wayland** (this is a KDE Wayland desktop app):
- `Qt.WindowStaysOnTopHint` is not honored on KDE Wayland — use a KWin rule matched by
  `wmclass`/title plus the Qt hint as a fallback.
- Restoring a window's on-screen position uses the KWin Scripting D-Bus API, not
  `QWidget.move()`/`pos()` (both unreliable under KWin). See existing helpers if adding this.
- X11 tools (`xdotool`, `wmctrl`, `xprop`) do not work on Wayland.

---

## Testing

- `pytest`, kept in `tests/`. Tests run headless with no game and no root against a synthetic
  memory image (`tests/conftest.py` `FakeMem`) — keep it that way so CI/others can run them.
- Tests encode **behavioral contracts**, not implementation details. A test that breaks on a
  pure refactor (no behavior change) is testing the wrong thing.
- Avoid over-mocking. When a change touches a real boundary (a `/proc` read/write path, the
  patch-state file, the profile), cover the real behavior against the synthetic image rather
  than asserting on mocks.
- Live verification against a running game is fine for the maintainer, but every change must
  also be covered by a headless test.
- **Never assert on source text.** No `inspect.getsource` and no substring checks against a
  module's own code: they pass when the line sits in a branch that never runs, and fail on a
  rename that changes nothing. Test behaviour. Where an *architectural* rule genuinely needs
  enforcing (no threads in the GUI transport, no JSON parsing outside `client.py`), read it
  from the AST — `test_view_parity.py` and `test_gui_helper.py` show the technique.
- **Mutation-check a new test: break the thing it guards and watch it fail.** A test written
  after the fix routinely passes against the bug it was meant to catch. When a mutation
  survives, say whether the test was weak or the mutant was *equivalent* — an equivalent
  mutant is a finding about the code (something is redundant or unreachable), not a pass.
  Both outcomes are worth recording in the validation report.
- **Shared scaffolding lives in `tests/conftest.py`.** `qt_app` and `gui_window` are there;
  so is `FakeMem`. A fixture copied into a third file belonged in conftest two files ago —
  and the copies drift: seven `QApplication` fixtures under two names is how three GUI test
  modules ended up passing only because another module's import ran first.
- **Refactors are pinned before they start.** Before restructuring a path that writes to the
  game, add characterization tests for whatever is only covered incidentally, then re-run
  them as mutations *after* the split. A green suite following a "behaviour-preserving"
  change is a question, not an answer: a `catch_tick` split once moved a `break` out of a
  guard, changing when the loop gave up, and all 579 tests still passed.

## Security (non-negotiable minimums)

- No hardcoded secrets/credentials; none in logs or error messages.
- No `eval`/`exec` of dynamic input, no shell-string injection (spawn with explicit argv
  lists), no network calls added without discussion.
- The tool writes to your **own** single-player game's memory over a sudo `/proc` path; it
  ships no game assets (sprites are decoded from the user's own install into a local cache).
- Run a dependency scan (`pip-audit`) when changing dependencies; record the tool + version.

## Writing style (READMEs, specs, dialogs, PR text)

- Factual and plain. No superlatives or marketing ("blazingly fast", "seamless", "powerful").
- Neutral voice; avoid first-person in durable docs.
- State assumptions and uncertainties; don't hide them behind confident phrasing.
- Don't assert what you haven't checked. A plausible mechanism written as fact gets cited
  as justification later, and is then hard to question.

### The README specifically

Written for someone who plays Terraria and knows nothing about reverse engineering. Keep
it crisp; the depth belongs in `specs/`, `docs/discovery.md`, `ce/` and the code, which
already carry it.

- **Describe cheats by effect, not mechanism.** "Pull items in from off-screen", not the
  method it hooks.
- **No internals.** No method or field names, struct offsets, code caves, AOBs, JIT, mono,
  stubs, anchors, or byte patterns.
- **No discarded or alternate approaches.** "We tried X and it didn't work" is spec
  material. The README says what the thing does now.
- **No implementation names as features.** "A GUI control panel", not "a PyQt6 control
  panel". Frameworks appear only where you must install them.
- **No defensive framing.** Not "no Cheat Engine required"; just say what it uses.
  Generalise tool names where the specific one doesn't matter.
- **Don't justify instructions** unless the reader would act differently knowing the
  reason. "Run it under Proton" needs no essay on JIT codegen.

Keep what changes what the user does: requirements, safety warnings, anything
irreversible, and behaviour that will surprise them (an edit that doesn't survive a
reload, a cheat that permanently alters their world).

### Screenshots

`tools/screenshot.sh` captures the panel; `--with-dialog` when a dialog is open. Five
images in `assets/`, refreshed when the panel visibly changes. The Player tab is left out
deliberately: it is two group boxes of buttons and shows nothing a reader needs.

- The alt text describes **what is actually in the image**. It is the only description a
  screen-reader user gets, and a stale one is worse than none — check it still matches
  before committing a new capture.
- Switch tabs by hand between runs; the script raises the panel itself, so there is no
  need to leave it focused.

## Commits, versioning, releases

- **Commit messages must NOT contain AI-attribution or `Co-Authored-By` trailers** (no
  "Generated with …", no "Co-Authored-By: …"). This applies to PR/MR descriptions too.
- Conventional-style subjects: `feat(...)`, `fix(...)`, `refactor(...)`, `docs(...)`.
- The version lives in `terrariabonker/__init__.py` and the About dialog/titlebar. Bump it in
  the same commit as the change (maintainer confirms the number). Semver-ish: features → minor,
  fixes → patch.
- Release tags are annotated, unscoped `vX.Y.Z`, pushed after the version-bump commit lands on
  `main` (`git tag -a vX.Y.Z -m "…" && git push origin vX.Y.Z`).

## Running

- CLI: `python3 terrariabonker.py <command>` (memory commands self-elevate via sudo). The GUI
  needs **passwordless sudo** for its memory actions (it can't answer a prompt in a
  subprocess); without it the GUI degrades with a warning and the recipe browser/icons still
  work. See the README "Requirements".
- Before running a background/GUI instance for debugging, kill stale ones so logs are
  clean — but **not** with `pkill -f terrariabonker`: `-f` matches the whole command line,
  including the shell that is running the pkill, so it kills the session issuing it. Match
  the exact argv instead:

  ```bash
  for p in $(pgrep -x python3); do
      tr '\0' ' ' < /proc/$p/cmdline | grep -q 'terrariabonker.py gui' && kill "$p"
  done
  ```

  The same trap applies to `pgrep -f` when hunting for the game or a worker.
