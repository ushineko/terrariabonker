# Spec 034: Clamp the smart-cursor search range

**Status**: INCOMPLETE (1 criterion open: the not-yet-JIT-compiled path,
pending a fresh game launch)
**Implementation Date**: 2026-08-23

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
offsets from an object pointer (`screenTargetX +0x28`, `screenTargetY +0x2C`,
`reachableStartX +0x30`, `EndX +0x34`, `StartY +0x38`, `EndY +0x3C`, through `esi`) — a stub
can shrink the box with arithmetic alone, needing no game state:

```
    px = (startX + endX) / 2            ; the player tile: GetTileRegion centred the box there
    startX = max(startX, min(px, cursorX) - N)
    endX   = min(endX,   max(px, cursorX) + N)
    ... the same for Y, using screenTargetY
```

Clamping the *result* rather than the input keeps placement and tool reach untouched, and
spanning player-to-cursor rather than one point is what makes it behave (see Risks).

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
   `GetTileRegion` returns and after the existing world-edge clamps, so this clamp is the
   last word. The stub narrows the four `SmartCursorUsageInfo` fields to the span between the
   player and the cursor plus N, intersected with the original box so it can only shrink.
   The displaced `test ebx,ebx` is reproduced **last**, because the instruction after the
   jump back branches on its flags.
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
- **Those four fields serve three purposes, and only one is visible in the IL.** They are the
  search extent, the "is the cursor within reach" test five instructions later, and the
  working area of a search that runs outward from the player. Two clamp shapes were written
  and rejected in play because of this: a player-centred box stopped containing the cursor
  ("moving the mouse starts placing without smart enabled"), and a cursor-centred box cut out
  the span back toward the player. The shipped shape spans both ends.
- **The player tile is taken as the original box's midpoint.** `GetTileRegion` centres the box
  on the player, so this holds except near a world edge where the clamps skew it; there the
  result is a slightly off-centre box, which is harmless because the intersection means it
  can only ever be smaller than what vanilla would have searched.
- **Interaction with `tool_reach`**: this does not undo it. Tile interaction, chests, signs
  and crafting stations keep the extended range, because `GetTileRegion` itself is untouched.
- **Rollback.** `git revert`; the cheat is off by default and disable restores the displaced
  bytes and scrubs the cave.

## Acceptance Criteria

- [x] A smart-cursor range cheat appears in the **Build** section, off by default, tunable,
      with a note explaining the quadratic cost
- [x] With `reach`/`tool_reach` at 75 and this cheat on, holding Shift to auto-place no longer
      stutters (the maintainer's original repro)
- [x] Manual placement reach, tool reach, chest/sign interaction and remote crafting stations
      are all unaffected while it is on
- [x] Changing the value changes the search box live, without a re-enable
- [x] Disabling restores the displaced bytes and the original smart-cursor behaviour returns
- [x] The anchor still resolves with the cheat applied (cold re-resolve)
- [ ] Behaviour when the method has not been JIT-compiled yet — NOT VERIFIED. It was
      already compiled in the session this was built in, so the path was never exercised.
      By design it reports "matched nothing" honestly (spec 030) and auto-restore retries,
      but that is reasoning, not evidence. Confirm on the next fresh game launch
- [x] The stutter is gone in play, maintainer-confirmed, with reach/tool_reach still at 75.
      Note it was triggerable by *holding Shift alone*, i.e. by the per-frame search rather
      than by placing. The interleaved CPU measurement used for spec 033 was deliberately
      not run: it needs the workload sustained for ~80s, which here means holding Shift
      throughout, and the subjective result is unambiguous
- [x] Anchor carries the build key it was confirmed on
- [x] All tests pass headless; flake8 clean on changed files; security review recorded
- [x] README updated; version bumped to 0.24.0 (maintainer confirmed)

## Executive Summary

Holding Shift to auto-place collapsed the frame rate with the reach cheats set high, because
`SmartCursorLookup` sizes its search region from `GetTileRegion` — which calls the
`GetRanges` that `tool_reach` forces, then adds `blockRange`. The region is an area, so reach
75 means 22,801 tiles scanned per frame, and both cheats stack into it. The cheat clamps that
region without touching reach itself.

The clamp could not go in `GetTileRegion` (nine callers, including the tile-interaction and
crafting-station checks `tool_reach` exists to extend), so it goes at the tail of
`SmartCursorLookup`, rewriting the four `reachable*` fields after the game's own world-edge
clamps.

What the IL did not show, and two rejected shapes did: those four fields serve three
purposes — the search extent, the "is the cursor in reach" test five instructions later, and
the working area for a search that runs outward from the player. A player-centred box broke
the second; a cursor-centred box broke the third. The shipped shape spans both ends, plus a
margin, intersected with the original so it can only shrink.

Reviewers: `_shrink_smart_cursor` and the two rejected shapes documented in its docstring.

## Testing

226 headless tests, flake8 clean, `pip-audit 2.10.0` clean. `tests/test_smart_cursor.py`
(11): the displaced store is reproduced first and the displaced `test ebx,ebx` last (the
following `je` branches on its flags); the radius is baked in four times per stub; a
zero/negative radius floors to 1; only the four `reachable*` fields are touched; enable
installs a jump and disable restores; retuning rewrites the stub without a re-enable; the
anchor still resolves with the cheat applied; and it lands in the Build section.

The stub was disassembled against the live game before each of the three attempts.

Live, maintainer-verified with `reach`/`tool_reach` left at 75: the stutter is gone — and it
had been triggerable by holding Shift alone, i.e. by the per-frame search rather than by
placing — while manual placement reach and tool/interaction reach are unchanged. Smart
placement itself behaves normally at range, which the first two clamp shapes broke in two
different ways (see the Executive Summary).
