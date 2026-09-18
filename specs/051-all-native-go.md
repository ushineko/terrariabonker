# Spec 051: The rest of it in Go

**Status**: IN PROGRESS. The window from spec 050 has been run against the live game and
what it found is fixed, so this starts. Spec 050's phase 6 — retiring the Qt window —
moves here as step 0, because the Python it would have to keep working is the Python this
spec deletes.

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

**The 572 KB of JSON does not become code.** `items.json`,
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

Ordered so that each step is verifiable on its own and the dangerous one is last. The
first two steps need no memory access at all, which makes them the cheapest place to
build the differential habit this port depends on.

0. **Retire the Qt window** (spec 050's phase 6). `terrariabonker/gui/` is 3,912 lines
   of PyQt6 that nothing runs any more. It does not come here on its own: 272 of the
   suite's 739 tests import it, and most of those are not testing a window — they use
   `gui/client.py`, which is 476 lines of argv builders with no PyQt6 in it, as the
   vocabulary for driving the common layer.

   So the window goes and that vocabulary stays, as `terrariabonker/argv.py`: the CLI's
   own argument contract, which is what it always was. The tests that test a window go
   with the window; the tests that drive the service through it keep working. PyQt6
   leaves `requirements.txt` in the same commit.

1. **The data tables.** `names`, `prefixes`, `recipes`, `npcs`, `tiles`, `content`,
   `projectiles` — loaders over the unchanged JSON in `data/`. No memory, no privilege,
   no fixture needed, and a differential test per table per row.

   **`names`, `prefixes` and `recipes` are done.** `internal/game` reads the embedded
   tables; every one of the 6,195 names and tooltips, every modifier's name, quality and
   effect, all 32 class combinations' modifier pools, and all 3,603 recipes with their
   stations and tile icons are checked against what the Python makes of the same bytes.

   The window read all three through the CLI at start-up and now reads none of them:
   three subprocesses gone, and every section is drawn with names, modifiers and recipes
   the first time rather than redrawn when a subprocess answers. `RecipesArgv`,
   `PrefixesArgv`, `NamesArgv` and their parsers went with them.

   The modifier pools are the interesting ones. They are not extracted data -- they are
   Terraria's own categorisation, written down by hand -- so porting them means the same
   constants spelled twice, which is this project's oldest failure mode. Every id's
   quality and every combination's pool is compared, and the comparison was
   mutation-checked by breaking one range and one set.

   This is also the step that pays immediately: the Go window shells out to the CLI four
   times at start-up for names, prefixes, recipes and the patch catalog, and those four
   round trips have no reason to exist once Go can read the same files. The window keeps
   working through the CLI until each one lands.

2. **The test fixture.** Nothing that touches memory starts until a Go test can build
   the image the Python tests use.

   **Done**, and it turned out to be smaller than the spec assumed. There is no image
   file: `tests/conftest.py`'s fake is a buffer at a base address with a handful of
   helpers that plant structures into it. So the asset to carry across is not a blob to
   load but *the shape of those structures* -- where a mono string keeps its length, that
   the length counts UTF-16 code units rather than bytes, how far below a player's life
   field the name pointer sits. `internal/memtest` plants the same things and a test
   compares the buffers byte for byte.

   Those offsets are facts about the game that now exist in two languages, which is the
   failure mode AGENTS.md names. The comparison is what holds them together, and it was
   mutation-checked by moving the name pointer four bytes and counting bytes instead of
   code units.
3. **`proc` and `locate`.** `/proc/<pid>/mem` and `/proc/<pid>/maps` from Go, then
   AOB/signature scanning and the player-block scan. Differential test: the same image
   in, the same address out.

   **The player scan is done.** `internal/locate` reads a Mem -- a real process for the
   CLI, a planted buffer for a test -- and every rule in it is compared with the Python's
   over the same bytes: what counts as a life and mana block, across fifteen cases
   including each edge the rule has a reason for; what counts as a name; and a scan of a
   region with two player copies and a near miss in it. Mutation-checked by requiring the
   boosted cap to equal the permanent one, which is the bug the headroom exists for, and
   by moving the name offset four bytes: eight failures.

   **`proc` is done too**, and more of it was testable than expected. A maps listing is
   messier than anything that would be written by hand, so the parser is handed a real
   one -- the Python interpreter's, a 64-bit process -- and compared with the Python's
   reading of the same text. Finding the game is compared against the running one.

   Addresses are `uint32` throughout. The game runs as the 32-bit Windows build and every
   pointer in its structures is four bytes, so the narrow type makes a truncation bug
   impossible rather than latent. Measured on the running game: 1,473 regions, 1.59 GiB,
   **none above 4 GB** -- it is a true 32-bit process, not a 64-bit one hosting 32-bit
   code, so the parser's skip of a wider address never fires on the target and exists for
   listings that are not the game's.

   **Checked against the live game**, which is the only evidence that matters here: both
   implementations scanned the same Terraria process (1,466 regions) and returned the
   same two player copies -- the same addresses, the same names, the same blocks, the live
   copy and its inert snapshot. Go took 1.01 s to the Python's 0.82 s, which is numpy's
   vectorised prefilter against a plain loop and is not worth anything yet.

   **The live-player resolver is done**, which finishes this step. `find_players` returns
   the live player *and* one or two inert snapshots, and writing to a snapshot looks
   exactly like the trainer not working: the number changes in memory and the game never
   reads it. Telling them apart by watching which one moves needs the game running
   frames, and it usually is not -- Terraria pauses when its window loses focus, which is
   what happens the moment anyone clicks in the trainer. So the live one is resolved
   instead: `Main.get_LocalPlayer` is `return Main.player[Main.myPlayer]`, its JIT'd tail
   is a distinctive index-and-return shape, and the two `mov reg,[abs]` in front of it
   carry the addresses of the two statics that lead to the player the game itself uses.
   Found once and kept; re-read cheaply through the kept anchor, which self-corrects when
   a collection moves the object.

   That search reads executable memory, which the writable-region scan deliberately does
   not cover, so `proc` grew a second region listing -- and the two are compared against
   the game's own maps rather than the interpreter's, because a 64-bit interpreter maps
   every scrap of its code above four gigabytes and the comparison would have had nothing
   left in it.

   **Checked against the live game.** Both implementations resolved the same anchor, the
   same `Main` static base, the same player address, name and block, over the same 1,492
   executable regions; Go's scan took 414 ms to the Python's 1,433 ms. The scan at that
   moment found two candidates and the *first* was the inert one -- full life -- while
   the live player was the second, which is the whole reason this path exists.

   Mutation-checked on eight guards: the pattern bytes, the two static offsets, the
   `statLife` offset, the array data offset, the index scale, the uniqueness rule that
   makes two candidates a refusal rather than a coin toss, the check on the instructions
   in front of the tail, and the bound on `myPlayer`. Each one breaks a differential test
   when broken.
4. **`layout`, `inventory`, `player`.** Offsets are declared once and imported —
   AGENTS.md is explicit that re-spelling a constant is the failure mode here (it was
   five spellings under four names once). In Go they are one package of typed constants,
   and the Python and Go values are diffed by a test while both exist.

   **This step is done.** `internal/layout` is every number: Main's block, a player's
   fields and an item's, fifty-six of them. The Python spreads them over three modules and
   re-exports some, so the test compares against the union of the three, **both
   directions** -- every Python constant must exist in Go with the same value and every Go
   entry must exist in Python -- and a name that disagrees with itself across the Python's
   modules fails there too. A name declared twice on the Go side panics at startup rather
   than letting one value quietly win.

   `internal/player` and `internal/inventory` are the readers and writers. Neither caches
   anything: the game writes to these fields continuously, and the `Item[]` pointer moves
   when the managed heap collects, so a pointer held from a second ago addresses whatever
   is there now.

   The tests assert nothing either implementation produced. A write into a running game
   that is one field out is not an error message -- it lands in whatever the game keeps
   next door -- so every write case runs the same operations over the same planted image
   in both languages and compares **the whole buffer**. The image is described once and
   planted twice, each side looking its offsets up by name in its own table, so a fixture
   cannot paper over an offset one of them has wrong.

   Fourteen mutations were checked against the inventory and four against the player, and
   two of them found real gaps rather than confirming coverage: a planted life block whose
   values were not all distinct read the same from the wrong offset as from the right one,
   and nothing had planted a held-slot value outside the hotbar.

   One bug in the Python fell out of it. `_item_addr` returns 0 for a slot holding no
   object, every caller tests it for truth, and `apply_prefix_stats` tested it for `None`
   -- so a modifier applied to an empty slot took the writing path and wrote each field at
   its own offset counted from address zero. Fixed there as well as here.
5. **`service`.** The common layer, on top of the above. Subcommand by subcommand.

   **The core is done**: locating, believing a copy, reading it, writing to all of them.
   That is the part where being wrong is silent. The game keeps the live player and one or
   two load-time snapshots; writes go to all of them so the live one is always hit, and
   reads come from the live one alone, because a snapshot holds whatever the slot held
   when it was taken and reporting that is reporting fiction. `locate.PickLive` came over
   with it, as the fallback for when the resolver cannot answer.

   The differential fixture is a game with **three** player copies, one live and a
   snapshot either side of it, and their inventories all differ. Both of those placements
   are load-bearing. The live copy in the middle means a reader that takes the first copy
   is caught and a writer that reports whichever copy it finished with is caught; with the
   live copy at either end, both of those pass by accident. Neither fallback can reach the
   live copy either -- every copy is below its life cap, so the activity guess refuses, and
   the snapshots are no poorer, so the inventory guess picks one of them -- so the tests
   fail if ground truth stops being consulted.

   That mattered. The first fixture had an extra pointer hop in its planted
   `get_LocalPlayer`, so the resolver never resolved anything and the activity fallback
   was quietly returning the right answer; every test passed. The mutation check found it.

   Eight mutations checked, four of which needed the tests strengthening rather than
   confirming them: the resolver being consulted at all, the cache being re-validated, a
   missing anchor forcing a rescan, and a copy that becomes somebody else being dropped.

   Still to come here: the build gate, the world and tile operations, fishing, auto-catch,
   selling and the rest, which all sit on modules that arrive in the later steps.
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

### What this port will not make faster

Worth writing down before anyone expects it. Applying a patch takes tens of seconds, and
measurement through the warm worker says the language is not why. With the game running:
`status` 3 ms, `inventory` 3 ms, `patch status` 2 ms, `build-check` 3 ms — and 3.7 s for
the first status, which is the player scan, and 1.4 s for the 3.2 MB compendium.

The wait when a patch is applied is the arena bootstrap: it hangs a springboard on a
per-frame path and waits up to 20 s for the game to run it. Terraria pauses in
single-player whenever its window loses focus, which is exactly what clicking in the
trainer does. A Go patcher waits for the same frame.

Two things do help, and neither is a rewrite: applying several patches on one press,
which spec 050 added, and saying *while waiting* that the game is paused rather than
after the wait fails. `Patcher.arena` already takes an `on_wait` callback for it.

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
