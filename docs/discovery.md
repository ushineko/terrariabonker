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
gameplay, so freezing every match is safe.

Picking the *live* copy for reads (inventory/status display) is done by ground
truth, not a heuristic: `resolve_local_player` resolves `Main.player[Main.myPlayer]`.
It AOB-finds `Main.get_LocalPlayer` — whose JIT tail
`cmp [eax+0C],ecx; jbe; lea eax,[eax+ecx*4+10]; mov eax,[eax]; ret`
(`39 48 0C 0F 86 07 00 00 00 8D 44 88 10 8B 00 C3`) is preceded by two
`mov reg,[abs]` that load the `Main.player` and `Main.myPlayer` statics — reads those
two addresses out of the instruction operands, indexes the `Player[]` szarray
(`arr + myPlayer*4 + 0x10`), and adds `Player.statLife`'s object offset (`0x738`).
This works even while the game is paused, which the activity heuristic (`pick_live`)
cannot: the trainer is normally used while its window has focus, so Terraria's
pause-on-focus-loss freezes every copy and the heuristic's "richest inventory"
fallback can pick a stale snapshot that outnumbers the live player's items.
`pick_live` remains only as a fallback if the `get_LocalPlayer` pattern is missing
(e.g. after a game update). Freezing still acts on every matched copy regardless.

## Godmode

Terraria rewrites `statLife` on damage and regen, so a one-shot write reverts.
Godmode is a rewrite loop that pins `statLife` to `statLifeMax` about 200 times a
second, four times per game frame. It is not a code patch, so a single hit larger
than current HP could still register death within a frame before the next
rewrite; raise permanent max HP (`set-max-hp`) for margin against big hits.

## Item field offsets by template diff (e.g. `rare` = 0xF8)

`Item` field offsets are found by comparing pristine template items. The game keeps
one template `Item` per type in `ContentSamples`; `service._template_block` locates it
by scanning for an object whose `+0x6C` equals the wanted type and whose `+0x00`
equals the shared `Item` vtable. To locate a field, read the templates of several
items whose value for that field is known and find the single offset that matches all
of them.

`Item.rare` (rarity tier, int) was pinned to **0xF8** this way: templates of Dirt
Block (0), Aglet (1), Muramasa (2), Excalibur (5) and Terra Blade (8) share exactly
one int offset carrying those values, and it validated across the spectrum — Old Shoe
−1 (gray), commons 0 (white), Shackle/Aglet 1 (blue), Meowmere/Zenith 10 (red). Small
integer fields are ambiguous alone (many offsets hold a `2`), so use four or more
items with *distinct* known values; the intersection is unique. Bright rarity values
outside the templated `[0x1C, 0x140)` block are found by resolving each template's base
address and reading a wider window.

`Item.defense` = **0xD4** was pinned the same way (a helmet tier ladder: Copper 1,
Iron 2, Silver 3, Gold 4, Platinum 5, Molten 8; a weapon reads 0). `Item.prefix`
(the modifier tier) can **not** be template-diffed — every `ContentSamples` template
has prefix 0 — so it was read straight from mono metadata with Cheat Engine
(`ce/poc_item_fields.lua`: `mono_findClass("Terraria","Item")` +
`mono_class_enumFields`): `prefix` = **0x15C** (a `System.Byte`). That same dump
confirmed every `/proc`-derived offset (type 0x6C, stack 0x88, damage 0xAC, defense
0xD4, rare 0xF8, …), so mono enumeration is the authoritative cross-check whenever an
offset is in doubt or after a game update.

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
