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

`tools/screenshot.sh` captures the panel; `--with-dialog` when a dialog is open. Four
images in `assets/`, one per tab, refreshed when the panel visibly changes.

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
