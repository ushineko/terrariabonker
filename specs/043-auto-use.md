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
- The hook site. `ore_extract` already occupies the `GrabItems` call inside `Player.Update`,
  and two jumps at one site is the thing to avoid, so this needs its own per-frame site
  where the Player is reachable.

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

## Alternatives considered

- *Keep the poller.* Rejected for the precision problem above, not the CPU one.
- *Synthesise a mouse click.* Rejected in spec 042: the trainer edits memory and does not
  drive input, Wayland restricts synthetic input, and a program that moves the player's
  mouse is a different kind of thing from this one.
- *Do the bite detection inside the stub too.* Rejected for now. It would remove the
  trainer from the loop entirely, but it means walking the projectile array in assembly,
  and the unprivileged side already does that reading comfortably. Arm-and-fire keeps the
  asm at a dozen instructions.
