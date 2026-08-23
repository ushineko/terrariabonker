# Smart-cursor findings — why big reach values stutter (1.4.5.7)

Recon for spec 034. Static half with `tools/ilrecon` (no Cheat Engine, no Wine, no running
game); JIT'd half by walking the call graph in `/proc/<pid>/mem` and disassembling with
`objdump -b binary -m i386`.

## The symptom

Holding Shift to auto-place torches collapsed the frame rate, with `reach` and `tool_reach`
at 75. Reported as possibly an accessory-cheat interaction; it is not.

## The cause

`SmartCursorHelper.SmartCursorLookup` sizes the region it searches with
`TileReachCheckSettings.GetTileRegion`, passing `Player.blockRange`:

```
IL_012B: ldfld Terraria.Player::blockRange
IL_0155: call  TileReachCheckSettings::GetTileRegion
```

`GetTileRegion` calls `GetRanges` — the method `tool_reach` forces — and adds the extra range
on top. Both reach cheats therefore feed it, and the region is an **area**:

| reach | tiles scanned per frame |
| --- | --- |
| ~5 (vanilla-ish) | 121 |
| 20 | 1,681 |
| 75 | 22,801 |
| 75 + 75 stacked | ~90,000 |

Confirmed by experiment: severe stutter at 75, none at 20.

## Why the clamp cannot go in `GetTileRegion`

Nine callers, several load-bearing for the reach cheats themselves:

```
Player::IsInTileInteractionRange      Player::AdjTiles
Player::IsTileTypeInInteractionRange  Player::SmartInteractLookup_PrepareCommonlyUsedInfo
Player::QuickMinecart                 Player::TryPlacingAGolfBallNearANearbyTee
SmartCursorHelper::SmartCursorLookup  TileReachCheckSettings::GetWorldRegion
```

Clamping there would shrink tile interaction range and remote crafting-station detection.

## Finding it in the JIT'd code

No distinctive constants to anchor on, so it was reached by call graph:

1. `tool_reach` already anchors `GetRanges`; scan for `call rel32` sites targeting it → two
   functions call it.
2. Walk back to each prologue; the one with **six** callers is `GetTileRegion` (0x1BD48C90 in
   the session this was derived on).
3. Scan for callers of *that*; the one followed by four `Utils::Clamp` blocks against
   `Main::maxTilesX`/`maxTilesY` is `SmartCursorLookup`.

The tail, which is where the cheat injects:

```
bf9259b:  call GetTileRegion
bf925c0:  mov [esi+0x30],eax     ; reachableStartX   (clamped to 10..maxTilesX-10)
bf925e4:  mov [esi+0x34],eax     ; reachableEndX
bf92608:  mov [esi+0x38],eax     ; reachableStartY
bf9262c:  mov [esi+0x3c],eax     ; reachableEndY     <-- injection point
bf9262f:  test ebx,ebx                               <-- flags for the following je
bf92633:  mov eax,[esi+0x28]     ; screenTargetX (the mouse tile)
bf92636:  cmp eax,[esi+0x30]     ; ... is the cursor inside the box?
bf9263b:  jl  bail                                   ; if not, no smart cursor
```

`esi` holds the `SmartCursorUsageInfo`: `screenTargetX +0x28`, `screenTargetY +0x2C`,
`reachableStartX +0x30`, `EndX +0x34`, `StartY +0x38`, `EndY +0x3C`.

## The part the IL does not tell you

Those four fields do **triple duty**, and the second and third uses only showed up in play:

1. the extent the smart cursor searches;
2. the "is the cursor within reach" test five instructions later — if the cursor falls
   outside the box, the game abandons smart cursor entirely;
3. the working area for the search itself, which runs **outward from the player**.

Two clamp shapes were written and rejected in-game because of this:

- **Centred on the player.** Moving the mouse past the radius put the cursor outside the box,
  so use (2) failed: "it places, then moving the mouse starts placing without smart enabled."
- **Centred on the cursor.** Use (2) was satisfied, but the span back toward the player was
  cut out, so use (3) failed once player and cursor were more than the radius apart.

The shape that works spans **both ends**: the original box's midpoint is the player tile
(GetTileRegion built the box around them), `screenTargetX/Y` is the cursor, and the clamp
keeps `min - n .. max + n` of that pair, intersected with the original box so it can only
ever shrink. The searched area becomes the on-screen separation plus a margin instead of the
reach squared, and a genuinely out-of-reach target still bails exactly as vanilla does.

The displaced `test ebx,ebx` must be reproduced **last** in the stub: the instruction after
the jump back branches on its flags, and the clamp arithmetic would otherwise clobber them.
