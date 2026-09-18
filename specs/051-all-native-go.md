# Spec 051: The rest of it in Go

**Status**: NOT STARTED — planning only. Nothing in this spec is implemented until the
maintainer has run the Go window from spec 050 against the live game and is satisfied
with it.

> **Note**: This work has no associated issue tracker ticket (personal utility).

Spec 050 moves the window to Go. This moves everything else: 9,686 lines of Python
across seventeen modules, and the 10,049 lines of tests that hold them honest. The
objective is a tool with no Python in it — one static CLI, one GUI, no interpreter, no
`requirements.txt`.

## Context

**What is left after spec 050**, by size:

| Module | Lines | What it is |
| --- | --- | --- |
| `patcher.py` | 1,976 | writes code stubs into the running game |
| `service.py` | 1,963 | the common layer every front end calls |
| `cli.py` | 1,231 | argparse front end; `--json` is the GUI contract |
| `xnb.py` | 492 | decode the game's XNB sprite containers |
| `inventory.py` | 438 | item slots, fields, stacks, prefixes |
| `sprites.py` | 419 | the PNG icon cache built from XNB |
| `tiles.py` | 312 | tile data |
| `content.py`, `locate.py` | 286 each | game content tables; find the player in memory |
| `version.py` | 247 | game build identification |
| `recipes.py`, `projectiles.py` | 206 each | recipe and projectile tables |
| `npcs.py`, `profile.py`, `selling.py`, `proc.py`, `projectile_edit.py` | 137–199 | the rest |

**The 572 KB of JSON in `terrariabonker/data/` does not move.** `items.json`,
`npcs.json`, `recipes.json`, `prefixes.json`, `prefix_stats.json`, `tiles.json` and
`tooltips.json` are data, not code. Go reads them as they are, and a diff of what each
language loads from them is the cheapest correctness check available.

**Why this is worth doing.** One language, one toolchain, one test command. A static
binary with no interpreter and no `pip install` to go wrong on a machine that is being
used to recover a game. It also removes numpy, whose only job is fast player-block
scanning — a plain Go loop over a `[]byte` is not slower than numpy, and does not need
to be installed.

**Why this is dangerous.** This is a working, playtested tool whose correctness depends
on offsets derived by hand for one game build (1.4.5.7) and on machine code written into
a live process. A rewrite that is 99% right is a tool that corrupts a save. Everything
below is arranged around not doing that.

## Design

### The privilege boundary does not move

AGENTS.md's rule stands after the rewrite, unchanged and for the same reason: the GUI
runs unprivileged and reaches memory only by shelling to the CLI under sudo. Go does not
make running a window as root acceptable. `serve` stays, its JSON-line protocol stays,
and `SERVE_OPS` stays — spec 050's Go window keeps talking to exactly the same wire,
and on the day the far side becomes Go it should not notice.

### Strangler, not big bang

Reimplement the CLI **one subcommand at a time**, keeping the Python one installed and
authoritative until each Go subcommand proves itself. The proof is differential: both
binaries run the same subcommand against the same synthetic memory image and their
`--json` output is compared byte for byte. angou already does this to hold its CLI output
stable (`tools/regress.sh` diffs against a previous commit's binary); the same technique
answers a harder question here — is the new implementation the same as the old one?

That requires the synthetic memory image the Python tests already use to be readable from
Go. It is the single most valuable asset in this port and the first thing to move.

### Order of work

1. **The test fixture.** Whatever builds the synthetic memory image for `pytest` becomes
   a file format both languages read, or a Go generator producing the identical bytes.
   Nothing else starts until a Go test can load the image the Python tests use.
2. **`proc` and `locate`.** `process_vm_readv`/`writev` and `/proc/<pid>/maps` from Go,
   then AOB/signature scanning and the player-block scan. Differential test: the same
   image in, the same address out.
3. **`layout`, `inventory`, `player`.** Offsets are declared once and imported —
   AGENTS.md is explicit that re-spelling a constant is the failure mode here (it was
   five spellings under four names once). In Go they are one package of typed constants,
   and the Python and Go values are diffed by a test while both exist.
4. **`content`, `recipes`, `npcs`, `tiles`, `prefixes`, `projectiles`.** Table loaders
   over the unchanged JSON. Differential test: every table, every row.
5. **`service`.** The common layer, on top of the above. Subcommand by subcommand.
6. **`cli`.** argparse to cobra, which the maintainer's other Go tools already use.
   `--json` output is diffed against the Python CLI's for every subcommand, and `serve`
   is reimplemented last so the GUI's wire is the final thing to change hands.
7. **`patcher`.** Left until last deliberately: it writes machine code into a running
   game, and it is the one module where being wrong costs more than an error message.
   Its stubs are architecture-specific byte sequences that move across as data, not as
   rewritten code.
8. **`xnb` and `sprites`.** Decoding the game's containers and building the PNG cache.
   Pillow leaves with them; Go's `image/png` writes the cache. Differential test: decode
   the same `Item_<id>.xnb` in both and compare the PNG bytes, or the pixels.
9. **Retire Python.** Delete the package and `requirements.txt`, rework `install.sh`,
   and move whatever remains of the 48 test files that is still meaningful.

### What the tests become

The 10,049 lines of Python tests are the specification of this tool's behaviour and must
not be thrown away in a translation. Each module's Go port lands with its Python tests
ported alongside it, and the differential harness runs both implementations until the
Python one is deleted. A Go module that passes its own translated tests but differs from
Python on the shared fixture has not passed.

## Acceptance criteria

_Not yet written in detail: this spec is a plan, and its criteria belong to the phases
that implement it. The shape they take:_

- [ ] AC1 No Python in the repository; no `requirements.txt`; `install.sh` installs two
  Go binaries.
- [ ] AC2 Every CLI subcommand's `--json` output is byte-identical to the Python CLI's
  for the whole differential corpus, checked while both existed.
- [ ] AC3 Every offset is declared once, in one Go package, and was diffed against the
  Python value before the Python was deleted.
- [ ] AC4 The synthetic memory image and the behaviour the Python tests pinned are both
  carried over; test count does not fall silently.
- [ ] AC5 `serve` speaks the same JSON-line protocol, and the spec 050 window works
  against the Go implementation with no change to its client.
- [ ] AC6 The maintainer has used it on a live game, including a patch and a restore,
  before the Python is deleted.

## Risks & Assumptions

- **Risk: this is the highest-risk work in the project.** The tool writes to a live
  game's memory. A subtly wrong port corrupts a character or a world, and the person it
  corrupts them for is the maintainer. The strangler order above puts `patcher` last for
  that reason, and nothing is deleted before its replacement has been used in anger.
- **Risk: offsets are build-specific and hand-derived.** Nothing in the port re-derives
  them; they move across as constants and are diffed. A game update during the port is a
  routine event that must not become a merge problem — one edit, not a hunt.
- **Assumption**: the Python tests' synthetic memory image can be made readable from Go
  without being regenerated. If it cannot, that is the first thing to fix, and it is
  worth fixing properly rather than working around.
- **Assumption**: no Python dependency does something Go cannot. numpy is a fast loop;
  Pillow decodes PNG; PyQt6 leaves with spec 050. XNB decompression is the one to check
  early — it is the only format work in the project.
- **Rollback**: each phase is revertible on its own while Python remains. After the final
  deletion, rollback is a revert of that commit; before it, the Python implementation is
  still installed and still authoritative.
