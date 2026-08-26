# Spec 042: Fishing — a kit, bait that never runs out, and no waiting

**Status**: COMPLETE — a rod and bait if you have none, bait that does not run out, and a rod power that makes fish bite about once a second and is put back when you switch off. Auto-catch is deliberately out of scope and is written up below for whoever picks it up.

> **Note**: No issue tracker ticket (personal utility).

Three related cheats behind one group, because nobody wants one of them on its own:

1. **Set me up to fish** — if you have no rod, you get one; if you have no bait, you get
   some. Fish anywhere without having gone shopping first.
2. **Fishing power** — a configurable number rather than whatever your rod happens to be.
3. ~~**Insta-fishing**~~ — folded into (2). Bites come about once a second at high
   fishing power, so it is the same lever, not a second one. See the recon below.

## Acceptance criteria

- [x] With no rod and no bait, switching the cheat on leaves you able to fish. *(The kit
      gives a Golden Fishing Rod and 30 Master Bait; unit-tested, not yet run in-game.)*
- [x] Bait never runs out while the cheat is on. *(Any stack below the configured floor
      is topped back up to it; every low stack in one round, not just the first.)*
- [x] Fishing power is a tunable, and every rod carried is raised to it. *(Ceiling 255,
      the byte's own limit; the maintainer chose to ship that rather than 125.)*
- [x] At the configured power, bites come fast enough to be worth calling instant.
      *(One catch a second at 255 against a trickle at the same rod's own 50 — the
      maintainer's measurement, and what the published counter formula predicts.)*
- [x] Switching the cheat off restores the rod's original power, with the game still
      running, and a rod left raised by a killed trainer is put back on the next start.
- [x] Nothing is given twice, and a rod the player already carries is left alone.
- [x] The cheat never overwrites a slot holding something else. *(`give_item` picks a
      free slot from the live inventory, which is the fix from the earlier data loss.)*
- [x] Works from the CLI and the Effects tab, sharing one implementation. *(The panel
      calls the same single round the CLI does, from a 1s timer.)*
- [x] Verified in-game on 1.4.5.8+24893155. *(Nothing goes in the build ledger: that
      records anchors, and this cheat patches no code. The item offsets are still
      build-specific and would need re-deriving after an update — `docs/item-fields.md`
      records how they were found.)*

## Context

This follows passive potions (spec 041): a trainer-held cheat rather than a code patch,
for the same reasons. Nothing here needs an anchor, a hook site or an arena slot, and
switching it off is free.

**The fishing data is real data.** Recon on 1.4.5.8+24893155 confirmed all three offsets
the design needs, each against the game's own numbers rather than a guessed pattern:

| Field | Offset | Evidence |
|---|---|---|
| `Item.fishingPole` | `+0x058` | exactly 7 items carry it, all rods — Golden 50, Hotline 45, Sitting Duck's 40, Mechanic's 35, Fiberglass 30, Scarab 30, Chum Caster 25 |
| `Item.bait` | `+0x05C` | 13 items, no rods — Gold Worm / Master Bait / Gold Grasshopper 50, Sluggy 25, Firefly 20, Snail 10 |
| `Projectile.bobber` | `+0x088` | true for 19 of 1100 projectile templates, including every bobber the seven rods shoot (362, 364, 365, 366, 382, 760, 775) |

This is the opposite of the accessory finding in `docs/item-fields.md`: there was no
movement-speed number to edit, but there *is* a fishing-power number.

**Giving the kit is already solved.** `give_item` has placed fully-statted items since
v0.2.2 by copying a ContentSamples template into a free slot, and it is what the
compendium's spawn button uses. The kit is a Golden Fishing Rod (2294) and a stack of a
50-power bait — Master Bait (2676) or Gold Worm (2895).

**Sources for the mechanic** (not derived here — read from the community wiki and used to
explain measurements taken in-game):
<https://terraria.wiki.gg/wiki/Fishing>

## Requirements

- **Give nothing the player already has.** Check for a rod (`fishingPole != 0`) and for
  bait (`bait != 0`) before placing anything, and never write over an occupied slot. Read
  the *live* inventory, not a copy — a stale copy is what caused the `give_item` data loss
  fixed earlier (see `_live_inventory`).
- **Fishing power** is written to the rod the player is holding. It is a **byte**, so the
  tunable is capped at 255 and the `ValueSpec` must say so rather than letting a spinbox
  offer a number that silently truncates.
- **Bait**: hold the stack at what it was, the same shape as the potion renewal. Do not
  write when the stack has not dropped.
- **No separate insta-fishing path.** Bite rate follows fishing power; there is nothing
  else to write.

## Live recon, 2026-08-25

Confirmed at a pond on 1.4.5.8+24893155:

- **The bobber is identifiable live.** `Main.projectile` was located structurally, and the
  cast bobber came back as projectile **type 362** — exactly what the Fiberglass rod's
  `shoot` field says it fires. Item → projectile → live object, end to end.
- **`fishingPole` is writable and restores cleanly.** 30 → 255 → 30, each step verified by
  reading back. This is the record-and-restore path working before anything was built on
  it.
- **Bait is consumed by decrementing the stack** (30 → 23 → 21 → 17 across the session), so
  pinning the stack is the right mechanism. This was an assumption in Risks and is now a
  measurement.
- **A catch needs the player to reel in.** That bounds any "insta-fishing" claim: the rate
  is limited by clicking, not only by the game's willingness to give a bite.

**Fishing power drives the bite rate — settled.** The maintainer watched it: bites came
about once a second at power 255, against a trickle at 30. The published mechanic explains
both numbers exactly, and corrects a wrong call made here.

There is a hidden **catch counter** that counts *up*. Per tick it gains 1–2 (base), plus
`fishingPower / 30`, plus a `(fishingPower / 3)%` chance of another 1–2, plus a 1-in-60
chance of +60. When it passes **660** a bite is rolled at `(75 + fishingPower) / 2` percent,
and the counter resets either way.

| Power | Counter gain/tick | Time to 660 | Observed |
|---|---|---|---|
| 30 | ~2.5 | ~4.4 s | the 4–5 s cycle seen in the bobber fields |
| 255 | ~10 | ~1.1 s | "a bite each second" |

That cycle at `+0x078`/`+0x0B4`/`+0x100`/`+0x128` was written off here as idle bobbing. It
almost certainly *was* the counter cycling, and the arithmetic says so. The reason the
probe missed it is that it searched for a field counting **down**; nothing here does.

**The counter was hunted and not found, and no longer needs to be.** Three probes at a
proper lake:

- The Player object across `statLife ± 0x1200`, scanned for any int that climbs and
  resets: one candidate at `statLife-0x49C`, ruled out because it keeps ticking with no
  line in the water (126 changes in 20 s). It is a general timer, and the apparent
  correlation came only from the bobber happening to be out the whole time.
- The bobber across its first `0x600` bytes, as **int and as float**: nothing climbs and
  resets at all.

So it is not a simple ramping value in either object, and the search would have to move to
Main's statics next. That work is unnecessary, because **insta-fishing and fishing power
are the same lever**: the maintainer measured about a bite a second at power 255 against a
trickle at 30, which is what the published counter formula predicts. There is no second
mechanism to find.

**Insta-fishing is therefore folded into the fishing-power tunable** rather than shipped as
its own toggle. Two switches implying two mechanisms would be a distinction the game does
not have.

Still open:

- **Whether the game clamps fishing power before using it.** The published bite *chance*
  caps at 125 (which is 100%), but the counter term is `power / 30` and is not obviously
  capped — the 255 result behaved faster than a 125 cap would allow. This decides whether
  the tunable's ceiling is 125 or 255, and is answerable with an A/B at those two values.

### Two broken instruments, recorded because both nearly became findings

A probe reported **zero catches in 90 seconds** while the player was in fact catching fish
— the bait stack and seven new fish in the bag proved it afterwards. The gap-detector was
wrong, not the game.

Worse, a write of `fishingPole=255` silently missed: the rod's address had been cached from
an earlier read and mono's GC had moved the object. The script printed `fishingPole was 0`
where 30 was expected and carried on regardless. **Locate an item by identity on every
access, never by a cached address** — the lesson spec 038 already exists for — and treat an
unexpected pre-value as a reason to abort, not a value to record.

### In-game, 2026-08-25 (kit and bait top-up)

- **The kit works.** The maintainer threw away their rod, ticked the cheat, and the panel
  logged `gave you a rod (slot 9)` — a Golden Fishing Rod, power 50.
- **It leaves owned gear alone**: run against a player who already had a rod and bait, it
  gave nothing and said so.
- **Bait is held up.** Both stacks were topped in one round (15 → 30 and 3 → 30), and it
  kept a single stack at 30 through continuous fishing.
- **The trash is not part of `Player.inventory`.** A bait moved to the trash vanished from
  what the cheat sees, which is the behaviour wanted — a rod someone threw away must not
  read as "you already have a rod". Slots 50–57 are coins and ammo; 58 read empty and its
  purpose is still unknown. No special-casing was added, because none is needed.
- **One flaw found and fixed**: every bait consumed logged its own `29 -> 30` line, which
  buries the panel within minutes of fishing. It now says it once per stack.

Still unverified: whether an item held on the mouse cursor is absent from the inventory
array. If it is, a player dragging their rod when the first round fires would be given a
second one. The exposure is one round per enable, and no fix has been attempted because
the premise has not been established.

## Recon still needed

- **The bobber's wait timer.** Which field counts down between cast and nibble is not
  known. Method: cast a line, sample the live bobber's struct repeatedly, and take the
  field that decreases while the player is fishing and stops when they are not. This is
  the same differential that found the buff arrays, and it has the same trap — a sample
  taken while the game is paused shows nothing moving and proves nothing, so the probe
  must carry its own liveness control.
- **Whether a player-side fishing field exists, which would make the item edit
  unnecessary.** If the player carries a fishing-power value that the game recomputes each
  frame from rod and bait, pinning it is a held effect that evaporates on its own — exactly
  how the potion buffs behave — and then nothing touches the save, the restore bookkeeping
  above disappears, and the byte cap on the tunable goes with it. **Check this before
  building the record-and-restore path**, because it would be work done to solve a problem
  that need not exist. The item edit is the fallback, not the first choice.
- **Whether the catch is worth influencing separately.** Fishing power decides quality;
  insta-fishing decides speed. If they turn out to be the same field in practice, the two
  toggles should be merged rather than shipped as a distinction that is not real.
- **`Main.projectile`'s address.** Found structurally during recon (a 1001-element object
  array whose elements share a vtable), but that search has not been made repeatable or
  tested. It needs the same treatment as the other locators before anything depends on it.

## Auto-catch — not yet attempted

Reeling in is still a manual act: the cheat makes fish bite constantly, and the player
clicks. Automating that is a separate question and is **not ruled out** — it has simply not
been investigated. It splits in two, and only one half is hard.

**Detecting a bite looks solved already.** While sampling the bobber, four fields moved
together in a repeating cycle — `+0x078` dropping 1 → 0 and `+0x0B4` 59 → 0, with `+0x100`
and `+0x128` flipping. That fired 8 times in 40 seconds, which matches the bite count over
the same window from an independent measurement. It reads as the bite event, though it has
not been confirmed against a bite the maintainer called out at the moment it happened.

**The generic route was tried: drive the player's "use item" control.** The maintainer's
suggestion, and the right instinct — it is not fishing-specific, so it would serve auto-fire
and auto-place too. Recon so far:

- A candidate for `controlUseItem` at **`statLife-0x00c6`**: usually 0, and held at 1 for a
  4.1 s unbroken run while the mouse button was held down. Five other bytes that toggled in
  a first pass never set at all in a second, so they were noise.
- **Writing it did not cast a line.** With a rod selected and the player not clicking, the
  byte was written at 100 Hz for three seconds and no bobber appeared.

Two explanations, and the second is likelier:

1. *A frame race.* The game sets the flag from input at the top of a frame and reads it
   later in the same frame; a poller outside the game lands wherever it lands. This is why
   an in-game stub is the reliable way to drive input flags, and an outside write is not.
2. *A held flag is not a press.* A fishing rod is not auto-reuse, and Terraria requires a
   fresh press for such items — `controlUseItem` together with `releaseUseItem` — so a byte
   pinned at 1 reads as "still holding since last frame", which is deliberately ignored.

### Result: the use-item action can be driven from memory

Confirmed in-game on 1.4.5.8+24893155. **`statLife-0x00c6` is the use-item control.**

- It is rewritten every frame: a 1 written into it is gone in 3–26 ms, averaging about one
  frame. That is what an input control looks like and nothing else does.
- With a rod selected, the player not touching the mouse, and no bobber in the water,
  writing it in a tight loop for two seconds **cast the line**.
- With a bobber already out, bursts as short as 20 ms moved it 150-odd pixels, alternating
  between two positions — the arc of repeated cast-and-reel cycles caught mid-flight.

**Write rate is the whole trick, and it is why the first attempt failed.** 100 Hz did
nothing; the tight loop managed ~400,000 writes/second. The game sets the flag from real
input at the top of each frame and acts on it later in the same frame, so a write only
counts if it lands in that window. Hammering wins by covering every frame many times over
rather than by aiming.

**This is not fishing-specific**, which was the point of the maintainer's suggestion: it is
"use the held item", so it applies to auto-fire, auto-place and anything else driven by the
use button.

**What it must not become**, though, is a busy loop left running. 400k writes/second is a
core spinning on `/proc/<pid>/mem`. A 20 ms burst is ~8,000 writes and is enough, so the
shape for a real cheat is: detect the event, burst briefly, stop. For auto-catch that means
bursting on a detected bite, not hammering continuously — and the cost of a burst should be
measured before it ships, not assumed to be free.

**A cheap discriminator, ready to run.** Before hunting `releaseUseItem`, settle whether
`statLife-0x00c6` is an input control at all — an input control is rewritten *every frame*
from the keyboard and mouse, so a 1 written into it must be gone within ~17 ms. Write 1,
then poll: cleared within a frame means the game is writing it and the offset is plausible;
still 1 after 250 ms means nothing rewrites it, so it is not an input control and the
search moves on. No fishing required, only a running game.

*This test must carry a liveness check.* Run once against a paused game it reported "still
1 after 300 ms" across five trials — a clean, confident, meaningless result, because a
paused game rewrites nothing. Sampling `statLife-0x49C`, which increments every tick
regardless of what the player is doing, is enough of a guard and turned the same test into
an honest "aborting: a paused game clears nothing".

If it does turn out to be an input control, the next attempt is `releaseUseItem` — likely
adjacent, since the control flags sit together — driving the pair as a press rather than a
hold.

**A concern that may sink this route whatever the offsets are.** The game rewrites input
flags from real input every frame, so a write from outside only matters if it lands in the
window between the game reading input and the game acting on it. That is a fraction of a
frame, and a poller cannot aim at it. If the press-and-release attempt also fails, the
answer is probably not a better offset but the wrong mechanism: driving input belongs in a
stub that runs *inside* the frame, which is the code-patch route below.

**Triggering the reel-in** is answered by the above: drive the use control. The routes
below are kept for the record, and the third is now moot.

- *Replicate what reeling in does.* Find what the game changes when the player clicks with
  a bite on, and do the same. Best fit for this project — memory only, no new machinery —
  but it needs the catch path understood, and that path is where the fish is actually
  granted, so getting it wrong could destroy a catch rather than take it.
- *A code patch on the fishing check.* Powerful and in keeping with the older cheats, but
  it needs an anchor and a stub, which the whole of spec 041 and this spec so far have
  avoided.
- *Synthesising a mouse click.* Rejected: the trainer edits memory and does not drive
  input, Wayland restricts synthetic input anyway, and a cheat that moves the player's
  mouse is a different kind of program from this one.

Worth knowing before starting: a share of catches is lost to the line breaking — the wiki
puts it at one in seven without the right accessory, the maintainer observed nearer one in
five in play, and High Test Fishing Line removes it. The bait is consumed either way. That
costs the player nothing here, because the bait pin tops the stack back up regardless of
whether the catch landed, but an auto-catch that silently swallows a break will look
broken, and the counting must not treat a break as a failure of the cheat.

## Risks & Assumptions

- **Item edits persist, so the original power is recorded and restored** (maintainer's
  decision). A group sitting under "Effects" must not quietly leave one permanent change
  behind; everything else there evaporates when the trainer closes, and this has to match.

  Note that `profile.py`'s `item_edits` is the **wrong** mechanism: it exists to
  *re-apply* an edit after a game restart, which is the opposite intent. This needs its own
  record of the pre-cheat value.

  The bookkeeping has one hard case: the trainer is closed or killed while the cheat is on,
  so nothing restores the rod and the original is lost. Persist the recorded value rather
  than holding it in memory, and restore any pending entry on the next start.
- **A byte field caps the tunable at 255.** Higher is not "more"; it wraps.
- **Lake size is a bigger lever than the cheat.** A body of water under 300 tiles is
  penalised multiplicatively, and a 75-tile pond takes roughly −75%. A player fishing in a
  puddle will see a fraction of what the configured power promises, so the README must say
  where to fish rather than let the cheat look broken.
- **Bait power lowers how often bait is consumed**, so a high-power bait is worth giving in
  the kit for its own sake, independently of pinning the stack.
- **Some catches are lost to the line breaking** — around one in five in play, one in
  seven per the wiki, none at all with High Test Fishing Line. The bait goes anyway, which
  the bait pin already covers. Not a bug in the cheat when it happens, and worth saying so
  before someone reports it as one.
- **Rollback**: no patching, so nothing to restore in the game's code. Given items and an
  edited rod are the exceptions, per the point above.
- ~~**Assumption**: bait is consumed by decrementing the stack.~~ Measured; see above.
- **A catch is a manual act.** The player casts and reels in. Any wording that suggests
  fish arrive on their own would oversell what this can do, in the README and in the UI.
