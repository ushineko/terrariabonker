# Spec 043: Auto-use — press the use button from inside the frame

**Status**: INCOMPLETE — specified, not started.

> **Note**: No issue tracker ticket (personal utility).

Make the game act as though the player pressed the use button, once, on a frame we choose.
That is "swing the held item" — so it serves auto-catch for fishing (spec 042), and equally
auto-fire, auto-place and anything else the use button drives.

## Why this needs a stub and not the poller that already works

The capability is already proven **without** any code patch: `statLife-0x00c6` is the
use-item control, and writing it in a tight loop cast a fishing line with the player's
hands off the mouse (spec 042 records the measurements). So this spec is not about whether
the trick works. It is about doing it properly.

The poller has two problems, and the second is the serious one.

**It burns a core.** The winning write rate was ~400,000 writes/second against
`/proc/<pid>/mem`. A 20 ms burst is ~8,000 writes. That is affordable occasionally and
indefensible continuously.

**It cannot control how many presses happen.** A burst covers every frame it spans, and
each of those frames is a separate press. In testing, a 20 ms burst left the bobber
mid-arc through *repeated* cast-and-reel cycles — the line was being cast and reeled several
times over. For auto-catch the requirement is exactly one reel per bite, and a busy loop
racing the frame cannot promise one of anything. A stub that runs once per frame can.

The poller wins by volume. A stub is correct by construction.

## Acceptance criteria

- [ ] Arming the stub causes exactly **one** use, on the next frame, and then disarms
      itself — verified by a counter the stub increments, not by watching the game.
- [ ] Arming it N times causes N uses. No burst, no repeats, no missed presses.
- [ ] While disarmed, the player's own clicking is completely unaffected — a cheat that
      makes the mouse feel wrong is worse than one that does nothing.
- [ ] Disabling restores the displaced bytes with the game still running, and the game
      keeps running.
- [ ] The stub does not run when no player is loaded (a title-screen frame must not write
      through a null player).
- [ ] CPU cost is indistinguishable from the cheat being off, measured rather than assumed.
- [ ] Fishing auto-catch is built on it and takes exactly one fish per bite.
- [ ] Works from the CLI and the panel, sharing one implementation.
- [ ] Verified in-game on the current build, and the anchor recorded in the build ledger
      (unlike specs 041 and 042, this one patches code, so the ledger applies).

## Design

**Arm a word, fire on the next frame.** The trainer writes a flag in the arena; the stub
checks it every frame, and when set, clears it and sets the use byte. Clearing before
acting is the ordering the ore extractor already uses — the count is consumed before the
work, so a stub that dies mid-batch cannot repeat it.

Sketch, deliberately small:

```
    cmp  dword [ARMED], 0
    je   skip
    and  dword [ARMED], 0          ; one-shot: consume before acting
    mov  eax, [<this>]             ; the Player, from the host method's frame
    test eax, eax
    je   skip                      ; no player loaded: do nothing
    mov  byte [eax + USE_OFF], 1
skip:
    <displaced bytes>
    jmp  back
```

No calls, no stack alignment, no mono ABI to satisfy. That matters: every crash in spec 040
came from a call — the argument order, the frame alignment, or the cave the stub lived in.
This stub calls nothing.

**Two constants to derive at build time**, neither of which is known yet:

- ~~`USE_OFF`~~ **Confirmed 2026-08-26: `Player + 0x672`.** It resolves to exactly
  `statLife-0x00c6` on the live object, so the arithmetic holds against the game and not
  only on paper, and it is an input control by the cheap discriminator: resting value 0,
  and a written 1 wiped by the game in 4.9–20.6 ms across five trials — one frame at
  60 fps. The liveness gate was run first and passed, so this is not spec 042's
  paused-game result.
- ~~The hook site.~~ **Cut 2026-08-26: `Player.Update`'s call to `BordersMovement`.**
  Details below.

## The hook site and its AOB — 2026-08-26

`Player.Update`'s call to `Player.BordersMovement`, at IL_8E19. Chosen because it is the
only call site of that method in the whole of `Update`, it is unconditional (the `netMode`
and shimmer branches above it merge first), and it sits ~50 IL bytes before
`ItemCheckWrapped` — the call that reads the use control. The write lands immediately
before the read, every frame. `ore_extract` owns `GrabItems` at IL_6B2E, which is a
different call much earlier in the same method.

Found by bounding the search rather than sweeping memory: the extractor's `grabitems_call`
anchor is itself inside `Player.Update`, so it served as a landmark and the scan ran
forward from it. The site is `+0x64EA` past it, which is the right direction and distance
for IL_6B2E → IL_8E19. One match in `±0x40000`.

```
anchor    E8 34 5C 24 0A                          call BordersMovement
inject    (anchor + 5)
displace  8B 45 08                                mov eax,[ebp+8]      <- this
          C7 80 FC 03 00 00 00 00 00 00           mov [eax+0x3FC],0    <- numMinions
after     8B 45 08 D9 EE D9 98 00 04 00 00        fstp [eax+0x400]     <- slotsMinions
          8B 05 ?? ?? ?? ?? 3D 02 00 00 00        cmp Main.netMode,2
```

Field offsets fall out of the match and corroborate it: `numMinions` at `this+0x3FC` and
`slotsMinions` at `this+0x400`, adjacent, the second stored with `fstp` exactly as the
IL's `ldc.r4 0` predicts.

**The AOB, with the patch site wildcarded — 1 live site:**

```
E8 ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ??
8B 45 08 D9 EE D9 98 ?? ?? ?? ?? 8B 05 ?? ?? ?? ?? 3D 02 00 00 00
```

Three variants were tested and all resolve to exactly one site: fully literal, patch-site
wildcarded, and no-call-at-all. The wildcarded one is what ships — it is the rule specs
032-034 and 037 each learned the hard way, and unlike `grabitems` this anchor does not have
to break it to stay unique, because the trailing `fstp`/`netMode` context carries the
uniqueness on its own.

**13 displaced bytes, and none of them relative** — `mov eax,[ebp+8]` and a
`mov dword [reg+disp32], imm32`. Both relocate into the stub verbatim; a 5-byte jump plus
8 bytes of padding covers them. The call itself is deliberately *not* displaced, for the
reason recorded on `grabitems_call`: its rel32 differs every session.

**The site hands the stub `this` for free**, which simplifies the design above. `[ebp+8]`
is `Player.Update`'s own argument and it is already being loaded by the first displaced
instruction, so the stub does not have to source the Player pointer from anywhere — and
the null-player guard in the sketch becomes moot, since `Update` is never entered on a null
`this`. The acceptance criterion about a title-screen frame still needs a test; it is now
expected to pass by construction rather than by the check.

**Unverified, and it must be before anything is written:** the call target
(`0x247bd4a0` this session) is identified by shape and position, not by name — nothing here
read mono's metadata to confirm it *is* `BordersMovement`. That does not affect the hook,
which only needs a per-frame site with `this` in the frame, but it must not be written down
as though the symbol were confirmed.

## Risks & Assumptions

- **This is the first new injection since the arena migration.** The arena, slot allocation
  and `_check_site` guard all exist and are proven, so the machinery is not new — but a day
  was lost to two crashes in spec 040, and the mitigations from it apply: place the stub by
  index in the arena rather than searching for a cave, refuse to write a jump over bytes
  that are not what will be restored, and keep the anchor's own patch bytes wildcarded so
  the site can still be found once patched.
- **A cheat that can fire the player's weapons deserves a deliberate choice.** Auto-use is
  not fishing-specific by design, which is its value and its hazard. It should ship switched
  off, and its label should say what it does rather than what it is for.
- **Rollback**: an injection like the others — disable restores the displaced bytes. Nothing
  is written to the world or the save.
- **Assumption**: the use control is a plain byte that the game reads later in the same
  frame it is set. The measurement supports this (a 1 written from outside survives 3–26 ms
  and produced real uses), but the stub writes at a different point in the frame than the
  poller did, and that could behave differently. First test is a counter, not a fish.
- ~~**Unconfirmed input**~~: discharged 2026-08-26. The bite signal is the game's own pull
  condition — `ai[0] == 0 && ai[1] < 0 && localAI[1] != 0` on the player's bobber, read out
  of `Player.ItemCheck_PullFishingBobbers` — and it was confirmed against the maintainer's
  own reel-ins: six seen dips, six clicks, the signal already raised on all six and never
  raised without a real catch behind it. Offsets and measurements are in spec 042, "The
  bite signal, settled".

## The whole cheat, proven with the poller — 2026-08-26

Before building the stub, auto-catch was run end to end from the unprivileged side, to
establish that the only thing left to fix is precision.

Detect a bite with `terrariabonker/projectiles.py`, burst on `Player + 0x672` for 20 ms,
count the fish:

```
bite: Bass (2290)   8208 writes in 20 ms
  Bass: 5 -> 6   (delta +1)
```

Hands off the mouse, a controlled before/after count, one fish. An earlier run watched the
pull path claim the bobber (`ai[0]` 0 → 1) within 50 ms of the burst.

**And the precision problem was measured in the same session.** That earlier burst took
the water from **one bobber to three**: it caught the fish and re-cast twice, because every
frame the burst spanned is its own press. This is exactly the failure this spec predicted
from the bobber's mid-arc behaviour, now with a number on it. The capability is not in
doubt; the count of presses is, and that is what the stub fixes.

## The first run crashed the game — 2026-08-26

Not the stub. The stub never ran. `enable("auto_use")` wrote it **over a live one**.

Arena slots were indexed by `sorted(INJECTIONS)`, so `auto_use` — which sorts first — took
slot 0. `inventory_accs` already held slot 0 in an arena left over from the running
session, enabled, with the game jumping into it every frame. 58 bytes went over code that
was executing. From then on `UpdateEquips` jumped into auto-use's stub, which ends by
replaying auto-use's displaced bytes and jumping back into `Player.Update`; the game ran on
with that control flow until a chum-bucket dictionary came back null a few frames later.
The `NullReferenceException` in `OnPreUpdateAllProjectiles` was the symptom, and it names
nothing that would lead an investigator back here.

The state file made it unambiguous — both injections recorded the same cave address:

```
"auto_use":       cave 1744834560   (0x68001000)
"inventory_accs": cave 1744834560   (0x68001000)
```

**The test suite caught this before the game did, and it was misread.** Adding the
injection broke an unrelated patcher test with "no player found", because the same shift
moved a stub onto the planted player in the synthetic image. That was fixed as a fixture
overlap — true, and beside the point. The commit message even states the mechanism
("arena slots are indexed by sorted injection name, so adding one moves every other slot")
while treating it as an artifact of the test image. It is a live-code hazard: an arena
outlives the trainer process that made it, and the stubs in it are running.

**Fix, in two parts.** `_SLOT_ORDER` is now an explicit append-only tuple, so a slot's
address is decided by position in a list nobody may reorder, and adding an injection cannot
move an existing one. `ARENA_MAGIC` is bumped to `TBARENA2`, so an arena stamped by the old
numbering is ignored rather than adopted — that costs 64 KB and avoids exactly this
overwrite during the transition.

Five regression tests cover it: every injection has a slot, slots never overlap, appending
moves nothing, the tuple is not merely sorted (so re-tidying it into `sorted()` fails), and
the stamp changed with the layout.

**The rule this earns**, alongside spec 040's: *never write a stub into a slot without
establishing what is in it.* Placing by index instead of searching for a cave was supposed
to make placement safe, and it does — but only against caves, not against the previous
version of yourself.

## Alternatives considered

- *Keep the poller.* Rejected for the precision problem above, not the CPU one.
- *Synthesise a mouse click.* Rejected in spec 042: the trainer edits memory and does not
  drive input, Wayland restricts synthetic input, and a program that moves the player's
  mouse is a different kind of thing from this one.
- *Do the bite detection inside the stub too.* Rejected for now. It would remove the
  trainer from the loop entirely, but it means walking the projectile array in assembly,
  and the unprivileged side already does that reading comfortably. Arm-and-fire keeps the
  asm at a dozen instructions.
