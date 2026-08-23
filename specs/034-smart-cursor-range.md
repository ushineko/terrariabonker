# Spec 034: Clamp the smart-cursor search range

**Status**: INCOMPLETE

> **Note**: No issue tracker ticket (personal utility).

## Context

Holding Shift to auto-place torches collapses the frame rate when the reach cheats are set
high. Diagnosed, not guessed:

`SmartCursorHelper.SmartCursorLookup` builds the region it searches from
`TileReachCheckSettings.GetTileRegion`, feeding it `Player.blockRange`:

```
IL_012B: ldfld Terraria.Player::blockRange
IL_0132: ldsfld TileReachCheckSettings::Simple
IL_0155: call  TileReachCheckSettings::GetTileRegion
```

`GetTileRegion` calls `GetRanges` — the method `tool_reach` forces to its configured value —
and then *adds* the extra range on top. Both reach cheats therefore feed the smart cursor's
search box, and the box is an area, so the cost is quadratic:

| reach | tiles scanned per frame |
| --- | --- |
| ~5 (vanilla-ish) | 121 |
| 20 | 1,681 |
| 75 | 22,801 |
| 75 + 75 stacked | ~90,000 |

Confirmed in-game: at `reach`/`tool_reach` = 75 the stutter is severe; at 20 it disappears
entirely. Nothing is wrong with the patches — this is an emergent cost of large values, and
the tooltips now say so.

The maintainer wants placement reach and tool reach to stay at maximum, with the smart
cursor's search clamped separately.

### Why the clamp cannot go in `GetTileRegion`

It has nine callers, and several are the ones that make the reach cheats work:

```
Player::IsInTileInteractionRange      Player::AdjTiles
Player::IsTileTypeInInteractionRange  Player::SmartInteractLookup_PrepareCommonlyUsedInfo
Player::QuickMinecart                 Player::TryPlacingAGolfBallNearANearbyTee
SmartCursorHelper::SmartCursorLookup  TileReachCheckSettings::GetWorldRegion
```

Clamping there would shrink tile interaction range and remote crafting-station detection —
exactly what `tool_reach` exists to extend. The clamp belongs inside `SmartCursorLookup`.

### Where it goes

The region is not anonymous stack slots: `GetTileRegion` writes it into four named fields of
the `SmartCursorUsageInfo` object, which the method then re-reads, clamps against the world
edges, and stores back:

```
IL_0154: ldloc.3                                   ; extra range (base + blockRange)
IL_0155: call GetTileRegion(..., &reachableStartX, &reachableStartY,
                                 &reachableEndX,   &reachableEndY, extraRange)
IL_015C: ldfld SmartCursorUsageInfo::reachableStartX
IL_016B: call Utils::Clamp<>                       ; against 10 .. maxTilesX-10
IL_0170: stfld SmartCursorUsageInfo::reachableStartX
   ... the same for reachableEndX, reachableStartY, reachableEndY
```

`loc.0` is a reference (`ldloc.0` + `ldflda`), so the four values are int fields at fixed
offsets from an object pointer — a stub can shrink the box with arithmetic alone, needing no
game state:

```
    cx = (startX + endX) / 2 ;  startX = cx - N ;  endX = cx + N
    cy = (startY + endY) / 2 ;  startY = cy - N ;  endY = cy + N
```

Clamping the *result* rather than the input is what keeps placement and tool reach untouched.

## Requirements

1. A toggleable cheat that limits the smart cursor's search box to a tunable radius, while
   `reach` and `tool_reach` stay at whatever the user set.
2. Default radius small enough to remove the stutter (the 20 that tested clean is a
   reasonable starting point) and tunable like the other valued cheats.
3. Its note states the performance relationship plainly: the smart cursor scans the square of
   this radius every frame, and large reach values are what make it expensive.
4. Disabling restores the original bytes; a game restart clears it.
5. Persists and auto-restores through the existing profile machinery.

### Technical

6. **Injection after the region is computed**, inside `SmartCursorLookup` — after
   `GetTileRegion` returns and (preferably) after the existing world-edge clamps, so this
   clamp is the last word. The stub rewrites the four `SmartCursorUsageInfo` fields to a box
   of ±N around their own midpoint.
7. **Field offsets and the register holding the info object** come from PASS 2 on the JIT'd
   body; the four fields are adjacent in the class, so one base register plus four
   displacements is expected.
8. The anchor wildcards whatever the stub displaces, so a cold re-resolve still matches with
   the cheat applied — the trap specs 032 and 033 both had to handle.
9. Recorded in the spec-030 ledger with the build key it is confirmed on, and resolved with
   the multi-site rule.
10. Lands in the **Build** section of the patch list (spec 033's grouping), next to the reach
    cheats it moderates.

## Risks & Assumptions

- **`SmartCursorLookup` may not be JIT-compiled until the smart cursor is first used**, like
  fast placement. If so the anchor resolves only after the player holds Shift once, and the
  cheat reports "matched nothing" until then — which the honest reasons from spec 030 already
  express, and which auto-restore's retry loop already tolerates.
- **Shrinking the box changes behaviour, not just cost**: the smart cursor will not reach as
  far as it did. That is the point, but it means the cheat is not purely an optimisation and
  belongs under the user's control with a visible value.
- **Midpoint arithmetic assumes the box is centred on the player.** `GetTileRegion` builds it
  from the player's position, and the world-edge clamps can skew it near a map edge; there
  the clamp yields a smaller or off-centre box, which is harmless.
- **Interaction with `tool_reach`**: this does not undo it. Tile interaction, chests, signs
  and crafting stations keep the extended range, because `GetTileRegion` itself is untouched.
- **Rollback.** `git revert`; the cheat is off by default and disable restores the displaced
  bytes and scrubs the cave.

## Acceptance Criteria

- [ ] A smart-cursor range cheat appears in the **Build** section, off by default, tunable,
      with a note explaining the quadratic cost
- [ ] With `reach`/`tool_reach` at 75 and this cheat on, holding Shift to auto-place no longer
      stutters (the maintainer's original repro)
- [ ] Manual placement reach, tool reach, chest/sign interaction and remote crafting stations
      are all unaffected while it is on
- [ ] Changing the value changes the search box live, without a re-enable
- [ ] Disabling restores the displaced bytes and the original smart-cursor behaviour returns
- [ ] The anchor still resolves with the cheat applied (cold re-resolve)
- [ ] Behaviour is sane when the method has not been JIT-compiled yet: reported honestly, and
      it applies once the smart cursor is first used
- [ ] Measured: CPU with the cheat on is materially below the same scenario with it off,
      using the interleaved A/B method from spec 033 (a single-block comparison is not
      trustworthy — the game's own workload drifts more than the effect)
- [ ] Anchor carries the build key it was confirmed on
- [ ] All tests pass headless; flake8 clean on changed files; security review recorded
- [ ] README updated; version bump confirmed by the maintainer

## Executive Summary

_Populate before opening the PR._

## Testing

_Populate during implementation._
