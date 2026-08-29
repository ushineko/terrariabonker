# Spec 049: Notice when the world changes

**Status**: INCOMPLETE — shipped in v0.41.1 at the maintainer's direction with one
criterion outstanding. 738 tests pass. `world_id()` is confirmed against the running game
(`status --json` reports `['Royal Brewery of Maggots', 4200, 1200]`), and the restore-timing
work is measured and done; what has **not** been observed is a world switch with the
trainer open putting the cheats back on its own. Committed rather than held because the
pieces are independently useful and the outstanding check needs a play session.

> **Note**: This work has no associated issue tracker ticket (personal utility).

Switching worlds with the trainer open leaves the saved cheats un-applied: the player is
rebuilt from the save file and nothing puts the edits back until the trainer is restarted.
Reported by the maintainer. Fixing it turns out to fix a second bug that shipped in
v0.41.0.

## Context

**Nothing in this codebase knows which world is loaded.** That single gap causes both
faults below.

**1. Auto-restore is keyed on the process id.** `MainWindow._maybe_restore` re-applies the
profile only when the pid changes — its docstring says so: "when a fresh in-world game is
detected (any new pid)". A world switch keeps the same process, so it never fires. Code
patches live in the process and survive; the player-state edits do not, because the game
rebuilds the player from the save.

**2. Auto-sell's piggy-bank cache can carry an answer between worlds (shipped in
v0.41.0).** `Service._world_key` is `(tile buffer address, max_x, max_y)`. `AGENTS.md`
already records that the tile buffer is allocated at the largest supported size and
*reused*, so two worlds of the same dimensions produce the same key.

This was **measured, not reasoned about**. Snapshotting `Main`'s statics either side of a
real switch:

| | before | after |
| --- | --- | --- |
| world name | The Lousy Yeet | Royal Brewery of Maggots |
| dimensions | 4200x1200 | 4200x1200 |
| tile buffer | 0x07780c28 | 0x07780c28 |

The key is byte-identical across the switch. The consequence is that auto-sell can decide
coins are reachable on the strength of a piggy bank placed in a world the player has left,
and pay into a bank they cannot open.

## Recon

Diffing `Main`'s 0x4000-byte static block across the switch: 139 changed dwords, and
exactly one that identifies the world.

- **`MAIN_WORLD_NAME_OFF = 0x660`** — `Main.worldName`, a mono string. Read 'The Lousy
  Yeet' and then 'Royal Brewery of Maggots', and both match their world files
  (`The_Lousy_Yeet.wld`, `Royal_Brewery_of_Maggots.wld.bak`), which is what distinguishes
  it from "some string that happened to change".
- The GUID at `+0x0148` did **not** change, so it is a session or client id, not a world
  one. Recorded so nobody else has to rule it out.
- **`Main.worldID` was not pinned.** `+0x0160` (116799 -> 128925) is a plausible candidate
  and nothing more; the world file parse that would have confirmed it did not read this
  build's header format, and a guessed constant is exactly what this project has scars
  from. The name is used instead, and this is left open.

## Design

One primitive, `Service.world_id()`, returning `(name, max_x, max_y)` or None when no world
is loaded. Both callers key off it. Nothing else about either feature changes.

**Why the name and not just the dimensions**: the dimensions alone do not distinguish two
worlds of the same size, which is the case that was actually measured.

**Known limitation, stated rather than hidden**: two worlds with the same name *and* the
same dimensions collide. Terraria permits duplicate names. The failure mode is the one that
exists today — a stale answer — not a bad write, and pinning `worldID` would close it.

**The restore trigger** becomes "the pid changed **or** the world changed". The retry
budget resets on a world change the same way it does for a new pid, because the same
lazily-JIT'd cheats need the same retries.

## Why restoring is slow, measured

The maintainer reported that after launching the game from the trainer and loading a
world, the cheats take "a long time" to come back. Measured on a real cold start:

| time | state |
| --- | --- |
| 00:08:47 | game process up |
| 00:09:55 | **9 of 15** cheats applied |
| ~00:10:05 | all 15 |

About **80 seconds**. Two causes, and only one of them is a fault.

**Every pass re-resolves the anchors.** Timed with a cold site map, as a fresh game pid
gives: 14.3 s for one pass across ~13 unique anchors (0.7-2.1 s each; `mining` and `reach`
share `reset_block`, so it is 13 scans and not 15). A warm process costs ~5 s because 11 of
the 15 are already resolved and persisted per-pid in `patches.json`. Each retry pays the
cold cost again.

**Several cheats hook methods the game has not compiled yet, and that wait is real.** mono
JITs lazily: `fast_place`'s method compiles the first time the player places a block. Those
cheats *cannot* be applied before then, no matter how fast the scan is. The retry loop
exists precisely for this, and the code already said so in a comment.

So the fix chosen here is **reporting, not optimising**: the panel logged one line on the
first pass and then went quiet for the remaining ~75 seconds, which is indistinguishable
from a hang. It now says how many are applied, how many are waiting, and *why* they are
waiting. Making the scans cacheable across pids is left alone deliberately — whether a site
resolved against one process is valid in another is a question about the JIT that would
need its own measurement, and this project has scars from assuming that class of thing.

**An instrument error worth recording.** The first probe written for this read the patch
state file without checking whose pid it was for, found the *previous* game's state, and
reported all 15 cheats applied 0.7 s after launch. It was wrong by ~80 seconds and looked
authoritative. The corrected probe ignores state that does not belong to the live pid.

## Acceptance criteria

- [x] `MAIN_WORLD_NAME_OFF` is declared once in `layout.py` with the other `Main` statics,
      not spelled a second time anywhere.
- [x] `Service.world_id()` returns a value that differs between two worlds of identical
      dimensions, covered headlessly against the synthetic image, and returns None when no
      world is loaded.
- [x] Reading the world name validates what it gets — a wrong offset must return None
      rather than a plausible string.
- [x] Auto-restore fires on a world change with the pid unchanged, and does not fire again
      while the same world stays loaded.
- [x] The retry budget resets on a world change, so a lazily-JIT'd cheat gets its retries.
- [x] Auto-sell's piggy-bank cache is invalidated by a world change **between two worlds of
      the same size** — the case the shipped key misses. A test plants exactly that.
- [x] Every new test is mutation-checked. *(M23-M26. One survivor, M25: dropping the
      None-guard on the world value. The test fed only `None`, so both versions settled
      after one restore; the case that separates them is a world that becomes *transiently*
      unreadable, which is what a load screen looks like. Test rewritten around that; the
      mutant now dies.)*
- [x] `world_id()` runs on every status poll, so Main's static base is scanned **once** and
      shared. It was calling `main_static_base` directly, which is a full memory scan
      costing ~1.5s — once per second would have been the whole budget. Extracted as
      `Service._static_base`, which `tilemap()` now shares.
- [x] Auto-restore reports progress on **every** pass, naming how many cheats are still
      waiting and that they apply when the feature is first used — a cold restore takes
      ~80 s and the panel used to go silent after the first pass. The line is not repeated
      while it is unchanged. *(Mutations M30 and M31 each kill a test.)*
- [ ] Verified in the running game: switch worlds with the trainer open and the saved
      cheats come back without restarting it.

## Risks & Assumptions

- **The world name is read from a build-specific static offset**, so it rots on a game
  update like every other constant here. It fails safe: `read_mono_string` returns None on
  a bad pointer, `world_id()` returns None, and both callers fall back to today's
  behaviour (restore on pid change; re-scan the world) rather than acting on a wrong
  answer.
- **Duplicate world names are possible** — see the limitation above.
- **Rollback**: one commit; both call sites are small and independently revertible.
