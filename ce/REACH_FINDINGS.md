# Terraria 1.4.5.7 tile-reach architecture (RE findings)

How mining, placement, and interaction reach are computed — derived by disassembling
the JIT'd managed methods with Cheat Engine's mono tools (see the `poc_*reach*`/
`poc_getranges`/`poc_mingetranges` probes). This is the map behind the "unified reach"
cheat.

## The three reach systems are NOT one lever

| System | Path | Reads |
| --- | --- | --- |
| **Placement** | `Player.ItemCheck_UseItem` float compares | `tileRangeX + item.tileBoost + blockRange`, `tileRangeX` read **directly** |
| **Mining / tools** | `IsTargetTileInItemRange` → helper → **4-out `GetRanges`** → **2-out `GetRanges`** | 2-out output (clamped) |
| **Interaction** (chests/signs/smart) | `IsInTileInteractionRange` → **2-out `GetRanges`** | 2-out output (clamped) |
| **Crafting-station adjacency** (recipe availability) | `Player.AdjTiles` nearby-tile scan | separate fixed radius — NOT `GetRanges` |

`tileRangeX` / `tileRangeY` are `private static int` (= 5 / 4). In this process their
static slots were `0x025AD30C` / `0x025AD310` (ASLR-varying; the read is
`mov eax,[0x025AD30C]` inside the 2-out `GetRanges`). Note `0x025AD314`/`0x025AD318`
are the neighbouring **`tileTargetX/Y`** (cursor tile, values in the thousands) — an
easy mis-identification.

## Why writing tileRangeX only moves placement

Empirically, setting the static `tileRangeX` (`0x025AD30C = 25`) **extended placement
but not mining or interaction**. Placement reads `tileRangeX` directly; mining and
interaction read the **output of `GetRanges`**, which reads `tileRangeX` and then
**clamps/overrides** it via conditional blocks keyed on the `TileReachCheckSettings`
flags. So the clamp discards the raw `tileRangeX` change. This is exactly why the
community table (FearLess "ReGrind" 1.4.5) forces the **output** of `GetRanges`, not
the input.

## The two `GetRanges` overloads

- **2-out** `TileReachCheckSettings.GetRanges(this, out x, out y)` — prologue
  `55 8B EC 53 57 56 83 EC 1C 8B 5D 08 8B 75 0C 8B 7D 10`. `esi = out_x ptr`,
  `edi = out_y ptr` held across the whole body. Computes `out = tileRangeX * this[0]`
  then conditionally clamps. Epilogue: `8D 65 F4 5E 5F 5B C9 C3`
  (`lea esp,[ebp-0C]; pop esi; pop edi; pop ebx; leave; ret`).
- **4-out** overload — calls the 2-out overload, then builds a bounding box
  (`lowX/highX/lowY/highY`) from the player position + the 2-out ranges. Used by mining.

Because the 4-out calls the 2-out, **forcing the 2-out output covers mining AND
interaction** in one hook.

## The cheat: force the 2-out `GetRanges` output (ReGrind-style)

At the 2-out epilogue, `esi`/`edi` still point at the out params, so inject before the
pops:

```
mov dword [esi], N      ; C7 06 <N>
mov dword [edi], N      ; C7 07 <N>
<original 5 bytes: 8D 65 F4 5E 5F>   ; lea esp,[ebp-0C]; pop esi; pop edi
jmp back to epilogue+5  ; E9 <rel32>  (lands on `pop ebx`)
```

Overwrite the 5 bytes at the epilogue start (`8D 65 F4 5E 5F`) with `jmp <cave>`
(`E9 <rel32>`, 5 bytes). This needs a **code cave** (~22 bytes of executable padding),
which is why it can't be an in-place equal-length patch. Anchor the method by the
fixed prologue above (no ASLR bytes in it), inject at `prologue + 0xCA` (this build).

Placement is a separate, simpler lever (write the static `tileRangeX`), if wanted.

## Code-cave note: borrow vs allocate

The `tool_reach` stub is written into **borrowed** executable padding (a run of int3 /
zero between JIT'd methods), not into memory we allocate. That is safe *enough* here
because it is small (22 bytes), disposable (disable restores, restart clears, AOB
re-derives), and because wine-mono's forward/bump code allocator won't reclaim
interstitial alignment padding (non-tiered, no re-JIT, no code unloading). **Risk
scales with stub size** — big caves are rare and more likely to be real data — so this
is a small-stub-only technique. When we add a second injection cheat, need a bigger
stub, or port to 64-bit (where `rel32` can't reach a far allocation), graduate to an
**allocate-the-body** backend and extract a reusable Hook/Detour abstraction. Full
rationale is in the `Patcher._find_cave` docstring.

## Probes

- `poc_player_reach.lua` — enumerate the Player reach-field offsets (tileRangeX/Y are
  static; blockRange/lastTileRange are instance).
- `poc_getranges.lua` — full disassembly of the 2-out `GetRanges` (the hook target).
- `poc_mingetranges.lua` — the mining chain (4-out → 2-out), proving they share the
  2-out output.
