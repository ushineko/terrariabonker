# Spec 037: More than one pylon per biome

**Status**: COMPLETE
**Implementation Date**: 2026-08-23

> **Note**: No issue tracker ticket (personal utility).

## Context

Terraria allows exactly one pylon of each type. That is a placement rule, not a structural
one, and it stops you building a pylon network with several waystations in the same biome —
one at the base, one at the mine, one at the arena.

### What the recon found

The rule is enforced in **one** place for single-player, and the rest of the system never
assumed it.

`TETeleportationPylon.PlacementPreviewHook_CheckIfCanPlace` is the whole gate, and its IL is
eleven instructions:

```
IL_0000: ldarg.3
IL_0001: call  GetPylonTypeFromPylonTileStyle
IL_0007: ldsfld Terraria.Main::PylonSystem
IL_000D: callvirt TeleportPylonsSystem::HasPylonOfType
IL_0012: brfalse.s IL_0016
IL_0014: ldc.i4.1     ; a pylon of this type exists
IL_0015: ret
IL_0016: ldc.i4.0
IL_0017: ret
```

**The name is misleading and the polarity has to be read, not assumed.** A method called
`CheckIfCanPlace` returns **1 when placement is forbidden**. It is registered as

```
PlacementHook(hook, badReturn: 1, badResponse: 0, processedCoordinates: true)
```

and `TileObject.CanPlace` rejects the placement when the hook's return **equals badReturn**.
So returning 0 unconditionally lifts the limit, and returning 1 would ban pylons entirely.

`HasPylonOfType` has exactly two callers — this hook and `TETeleportationPylon`'s
`NetPlaceEntityAttempt`, the multiplayer path. Patching the hook is therefore narrower than
patching the predicate, and single-player needs nothing else.

**Nothing downstream dedupes by type**, which was the real risk: a placement that succeeds
but never appears on the map would be worse than no cheat.
`TeleportPylonsSystem.UpdatePylonsListAndBroadcastChanges` rebuilds the pylon list by walking
every `TETeleportationPylon` in `TileEntity.ByPosition` and adding each one with its own
`PositionInTiles` and `TypeOfPylon` — no uniqueness check anywhere in it. The map layer and
the teleport handler work from that list and from positions, so duplicates should list, draw
and teleport like any other pylon.

## Requirements

1. A toggleable cheat that allows more than one pylon of the same type to be placed.
2. Off by default; disabling restores the original bytes and the vanilla rule returns.
3. Persists and auto-restores through the existing profile machinery.
4. Its note says what it does in one line, to the length the other cheat notes now keep.

### Technical

5. **Patch the hook, not the predicate.** Force
   `PlacementPreviewHook_CheckIfCanPlace` to return 0 — `31 C0 C3` (`xor eax,eax; ret`)
   over the prologue's `55 8B EC`. Three bytes, nothing displaced that has to be reproduced,
   so no code cave is needed. This leaves `HasPylonOfType` alone, and with it the
   multiplayer placement path.

   Those same three bytes are **wildcarded in the anchor**, so a cold re-resolve still
   matches once the cheat is applied and it can be turned off after a restart — the trap
   specs 032, 033 and 034 each hit.
6. **The anchor is derived from the JIT'd body**, which means the method has to have been
   compiled first: it is tiny and mono compiles lazily, so it does not exist until a pylon
   placement has been attempted at least once.

   Resolved with the project's own CE workflow (`ce/poc_pylon.lua`, following
   `poc_spawner.lua`) rather than by scanning: `mono_findMethod` + `mono_compile_method`
   gave `0x1AA549E0` in seconds. A blind shape-scan of the executable pages was tried first
   and failed, because **`GetPylonTypeFromPylonTileStyle` is inlined** to two `movzx` — so
   the method contains one real call, not the two the IL suggests. That is the argument for
   reaching for `ce/` first when a method has to be located by name.

   ```
   +00  55 8B EC 83 EC 18     push ebp; mov ebp,esp; sub esp,0x18
   +06  B8 <abs> / F7 00 01…  mono type-init check
   +18  8B 45 14              mov eax,[ebp+0x14]      ; the style argument
   +1B  0F B6 C0 / 0F B6 C8   movzx  (GetPylonTypeFromPylonTileStyle, inlined)
   +21  8B 05 <Main.PylonSystem>
   +33  E8 <HasPylonOfType>
   ```

   `Main.PylonSystem` sits at Main-static **+0xAF0**. `GetPylonTypeFromPylonTileStyle` ends
   `C9 C3` — `leave; ret` with no `ret N` — confirming cdecl, so a bare `ret` in the stub
   leaves the caller to clean the arguments.
7. **Lands in the Build section** of the patch list, next to the other placement cheats.
8. Recorded in the spec-030 ledger with the build key it is confirmed on, and resolved with
   the multi-site rule in case the method is JIT'd into more than one arena.

## Risks & Assumptions

- **The method is lazily compiled**, like `fast_place` and unlike `smart_cursor`. Until a
  pylon has been placed once in a session the anchor resolves to nothing, and the cheat
  honestly reports "matched nothing" (spec 030). Auto-restore's retry loop and the spec-036
  build gate both already tolerate that, but it is the cheat's normal cold state rather than
  an error, and the note should say so.
- **Polarity is the dangerous part.** Returning 1 instead of 0 would forbid every pylon
  rather than allow several. The value is pinned by `badReturn: 1` in the hook registration,
  and a test asserts the stub returns zero.
- **Duplicate pylons are believed harmless but are not proven beyond the list.** The list
  keeps them and the map draws from the list; whether the teleport UI picks sensibly between
  two pylons of one type is a question only play answers. Acceptance requires teleporting to
  both.
- **Multiplayer is untouched.** `NetPlaceEntityAttempt` still enforces one per type, so this
  is a single-player cheat by construction rather than by omission.
- **The world file is unaffected.** Pylons are ordinary tile entities; a world with several
  of a type loads in vanilla, where the extras simply cannot be replaced once broken.
- **Rollback.** `git revert`; the cheat is off by default and disable restores the displaced
  bytes. A game restart clears it. No save-format change.

## Acceptance Criteria

- [x] A `pylons` cheat appears in the **Build** section, off by default, with a short note
- [x] With it on, a second pylon of a type already present can be placed — maintainer
      placed a second Cavern pylon with one already in the world
- [x] Both pylons appear on the map, drawn into the pylon network — verified in-game
      from the maintainer's map screenshot showing two Cavern pylons with network lines
- [x] Disabling restores the displaced bytes and the vanilla one-per-type rule returns
- [x] The stub returns **zero** (a test pins the polarity, since returning one would ban
      pylons entirely; mutation-checked)
- [x] The anchor still resolves with the cheat applied (cold re-resolve), verified live
      against the running game with the patch installed
- [x] Before the method is JIT-compiled the cheat reports "matched nothing" rather than
      failing — the honest reason from spec 030 — and applies on a later pass
- [x] The anchor carries the build key it was confirmed on (`1.4.5.8+24893155`), and
      claimed nothing until it had been
- [x] The cheat persists and auto-restores like the others
- [x] All tests pass headless (347); flake8 clean on changed files; security review recorded
- [x] README updated; version bumped to 0.29.0 (maintainer confirmed)

## Executive Summary

Terraria allows one pylon of each type. That is a placement rule, not a structural one, and
removing it lets you build a pylon network with several waystations in the same biome.

The recon question was whether the constraint is checked in one place or many. One:
`TETeleportationPylon.PlacementPreviewHook_CheckIfCanPlace` is the whole gate for
single-player, eleven IL instructions, and `HasPylonOfType` has only two callers — this hook
and the multiplayer path. **The polarity has to be read rather than assumed**: a method named
`CheckIfCanPlace` returns 1 when placement is *forbidden*, because it is registered with
`badReturn: 1` and `TileObject.CanPlace` rejects when the return equals it. Returning 1 would
have banned pylons outright.

The second question was whether anything downstream assumes uniqueness — a placement that
succeeded but never appeared on the map would be worse than no cheat.
`UpdatePylonsListAndBroadcastChanges` rebuilds the pylon list by walking every pylon tile
entity and adding each with its own position and type, with no dedupe anywhere. Confirmed in
play: two Cavern pylons, both on the map, both wired into the network.

Reviewers: the `pylon_place` anchor (why the first three bytes are wildcarded) and
`ce/poc_pylon.lua`.

## Testing

347 headless tests, flake8 clean on changed files, `pip-audit 2.10.0` clean.

- `tests/test_patcher.py` (+4): the stub returns **zero**, and neither `mov eax,1` nor any
  other non-zeroing form — the mutation that flips it to 1 fails this, and that mutation is
  the one mistake that would ban pylons entirely rather than unlock them; the patch is at
  the method entry with `orig` = `55 8B EC` and the same length as the replacement; the
  anchor wildcards the bytes the cheat overwrites; and it lands in the Build section.
- `tests/test_build_ledger.py` (+1, and a new category): `pylon_place` was derived on
  1.4.5.8 and never existed on the older builds, so it claims neither the derivation build
  nor the 2026-08-23 rebuild. It sat in an `UNPROVEN_ANCHORS` set claiming *nothing* until
  it had been watched working, which is the state the panel renders as unproven; that set is
  kept, now empty, so the next derived-but-unconfirmed anchor has somewhere honest to live.

Live, against the running game:

- the anchor resolves to `0x1AA549E0` — exactly the address CE reported — and uniquely;
- enabling writes `31 C0 C3` at the site, and a **cold re-resolve with the cheat applied
  still finds it**, so it can be turned off after a restart;
- disabling restores `55 8B EC` byte for byte;
- **maintainer-confirmed in play**: a second Cavern pylon placed alongside an existing one,
  both drawn on the map and connected into the pylon network.

Not separately exercised: teleporting to each of two same-type pylons in turn. The map shows
both as network nodes, and the teleport handler works from positions rather than types, but
that is reasoning rather than evidence.
