# Spec 040: Ore extractor lite — auto-mine contiguous ores

**Status**: RECON ONLY — not implemented, open questions below

> **Note**: No issue tracker ticket (personal utility).

## Context

Mine one ore tile and the contiguous vein of the same ore goes with it, driven by a
whitelist of tile types.

The maintainer flagged this as likely challenging. Recon agrees, and says why: unlike every
code-patch cheat so far, **there is no existing behaviour to widen**. The reach cheats
enlarged a bound, the pylon cheat inverted a gate, the accessory cheats extended a loop the
game already ran. Vein mining is new behaviour, and it needs two things the game never does
in one place: a flood fill, and a whitelist.

### What the recon established

**The mining path**

```
Player.ItemCheck_UseMiningTools_ActuallyUseMiningTool
  -> Player.PickTile(int x, int y, int pickPower)        # applies damage to one tile
       -> WorldGen.KillTile(...)                         # once damage exceeds hardness
```

`PickTile` is the right seam: it is where a single tile is worked on, it is called by the
mount drill and the Digtoise projectile as well as by hand mining, and it ends in
`KillTile`, which is what actually drops the ore. **Nothing in that path loops over tiles.**

**Addresses, on build 1.4.5.8+24893155** (resolved with `ce/poc_tiles.lua`):

| thing | where |
| --- | --- |
| `Main.tile` | Main-static **+0x99C** (a pointer to a `Tilemap` object) |
| `maxTilesX` / `maxTilesY` | Main-static **+0x5A4** (4200 x 1200 on the test world) |
| `Player.PickTile` | JIT `0x2178CD48` |
| `WorldGen.KillTile` | JIT `0x25900000` |

**The tile map is not a managed array.** Scanning every heap object for an array of
`W*H`, `(W+1)*(H+1)`, `W*(H+1)` or `(W+1)*H` entries found nothing real — the three hits had
implausible vtables. `Tilemap` keeps its data in unmanaged memory, which is consistent with
1.4.4's move to a struct-of-arrays tile store. `PickTile` reaches it as
`mov eax,[Main.tile]` then `mov eax,[eax+0x08]`, so the buffer hangs off the Tilemap object
at +0x08 (or +0x0C, also a plausible raw pointer). The evenly spaced pointers at +0x10
onwards were checked and are **not** data descriptors: they are identical small objects of
one class.

## Why it is challenging

- **No bound to relax.** Every previous cheat changed a number or a branch in code the game
  already ran over the right data. This one has to introduce iteration.
- **Tiles cannot be cleared by poking memory.** Zeroing a tile skips the drop, the framing
  update, the lighting update and the net message. The whole point is to *get* the ore, so
  the removal has to go through the game's own code.
- **A flood fill in a code cave is a lot of assembly** — a work queue, bounds checks, tile
  indexing and a whitelist lookup, all in a stub, all crash-on-mistake.

## Recommended shape (not yet built)

Split it where the project has split things before, keeping policy out of assembly:

- **Python does the thinking.** Read the tile map, flood-fill the contiguous run of
  whitelisted types from the tile just mined, and produce a list of coordinates. The
  whitelist is then a config value rather than a constant baked into a stub, and getting it
  wrong costs nothing.
- **Assembly does one dumb thing.** A stub in a per-frame method reads a queued `(x, y)`
  from a scratch area and calls `Player.PickTile(x, y, <power>)`, then clears the slot.
  Bounded work per frame, no recursion, no queue management in machine code.

This is the flag-polling design that was considered and rejected for NPC spawning, where a
template copy turned out to be enough. Here there is no such shortcut: only the game's own
code can mine a tile properly.

## Open questions, in the order they need answering

1. **The `Tilemap` layout** — which pointer holds the tile buffer, the per-tile stride, and
   where the type field sits within it. Without this there is no flood fill. Approach: read
   `[Main.tile + 0x08]` and `+0x0C`, and correlate against tiles whose type is known because
   the player is standing on them.
2. **Is `PickTile` safe to call from a cave?** It touches `hitTile`/`hitReplace` caches and
   sends net messages. Calling it re-entrantly, or outside the frame phase it expects, may
   not be safe.
3. **`PickTile` or `KillTile` directly?** `PickTile` respects pick power and plays the
   normal mining pipeline; `KillTile` is immediate but skips it. "Lite" probably wants the
   first.
4. **How much per frame?** A large vein mined in one frame will spike; a few tiles per frame
   is smoother and is also a natural way to keep the stub simple.

## Risks & Assumptions

- **This one can corrupt a world.** Every previous cheat has been reversible in memory; a
  wrongly mined tile is a permanent change to the save. Testing wants a throwaway world.
- **The whitelist matters for safety, not just taste.** A flood fill that escapes its
  whitelist could strip a large region.
- **Multiplayer is out of scope**, as with the other cheats.
