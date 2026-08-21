# How the offsets were found, and how to re-derive them

Everything the trainer knows about Terraria is a handful of memory offsets in
`terrariabonker/locate.py` and `terrariabonker/player.py`. They were derived
live, from scratch, with no pre-existing address list. This document records the
method so the offsets can be rebuilt after a game update moves them.

## Contents

- [The environment](#the-environment)
- [Finding health](#finding-health)
- [Identifying the Player object](#identifying-the-player-object)
- [The name anchor](#the-name-anchor)
- [The locator](#the-locator)
- [Godmode](#godmode)
- [Re-deriving after an update](#re-deriving-after-an-update)

## The environment

Terraria runs as the 32-bit Windows build under Proton (`Terraria.exe`,
.NET Framework + XNA on wine-mono). Two facts shape the whole approach:

- **wine-mono uses a non-moving garbage collector.** A located object keeps its
  address for its lifetime, so a raw `/proc`-based trainer is stable within a
  world. The Player object is reallocated on world reload, not on death/respawn.
- **`kernel.yama.ptrace_scope=1`** blocks reading another process's
  `/proc/<pid>/mem` without privilege, so the trainer runs under `sudo`.

The game process is the one PID whose `/proc/<pid>/maps` maps `Terraria.exe`
executable (`r-xp`); the Proton wrapper scripts reference the path but never map
it executable.

## Finding health

Classic value-scan narrowing, done through `/proc/<pid>/mem` with numpy:

1. Scan all writable anonymous regions for the current HP as an int32.
2. Change HP in game (take a hit), scan again for the new value.
3. Repeat until a couple of addresses remain.

Starting from 100 HP, a single narrow to 53 (one slime bite) went from ~27,000
matches to 2. Writing a sentinel to a candidate and watching the on-screen hearts
confirmed which address is the live `statLife`.

## Identifying the Player object

Dumping the ints around the confirmed `statLife` showed the tell-tale block:

```
-0x08  -0x04  +0x00  +0x04  +0x08  +0x0C
  100    100     97     20     20     20
 max2   max    life   mana  manaMax manaMax2
```

That contiguous life/mana run (`statLifeMax2, statLifeMax, statLife, statMana,
statManaMax, statManaMax2`) is the Player object's signature. A second address
also tracked HP but was surrounded by an array-of-structs, not this block; it is
an inert copy, not the live player.

## The name anchor

Dereferencing the object's pointer fields and testing each target as a 32-bit
mono String (`+8` int length, `+0xC` UTF-16 chars) turned up the character name
`terrariabonker` at a fixed `statLife - 0x6C0`. The neighbouring pointer at
`-0x6C4` held a cached coin-display string (`"2 silver 57 copper"`), a lead for a
future money editor.

## The locator

The locator (`find_players`) scans writable memory for six consecutive int32 that
pass Terraria's own invariants and are confirmed by a real name string:

- `100 <= statLifeMax <= 500`, and `statLifeMax2 == statLifeMax`
- `statManaMax` a multiple of 20 in `[20, 400]`, and `statManaMax2 == statManaMax`
- `1 <= statLife <= statLifeMax`, `0 <= statMana <= statManaMax`
- a decodable mono String pointer at `statLife - 0x6C0`

Against ~1.6 GB this returns only the player's own copies (usually the live one
plus one or two load-time snapshots sharing the name) in about a second. The
snapshots are inert: writes to them persist without reverting and do not affect
gameplay, so freezing every match is safe. `pick_live` guesses the live copy for
display by sampling for activity, and falls back to the copy whose HP is below
max; freezing does not depend on the guess being right.

## Godmode

Terraria rewrites `statLife` on damage and regen, so a one-shot write reverts.
Godmode is a rewrite loop that pins `statLife` to `statLifeMax` about 200 times a
second, four times per game frame. It is not a code patch, so a single hit larger
than current HP could still register death within a frame before the next
rewrite; raise permanent max HP (`set-max-hp`) for margin against big hits.

## Re-deriving after an update

`version.py` gates the trainer on the exact build (`1.4.5.7`, Steam buildid
`24825745`). If the game updates:

1. Run `terrariabonker version` to see the detected build.
2. If the layout still matches, `terrariabonker status` finds the player and the
   offsets are still good; bump `KNOWN_VERSION` / `KNOWN_BUILDID`.
3. If `status` finds nothing, the offsets moved. Repeat "Finding health" above
   with the scratch scanner, re-dump the block to confirm the six-int layout is
   unchanged (it may not be), and re-measure the name offset by dereferencing
   pointer fields for the character name. Update the offsets in `locate.py` and
   `player.py`.

The locator fails safe: on a shifted layout it matches nothing rather than
writing to a wrong address, which is why the version gate is a warning aid rather
than the primary defence.
