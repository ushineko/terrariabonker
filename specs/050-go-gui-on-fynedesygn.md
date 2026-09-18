# Spec 050: The GUI in Go, on fynedesygn

**Status**: INCOMPLETE — phases 1-5 are done and installed. Phase 6 (retiring the Qt
window) waits on AC10: the maintainer's live session. Until then both panels are
installed and they exclude each other, so the Qt one is still there to fall back to.

> **Note**: This work has no associated issue tracker ticket (personal utility).

Replace the PyQt6 control panel with a Go one built on
[fynedesygn](https://github.com/ushineko/fynedesygn), the Fyne design system three of
this maintainer's other programs already run on. The Python common layer, the CLI and
every memory operation are untouched: this moves the window, not the trainer.

It is the first half of going all-native Go. Spec 051 is the second, and the GUI goes
first because it is the only part already separated by a contract — `gui/client.py`
defines every command the window may send and `tests/test_view_parity.py` fails the
build when that drifts from the real CLI parser.

## Context

**The window is 3,912 lines of PyQt6 across ten files**, against 9,686 lines of Python
that actually does something to the game. It is the largest single-purpose dependency
in the project: PyQt6 is pulled in for the GUI alone, and nothing else in the tool
needs a toolkit.

**The boundary it sits behind is already the right one.** AGENTS.md states it:

> **GUI** — `terrariabonker/gui/`: runs **unprivileged** (Qt must not run as root) and
> reaches the common layer by shelling each action to the CLI under sudo
> (`gui/client.py` builds the argv and parses the JSON). Do not add in-process root
> memory access to the GUI.

That rule is about privilege, not about Python, so it survives this port and survives
spec 051 as well. A Fyne window must not run as root any more than a Qt one must — on
Wayland it is worse, not better. The Go GUI will shell to the CLI under sudo exactly as
the Qt one does, know nothing about memory, and hold no offset.

**Two transports already exist and both are easy in Go.** A long-lived
`sudo terrariabonker serve` reads one JSON request per line —
`{"id": N, "argv": [...]}` — and writes one reply per line —
`{"id": N, "ok": bool, "out": str}` — against a `Service` whose locate caches stay
warm. That is the difference between ~2.7 s and ~2.5 ms per read, which is what makes
the 1 Hz inventory sync affordable. `SERVE_OPS` whitelists what it will serve;
everything else, and any fallback when the worker will not start, is a one-shot CLI run
under sudo. The worker exits on stdin EOF, so it cannot outlive the window that started
it.

**The icons are already files.** `sprites.py` decodes the game's `Item_<id>.xnb` into a
PNG cache under `~/.cache/terrariabonker/<version>/`, with a `.done` marker. Go loads
PNGs from disk with the standard library; Pillow is a build-time dependency of the cache,
not a runtime dependency of the window. Until spec 051 the cache is still built by
`extract-sprites`, which the window already knows how to invoke.

**What the window is.** Seven tabs — Player, Effects, Projectiles, Patches, Inventory,
Recipes, Compendium — plus an item-picker dialog, an inventory grid, a single-instance
guard, and UI state that must tolerate a root-owned config directory (the CLI creates it
under sudo).

## Design

### What changes and what does not

| | |
| --- | --- |
| Replaced | `terrariabonker/gui/` — 3,912 lines of PyQt6 |
| Untouched | `service.py`, `patcher.py`, `cli.py`, `locate.py`, `inventory.py`, `xnb.py`, `sprites.py`, every offset, every test of them |
| Unchanged contract | the CLI's `--json` replies, `serve`'s JSON lines, `SERVE_OPS` |
| Removed dependency | PyQt6 |
| New dependency | Go 1.26, Fyne 2.8.1 and fynedesygn v0.1.5, for the GUI only |

The repository becomes Python plus Go: `terrariabonker/` stays as it is, and the window
is `cmd/terrariabonker-gui` over `internal/gui`, with its own `go.mod`. One version
number, one `install.sh`, one spec series.

### Navigation, not tabs

fynedesygn's shell is a left-hand section list, not a `QTabWidget`. The seven tabs become
seven sections in the same order. This is the design system's shape and the other three
programs on it read the same way; it also gives the window a status bar, which the Qt one
does not have and which this tool wants — whether the game is attached, which world is
loaded, whether the privileged worker is up.

### The wire

`internal/gui/client` is the Go counterpart of `gui/client.py`: one place that knows the
contract, toolkit-free, one function per operation returning argv, one parser per reply.
`internal/gui/helper` is the counterpart of `gui/helper.py`: one `sudo terrariabonker
serve` process, requests keyed by id, replies read from a scanner on stdout, falling back
to a one-shot CLI run when the worker will not start or refuses a command.

Every call goes through the shell's `Perform`/`Load`, so the window shows progress and
reports failures as banners rather than sitting still — and a long operation that can be
cancelled puts its Cancel on the busy popup (`shell.BusyCancellable`).

### Phases

Each phase ends in a window that builds, runs and is worth opening. The PyQt6 GUI keeps
working until phase 6, because it is one file and cannot be half-deleted; phase 6 removes
it in one commit.

1. **Skeleton and wire.** `go.mod`, `cmd/terrariabonker-gui`, the shell, the status bar,
   single-instance, UI state, the `serve` transport with its CLI fallback, and the
   **Player** section (status, HP/mana, max HP/mana).
2. **Effects and Patches.** fast-mining, long-reach, potions, fishing, fishing-buffs,
   catch, vein/extract, the patch table, `patch set`, `restore`.
3. **Inventory.** The 1 Hz sync, the icon grid off the PNG cache, `set-stack`,
   `set-item`, `give`, the item picker and prefixes. The largest piece.
4. **Recipes and Compendium.** The tables, their icons and filters, `build-check`,
   `accept-build`, `spawn-npc`, and the wiki links (`xdg-open`).
5. **Projectiles.** `projectile-of`, `projectile-tick`, `projectile-stop`.
6. **Retire the Qt window.** Delete `terrariabonker/gui/`, drop PyQt6 from
   `requirements.txt`, rework `install.sh` and the desktop entry, move the view-parity
   test to the Go side, update README and AGENTS.md, bump the version.

### The parity test must survive the port

`tests/test_view_parity.py` checks every argv `client.py` can emit against the real CLI
parser, so a missing `--json` becomes a test failure rather than a runtime gap. That
property is worth more than the test's current implementation. In phase 6 it becomes a Go
test that runs the installed `terrariabonker` and asserts the same thing — every argv the
Go client can build is accepted by the Python parser, and every op it sends to the worker
is in `SERVE_OPS`. Losing this check is not an acceptable outcome of the port.

## Acceptance criteria

- [x] AC1 `cmd/terrariabonker-gui` builds with `CGO_ENABLED=1` against fynedesygn
  v0.1.9 — the port drove five library releases, four of them fixes it found — and
  `go.mod` names no `replace`.
- [x] AC2 The window runs unprivileged. No Go file reads or writes another process's
  memory, opens `/proc/*/mem`, or names a game offset; every privileged action is a
  `sudo terrariabonker …` subprocess (AGENTS.md architecture rule).
- [x] AC3 The seven sections are present in the Qt window's order — Player, Effects,
  Projectiles, Patches, Inventory, Recipes, Compendium — and each renders headlessly in
  every colour scheme.
- [x] AC4 The `serve` transport works: one worker under sudo, requests keyed by id,
  replies matched to their request, EOF on exit. When the worker will not start or
  refuses an op, the same call runs as a one-shot CLI and the window says so.
- [x] AC5 Every argv the Go client builds is accepted by the real CLI parser, and every
  op it sends to the worker is in `SERVE_OPS` — asserted by a test, not by reading.
- [x] AC6 Inventory syncs at 1 Hz off the warm worker without blocking the UI thread,
  and the icon grid draws from the existing PNG cache with no Python at runtime.
- [x] AC7 A root-owned `~/.config/terrariabonker` does not break UI state or the
  single-instance guard (the CLI creates that directory under sudo).
- [x] AC8 `go test ./...`, `go vet ./...` and the pinned golangci-lint are clean; the
  Python suite still passes unchanged.
- [ ] AC9 `terrariabonker/gui/` is deleted, PyQt6 is out of `requirements.txt`, and
  `install.sh` installs and launches the Go window (phase 6).
- [ ] AC10 The maintainer runs it against the live game and reports what is wrong.

## Risks & Assumptions

- **Assumption**: the CLI contract is complete and honest. `client.py`'s 45 functions and
  the parity test are the specification; anything the Qt window does outside them is a
  gap this port will find. Recorded as it is found, not worked around.
- **Risk**: the 1 Hz inventory sync is the performance-sensitive path. Fyne draws on one
  thread and the shell's rule is that work hops to it with `fyne.Do`; a grid of ~50 icon
  cells rebuilt every second must not rebuild widgets it can update in place.
- **Risk**: sudo's password prompt. The Qt window uses `sudo -n` and reports the failure
  rather than hanging on a prompt with nowhere to type. The Go window must do the same;
  a GUI that blocks invisibly on a TTY prompt is the worst failure mode here.
- **Risk**: this is a trainer for a live game. The window is not where the danger is —
  no offsets move — but a wrong argv is a wrong write. AC5 exists for that.
- **Rollback**: until phase 6 the Qt window is untouched and still installed; after it,
  `git revert` of the phase-6 commit brings it back.

## What the port found

Three behaviours the phase list did not name, each found by reading the Qt window rather
than `client.py`, because `client.py` is the boundary for operations and not for data:

- **The build gate** (spec 036). The patches are byte patterns derived against one exact
  build, so a game update has three outcomes and the window has to know which before it
  writes anything. It needed a two-second status poll, which the Go window did not have.
- **Auto-restore** (spec 049). A patch lives in the running process, so a restart or a
  world load clears every one of them. It hangs off the same poll.
- **The single-instance lock**. Two panels start two privileged workers and two
  auto-restore loops on one game, and they fight.

And three tables the Qt window imported in-process, which a Go window cannot: the patch
catalog, the modifier list and the recipe book. Each became a read-only CLI dump rather
than a second spelling of a table this repository already holds once.
