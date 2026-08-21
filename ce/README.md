# CE-table spike — code-patch cheats for the frame-reset fields

Some Terraria cheats can't be done by an external `/proc` value-writer: the game
recomputes certain player fields every frame in `Player.ResetEffects()` and reads
them within that same frame, so any external write loses the race (the same reason
a single lethal hit can kill through a health freeze). Those cheats need a **code
patch** — remove the per-frame reset so a set value sticks.

This directory is the **validated spike** proving that works on Terraria 1.4.5.7,
using Cheat Engine as a scriptable, mono-aware resolver. Companion to the base
`/proc` trainer; the eventual build is a PyQt "CE Patches" tab plus a lifecycle
manager that owns a hidden CE worker.

## What was proven (2026-08-21, end to end)

1. **CE 7.6 runs headlessly under Wine** via a Lua dropped in `autorun/` (runs at
   startup, no clicks). Log to a file the Linux side reads.
2. **CE launched into the game's Proton prefix shares its wineserver namespace** —
   it sees `Terraria.exe`. (This was the make-or-break risk; it's fine.)
3. **CE's mono dissector** resolves managed classes/methods by name to their JIT
   address (`Terraria.Player:ResetEffects` → native code) and enumerates field
   offsets.
4. The exact **reset instructions** were found and **patched**, and a `/proc`-set
   `pickSpeed` then **held across live frames** — confirmed in-game as real global
   mining speed (fast blocks, *stock* swing/useTime), which `/proc` alone can't do.

## Key data (1.4.5.7)

Field offsets (from CE mono): `pickSpeed +0x8D8`, `wallSpeed +0x8DC`,
`tileSpeed +0x8E0`, `blockRange +0x9F8` (instance); `tileRangeX/Y +0x4C/+0x50`,
`itemGrabSpeed/Max +0x70/+0x74` (static); `statLife +0x738` (so from the `/proc`
statLife anchor, `pickSpeed = statLife + 0x1A0`).

Native reset sites inside `ResetEffects` (JIT addresses move per game *process*;
re-resolve the method and AOB-scan its body — they survive world reloads, only a
game restart re-JITs). `edi = this`:

| field | instruction | bytes | patch |
| :-- | :-- | :-- | :-- |
| pickSpeed | `fstp [edi+8D8]` | `D9 9F D8 08 00 00` | `DD D8 90 90 90 90` |
| tileSpeed | `fstp [edi+8E0]` | `D9 9F E0 08 00 00` | `DD D8 90 90 90 90` |
| wallSpeed | `fstp [edi+8DC]` | `D9 9F DC 08 00 00` | `DD D8 90 90 90 90` |
| blockRange | `mov [edi+9F8],0` | `C7 87 F8 09 00 00 00 00 00 00` | `90`×10 |

`fstp` is a pop-and-store: neutralize with `fstp st(0)` (`DD D8`, pops to keep the
x87 stack balanced) + NOP padding — a blind NOP leaks the pushed value and overflows
the FPU stack. `blockRange` is a plain `mov` → straight NOP.

## Per-field results (which resets are cleanly patchable)

Not every frame-reset field behaves the same — this is the main extended finding:

- **`pickSpeed` (mining speed) — CLEAN.** Single reset; neutralize it and a set
  value holds. Confirmed fast mining, stock swing. Note it's a *time* multiplier:
  **lower = faster.** Minor drift (`0.20 → 0.30`) means a second writer (buff/
  accessory, e.g. Mining Potion) also nudges it — patch that too or re-apply.
- **`blockRange` (placement reach) — CLEAN.** Single reset (`mov …,0`); NOP it and a
  set value holds. Item-independent extended reach, confirmed in-game.
- **Placement speed (via `tileSpeed`/`wallSpeed`) — SOLVED, by a different tactic.**
  Neutralizing their reset does NOT work: they're written by *multiple* per-frame
  paths (value won't settle — "all over the place"), their reset is load-bearing for
  autoplacement (removing it breaks auto-place), and the value is clamped up to `3.0`
  by accessories. The right target is where the value is **read**: placement timing
  funnels through `Player.ApplyItemTime(Item, float)`, computing
  `itemTime = max(1, (int)(useTime * tileSpeed))`. Overwrite that `max(edi,1)` block
  (`B8 01000000 3B F8 0F4C F8`, 10 bytes; `edi` = the computed time) with `mov edi, N`
  (`BF 0N 000000`) + NOP×5 — forcing `itemTime` to a small constant regardless of
  tileSpeed. Confirmed: fast, consistent placement, autoplacement intact (`N=4` places
  about as fast as you can move).

Takeaway for the build: a **single-reset** field (pickSpeed, blockRange) is patched by
NOPing its reset; an **entangled** field (tileSpeed/wallSpeed) is patched at its
**read/use site** (the timing math), not where it's reset. Both are CE-resolved →
byte-patch, so both fit the `/proc`-applies model.

## Architecture takeaway

CE is the **resolver** (mono → offsets + JIT address + patch bytes). *Applying* a
patch is just a byte-write — which `/proc` already does. So the runtime cheat can
run entirely through the existing `/proc` layer (AOB-scan the JIT anon region for
e.g. `D9 9F D8 08 00 00` → patch), with **CE optional at runtime** — use it only to
(re)derive offsets after a game update. That keeps everything single-pane in PyQt.

## Files

- `ce-terraria.sh` — launch CE into Terraria's prefix (appid 105600), 32-bit CE to
  match the 32-bit game + its MonoDataCollector32.
- `poc_resolve_method.lua` — attach + mono resolve `ResetEffects` → JIT address.
- `poc_fields.lua` — dump the speed/reach field offsets.
- `poc_item_fields.lua` — dump `Terraria.Item` field offsets (mono metadata). Used to
  pin `prefix` = 0x15C (a byte; not template-diffable since every template has prefix
  0) and to cross-check `defense` = 0xD4 / `rare` = 0xF8 against the /proc offsets.
- `poc_player_reach.lua` / `poc_getranges.lua` / `poc_mingetranges.lua` — the tile-reach
  investigation: Player reach-field offsets, the 2-out `TileReachCheckSettings.GetRanges`
  (the hook target for unified mining+interaction reach), and the mining chain proving
  the 4-out overload calls the 2-out. Full write-up in `REACH_FINDINGS.md`.
- `poc_grabitems.lua` — dump `Player.GrabItems` to find the pickup-range hook site: a
  call returns the grab range in eax, then `mov [ebp-54],eax`; injecting `imul eax,N`
  before that store scales the pickup radius (ported from the FearLess ReGrind table).
- `poc_patchsites.lua` — disassemble `ResetEffects`, flag the field-reset writes.
- `poc_patch_pickspeed.lua` — the payoff: scan + patch the pickSpeed reset.

To run one: copy it into
`<prefix>/drive_c/Program Files/Cheat Engine/autorun/`, launch via `ce-terraria.sh`
with the game running, then read the `tbonker_*.log` it writes next to the CE install.
CE must be present in Terraria's prefix (copy the `Cheat Engine` dir from an existing
CE prefix, or run the installer into it).
