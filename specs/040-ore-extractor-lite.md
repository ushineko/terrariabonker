# Spec 040: Ore extractor lite — auto-mine contiguous ores

**Status**: IN PROGRESS — both halves work in-game from the CLI (a vein mined 11/11 in
64s with the game healthy). No GUI surface yet; see "Where the write half stands".
The write half (the per-frame stub) is not started

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

**The tile map is not a managed array**, which is why scanning every heap object for an
array of `W*H`, `(W+1)*(H+1)`, `W*(H+1)` or `(W+1)*H` entries found nothing real. Guessing
at it was the wrong approach; `PickTile`'s own indexing gives the layout exactly, and since
its JIT address is known the disassembly can be read straight out of memory without CE:

```
obj    = [Main.tile + 0x08]         ; width @0, originX @4, height @8, originY @0xC
idx    = height * (x - originX) + (y - originY)      ; column-major
entry  = *(u32*)(Main.tile + 0x10 + 4*idx)           ; one POINTER per tile
type   = *(u16*)(entry + 0x08)
```

So `Main.tile` is the base of a large buffer — 8401 x 2401 = 20,170,801 entries of 4 bytes,
about 81 MB, matching an 81 MB anonymous mapping — and `[Main.tile + 0x0C]` holds that entry
count. Each entry points at a 24-byte tile object. The evenly spaced pointers seen at +0x10
during the first pass were **not** descriptors: they were consecutive *tile* objects, and
they looked identical because they were all air.

**Verified against reality**, twice, on two different worlds. Reading the column through the
player returned air above, Plants (3) then Grass (2) at their feet, air carrying a dirt wall,
then Stone (1) — Terraria's own tile ids, in the arrangement the player was standing in.

**The two sizes mean different things, and confusing them would send a flood fill out of the
world.** The bounds object reported 8401 x 2401 on a *small* (4200 x 1200) world:

| source | value | meaning |
| --- | --- | --- |
| `maxTilesX` / `maxTilesY` (Main-static +0x5A4) | 4200 x 1200 | the world actually loaded |
| bounds object at `[Main.tile + 0x08]` | 8401 x 2401 | the buffer's stride |

The buffer is allocated at the largest supported size and reused across worlds, so its
height is the **indexing stride** and must be used for the index arithmetic, while
`maxTilesX/maxTilesY` is the **extent** and must be used for bounds checks. Reading this the
other way round — as an earlier draft of this spec did — would walk a flood fill past the
world edge into tiles left over from whatever was loaded before.

**Air versus dirt does not need solving.** Tile id 0 is Dirt, and an empty tile is also 0
with an "active" flag clear that has not been located. It does not matter here: every ore is
a non-zero id, so a whitelist of ore ids can never match an empty tile.

## Why it is challenging

- **No bound to relax.** Every previous cheat changed a number or a branch in code the game
  already ran over the right data. This one has to introduce iteration.
- **Tiles cannot be cleared by poking memory.** Zeroing a tile skips the drop, the framing
  update, the lighting update and the net message. The whole point is to *get* the ore, so
  the removal has to go through the game's own code.
- **A flood fill in a code cave is a lot of assembly** — a work queue, bounds checks, tile
  indexing and a whitelist lookup, all in a stub, all crash-on-mistake.

## Shape (as built)

Split it where the project has split things before, keeping policy out of assembly:

- **Python does the thinking.** Read the tile map, flood-fill the contiguous run of
  whitelisted types from the tile just mined, and produce a list of coordinates. The
  whitelist is then a config value rather than a constant baked into a stub, and getting it
  wrong costs nothing.
- **Assembly does one dumb thing.** A stub reads a queued `(x, y)` from a scratch area and
  calls `Player.PickTile(x, y, <power>)`. Bounded work per call, no recursion, no queue
  management in machine code.

  Built with two deviations from this sketch. The hook is `PickTile`'s own entry rather
  than a per-frame method — it already runs on the game thread, and only while mining,
  which is the only time the cheat should act. And the stub does **not** clear the slot,
  because it may not write to its cave at all; the caller clears it once the tile is gone.

This is the flag-polling design that was considered and rejected for NPC spawning, where a
template copy turned out to be enough. Here there is no such shortcut: only the game's own
code can mine a tile properly.

## Open questions, in the order they need answering

*(1-3 are answered; only 4 remains.)*

1. ~~**The `Tilemap` layout.**~~ **Answered** — see above. The flood fill can be written in
   Python today.
2. ~~**Is `PickTile` safe to call from a cave?**~~ **Answered from the IL — and the answer
   was over-confident. Calling it crashes the game; see "Where the write half stands".**
   Four things were checked in the IL rather than assumed, and all four still hold. What
   they do not cover is the part that actually breaks a tile: every check below concerns a
   path the call takes *before* damage reaches 100, and the destruction path past that
   point has never once run without killing the process. Reasoning from IL established
   that nothing *should* go wrong; it did not establish that nothing does.

   - **The net traffic is free.** `NetMessage.SendData` opens with
     `ldsfld Main::netMode; brtrue; ret` — in single player every SendData on the kill
     path returns immediately.
   - **Out-of-range coordinates are a no-op, not a crash.** `WorldGen.KillTile` bounds-checks
     `i`/`j` against `Main.maxTilesX`/`maxTilesY` and returns. (That independently confirms
     which of the two sizes is the world extent — see above.)
   - **Protected tiles protect themselves.** `PickTile_DetermineDamage` calls
     `WorldGen.CanKillTile` and zeroes the damage if the tile may not be broken, so the
     flood fill inherits the game's own rules without implementing any of them.
   - **The damage cache does not leak.** Damage accumulates in `this.hitTile` and the tile
     breaks at 100; `ClearMiningCacheAt` frees the slot on the break. `HitTile` has a fixed
     `MAX_HITTILES` cap, which only matters if tiles need repeated hits — with a high pick
     power they break on the first.

   Nothing in the method needs the item-use context: it reads the tile, `this.hitTile` and
   statics. Two requirements remain — it is an instance method, so the stub needs
   `this` = `Main.player[Main.myPlayer]`, and it must run on the game thread, which is
   exactly why the design is a per-frame stub rather than an external call.

   **Signature**, from two real callers: `PickTile(this, int x, int y, int pickPower,
   int cap)`. `DamageTileWithShovel` passes `(100, -1)`; the mining path passes
   `(item.pick, -1)`.
3. ~~**`PickTile` or `KillTile` directly?**~~ **PickTile.** `CanKillTile` is consulted inside
   `PickTile_DetermineDamage`, so calling `KillTile` directly would skip the one check that
   keeps a flood fill away from tiles the game forbids breaking.
4. ~~**How much per frame?**~~ **Answered by the design, not by a tunable.** One tile is
   armed at a time and is re-mined on every `PickTile` call until it breaks, so the rate is
   the player's own swing rate and there is no burst to smooth: 11 tiles took 64 seconds of
   ordinary mining. Re-mining a broken tile is a no-op (`CanKillTile` zeroes the damage),
   which is what makes "arm and wait for it to be gone" safe.

## The write half: what broke, and what fixed it

**Working.** Verified in-game: a single armed tile mined on the first swing, then an
11-tile tin vein taken 11/11 in 64 seconds with the process healthy throughout.

Getting there cost three wrong diagnoses, and the shape of the mistake was the same each
time — a hypothesis that fit the evidence was declared a root cause before any test could
tell it apart from its rivals. What finally settled it was not more reasoning but wine's
own SEH tracing (`PROTON_LOG=1`, which sets `+seh`), which named the faulting instruction
in one run. Attaching a debugger instead would have been much worse: mono uses SIGSEGV as
normal control flow for null checks and GC write barriers, so "break on access violation"
trips constantly on healthy execution.

**Two real bugs found and fixed, neither of which was the crash:**

- **The re-entrancy guard was not a guard.** The stub cleared its "armed" flag as the first
  guarded instruction, so `ore_pending` reported "done" milliseconds before the mining it
  triggered had finished, and the queueing loop re-armed while the nested `PickTile` was
  still running. The re-entered stub then called `PickTile` again, recursing as deep as the
  vein was long. The slot is now a three-state machine (0 idle, 1 armed, 2 in flight) and
  the guard holds for the whole nested call, so depth is one by construction.
- **The active flag was never located.** `type_at` matched on the raw tile id, but Terraria
  clears only bit 5 of `sTileHeader` when a tile is mined and leaves `type` alone — so the
  flood fill matched tiles that no longer exist. Harmless (`CanKillTile` zeroes the damage)
  but pure wasted work. `solid_type_at` now gates on the active bit, and
  `check_active_offset` validates the field offset against the live world rather than
  trusting mono's field layout.

**The crash: the stub wrote to its own cave, and a cave is not writable.**

```
Unhandled exception: page fault on WRITE access to 0x7795424b
                     in wow64 32-bit code (0x77954216)
  info[0]=00000001 (write)   ESI:7795420f
  =>0 0x77954216 in cuesdk_2015 (+0x4216)
```

Cave at 0x77954209, so esi is cave+6 (the call/pop anchor), the faulting instruction is
cave+0x0D (`inc dword [esi+0x3c]`, the in-flight state write) and the target is cave+0x42,
the slot. `_find_cave` had borrowed padding inside a **code section of CUESDK_2015.dll**,
the Corsair SDK that ships with the game — read-execute. Installing the stub worked anyway
because `/proc/pid/mem` bypasses page protection; the CPU running that stub does not.

`ore_extract` was the first stub in this project to keep mutable state in its cave. Every
other injection only reads and executes there, which is why nothing had ever hit this and
why `_find_cave` had no writability check.

**The fix: design the write out.** The slot stays in the cave but is written only from the
unprivileged side, which may. The re-entrancy guard — the one thing that genuinely needed
a write — moved onto the stack: the nested call passes `ORE_SENTINEL` as PickTile's `cap`,
and the stub compares the incoming `cap` at `[esp+0x34]` before doing anything. That needs
no audit of what the game's own callers pass, because the test is "is this *my* sentinel".
`Injection.writes_cave` now makes `_find_cave` demand a writable page for any future stub
that does need one, so this fails at install rather than on the first swing.

**What the evidence had already ruled out:**

- It is a **hard native fault**, not a managed exception: nothing is appended to
  `client-crashlog.txt` (whose last entry predates both crashes), and wine traps SIGSEGV
  itself so the kernel logs nothing either.
- It is **not the code cave** — the cave is genuine `int3` padding in the same anonymous
  `r-xp` region the working `smart_cursor` stub has lived in all along.
- It is **not the signature** — `PickTile(this, x, y, pickPower, -1)` matches the game's own
  call site in `ItemCheck_UseMiningTools_ActuallyUseMiningTool` exactly.
- It is **not recursion** — a single queued tile has nothing to re-arm with and cannot
  recurse, and it crashed anyway. (The three-state guard was still a real fix for a real
  bug: the old flag was cleared before the work, so the queueing loop re-armed mid-call.
  It was simply never the cause.)
- It is **not stack alignment**, or at least not that alone: the `teleport` stub hands its
  callee `esp ≡ 8 (mod 16)` where mono assumes 12, and has never crashed. (The extractor
  now realigns anyway; mono builds 16-byte frames assuming `esp ≡ 12` at entry, which
  `PickTile`'s own prologue confirms — 4 pushes plus `sub esp,0x7C` is 140 bytes.)

- It is **not a bad `this`** — the prime suspect for a while, and wrong. Checked without
  touching the game: `[Main.player][Main.myPlayer]` is `0x3A54A090`, the same object
  `live_block()` resolves, same vtable.

**Left to do:** no GUI surface — the extractor is CLI-only (`tb vein`, `tb extract`).

**The bisect that got there,** kept because the ordering is the reusable part:

1. ~~*Read-only, no injection.* Compare `[Main.player][Main.myPlayer]` against the player
   object `live_block()` already resolves.~~ **Done — they match** (`0x3A54A090` both ways,
   same vtable, `myPlayer` 0 of a 256-element array). `this` is correct and is no longer a
   suspect, which moves the fault into the destruction path itself.
2. ~~*Read-only.* Run `check_active_offset` against the live world.~~ **Done, and it found a
   regression rather than confirming anything.** `sTileHeader` is at **0x0E**, not the 0x0C
   that adding up the field widths gives -- mono leaves two bytes of padding there. The
   wrong offset reads a constant zero, so every tile looked mined and `flood` returned
   nothing at all against the live game. It passed CI because the fixture was written to
   the same wrong guess. Corrected, and the validator now rests on premises that are
   actually true (sky ~0% active vs deep >30%), not on "id 0 means empty" -- Dirt *is*
   id 0, and that premise is what let the wrong offset through.

   Also settled: `KillTile` goes through `Tile.ClearEverything`, which zeroes `type` and
   `sTileHeader` together, so a mined tile reads back as id 0 regardless. The active bit
   earns its place by separating a **dirt block** from **air**, not a mined tile from a
   standing one, and the original justification for adding it was wrong.
3. *Stub with no call.* Compute `this`, write it to the slot, return. Confirms the stub
   agrees with Python and that the injection alone is inert.
4. ~~*Bisect the destruction path.* Call with `pickPower=1` so damage stays under 100.~~
   **Crashed at power 1**, before any tile could break — which ruled out the destruction
   path and pointed at the general machinery, where the fault turned out to be.

## Risks & Assumptions

- **This one can corrupt a world.** Every previous cheat has been reversible in memory; a
  wrongly mined tile is a permanent change to the save. Testing wants a throwaway world.
- **The whitelist matters for safety, not just taste.** A flood fill that escapes its
  whitelist could strip a large region.
- **Multiplayer is out of scope**, as with the other cheats.
