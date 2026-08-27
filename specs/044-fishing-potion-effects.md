# Spec 044: Fishing potion effects, without the potions

**Status**: COMPLETE — the three fishing potions' effects as three independent switches,
deferring to any real potion and to the passive-potions cheat.

> **Note**: No issue tracker ticket (personal utility).

Three checkboxes beside the fishing cheat: **Fishing power**, **Sonar** and **Crates**.
Each holds the buff its potion grants, for as long as it is ticked, without the potion.

## Acceptance criteria

- [x] Each of the three effects is its own switch and works alone. *(Independent
      checkboxes on their own timer; `--power`, `--sonar`, `--crate` on the CLI.)*
- [x] They work without the rod-and-bait cheat being on. *(Separate group box, separate
      timer; a test asserts ticking one does not turn the fishing cheat on.)*
- [x] **A potion the player drank takes precedence, and so does the passive-potions
      cheat.** *(Verified live: a Fishing Potion buff at 28800 ticks was untouched across
      three rounds and reported as deferred.)*
- [x] Switching an effect off never cancels a buff — it lapses on its own. *(Nothing
      writes a zero; a test pins that a round with nothing ticked writes nothing at all.)*
- [x] The buff ids are read from the game rather than guessed. *(ContentSamples item
      templates: 2354/2355/2356 carry buffType 121/122/123.)*
- [x] Works from the CLI and the panel, sharing one implementation. *(`fishing-buffs`
      blocks with `--watch`; the panel drives the same round from a 1s timer.)*
- [x] Verified in-game on 1.4.5.8+24893155. *(All three added, then held; no anchor, no
      stub, nothing for the build ledger.)*

## Context

This is passive potions (spec 041) pointed at three specific buffs, with the potion
requirement removed. A buff is a `(type, ticks)` pair the game counts down, so holding one
up is exactly what standing by a campfire does — which is also why switching off needs no
cleanup at all.

**Why not just favorite a potion.** The passive-potions cheat already holds any potion's
buff up, but it needs the potion in the bag. These three are the ones a fishing session
wants and the ones a player is least likely to have stacks of.

## Precedence

The maintainer's requirement: the potion trainer, and by extension any potion actually
drunk, beats this cheat.

It falls out of `Buffs.renew` refusing to shorten a buff — a slot running longer is left
completely alone rather than rewritten. So an eight-minute Fishing Potion stays eight
minutes; this never cuts it to the two seconds it renews on, and never removes a buff at
all.

That is inherited behaviour, so it is pinned by its own test rather than left to follow
from something else: *"it follows from X"* stops being true the moment X is refactored.

**One reporting bug, found live.** `renew` returns `kept` for two different situations —
something else owns the buff, and our own renewal from a second ago has not run out yet —
and the first version reported both as "a potion is already running it". A second round a
moment after the first claimed a potion was running Sonar when the only thing running it
was the round before. The clock is now read *before* renewing, and only a buff running
longer than this cheat would ever set counts as somebody else's.

## Alternatives considered

- **Stop renewing when a rod is not held**, so the effects lapse outside fishing.
  Considered and rejected. The gain is cosmetic — a tidier buff bar, and the icons already
  say what each effect is — while the cost is a real failure mode: the catch counter reads
  `FinalFishingLevel` every tick the bobber is out, not once at the cast, so a player who
  casts and then switches to a torch for a moment would silently lose fishing power
  mid-cycle. That reads as "fishing is worse sometimes", which nobody would attribute to
  the trainer.

  The asymmetry with auto-catch's rod gate is the point: that one exists because an
  ungated press swings the player's sword, which is harm. A buff cannot do anything to
  anyone, so gating it for consistency would inherit a risk to fix an appearance.

  If it is ever wanted, the condition must be *rod held **or** bobber in the water*, never
  rod alone — both halves already exist (`Inventory.holding_rod`, `projectiles.find_bobbers`).

## Risks & Assumptions

- **Buff ids are content, not offsets.** They come from the game's own item templates, so
  they are as stable as item ids are, and a game update that renumbers buffs would need
  them re-derived — the same exposure as `KIT_ROD`.
- **The effects are held, not permanent.** They lapse a couple of seconds after the last
  renewal, which is the same shape as passive potions and needs no restore bookkeeping.
- **Rollback**: nothing is patched and nothing is written to the save.
- **Sonar names what is biting**, which overlaps what auto-catch already reads out of the
  bobber. That is a coincidence of purpose, not a dependency: neither uses the other.
