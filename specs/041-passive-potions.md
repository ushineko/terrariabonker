# Spec 041: Passive potions — buffs from a favorited potion in the bag

**Status**: INCOMPLETE

> **Note**: No issue tracker ticket (personal utility).

Carry a potion and get its effect. The potion is not consumed, not used, and not moved to
a slot — it sits in the inventory and the buff stays up. Two gates keep it deliberate: the
potion must be **favorited** (alt-click), and the stack must reach a configurable size.

The favorite gate is the point of the design, not a detail. Without it, every potion the
player picks up starts doing something, which is exactly the behaviour nobody wants from a
loot game. Favoriting is already the gesture for "this is mine and it stays here", it is
one keystroke, and it is visible in the UI — so the cheat is opt-in per potion, by the
player, with no extra interface of our own.

## Acceptance criteria

- [ ] A favorited consumable potion in the inventory grants its buff, without being used
      and without the stack shrinking.
- [ ] An **unfavorited** potion grants nothing, however large the stack. Favoriting it
      while the game runs starts the effect; unfavoriting stops it.
- [ ] The stack threshold is a tunable, minimum 1, and a stack below it grants nothing.
- [ ] The buff lapses on its own shortly after the potion leaves the inventory, is
      unfavorited, or the cheat is switched off — no bookkeeping, no stuck buffs.
- [ ] Non-consumables are inert: a favorited pet, light pet or mount item does nothing,
      even though it carries a `buffType`.
- [ ] Nothing is consumed and no stack is decremented, ever — verified in-game across a
      session, not only by reading the stub.
- [ ] No audible or visual spam: the buff is applied quietly, not re-announced every
      frame.
- [ ] **Drinking a potion normally is never degraded by the cheat.** A buff the
      player applied at its full duration keeps that duration: the per-frame refresh
      may extend a buff, never shorten one. Verified in-game by drinking a potion the
      player also carries favorited, then dropping the stack — the drunk buff must run
      out its own clock.
- [ ] Disabling restores the displaced bytes and stops the effects with the game running.
- [ ] Works from the CLI and the trainer panel, sharing one implementation.
- [ ] Verified in-game on the current build, with the build recorded in the ledger.

## Context

This is the third cheat in the "inventory acts like a slot it is not" family, after
inventory accessories (spec 033) and vanity accessories (spec 032). It reuses their shape:
a per-frame walk over the 58 inventory slots that the game already performs, with our work
added for the items that qualify.

**No potion table.** The obvious implementation is a map from item id to buff id, and it
is the wrong one — it would need maintaining forever and would be wrong the first time the
game adds a potion. Terraria stores `buffType` on the item itself, so the stub reads what
the game would have used had the player drunk it. Every potion works, including ones that
did not exist when this was written.

**Why a short duration, refreshed.** A small buff time, renewed while the potion qualifies,
makes "off" free: stop renewing and the buff expires by itself a moment later. The
alternative — a long duration plus explicit removal — needs the cheat to know every buff it
ever granted and to still be running when the player drops the potion, and gets it wrong
when the game exits mid-buff. This is not a trick: the game already does exactly this for
station buffs. A campfire's buff sits at one tick and is renewed every frame, which is why
it lapses the moment you walk away.

**Why no code injection.** A buff is a `(type, time)` pair the game counts down, so the
trainer can write it directly on a timer, the way the vein watcher already drives the
extractor. That removes an anchor, a hook site, an arena slot, a stub, and the whole class
of crash this project spent a day on. It also makes the "never shorten a buff the player
drank" rule a read-compare-write in Python instead of array logic inside a stub. The cost
is that the effect stops when the trainer is closed, unlike the patch-based cheats — the
same trade the vein watcher already makes. Decided with the maintainer; calling the game's
own `AddBuff` from an injection was the alternative.

## Requirements

- Gate each item on **all** of: `favorited`, `stack >= threshold`, `consumable`, and
  `buffType != 0`. The `consumable` test is what keeps pets and mounts out (decided with
  the maintainer; anything with a buff was the alternative and was rejected as too easy to
  trigger by accident).
- Write `Player.buffType[i]` / `Player.buffTime[i]` directly, from the trainer, on a timer
  fast enough that a short buff time never lapses between refreshes.
- **Never lower an existing buff time.** Read the slot first; if the buff is already
  running longer than the refresh window, leave it alone. This is what keeps a potion the
  player drank at its full duration, and it is why the direct-write approach is the safer
  one — the check is trivial here and would be stub logic otherwise.
- One tunable: the minimum stack, default and bounds set in `ValueSpec`, minimum 1.
- Cheap rejection first. The host loop runs 58 times a frame; the common case is a stack
  of dirt and must cost a byte test, not a call. Order the gates cheapest-first.

## Recon needed before implementation

None of these are known yet, and each is a place the work can stall:

- ~~`Item.favorited`, `Item.stack`, `Item.consumable` and `Item.buffType` field offsets.~~
  **Done.** `stack` (0x88), `consumable` (0xBD) and `buffType` (0x130) were already mapped
  and are confirmed live against six real potions; `favorited` is 0x70, derived from three
  snapshots tracking the player's alt-clicks. The premise holds: every potion carried a
  distinct `buffType` and `consumable=1`, and the non-potion beside them read zero for
  both, so the gate separates cleanly with no item table.
- ~~`Player.buffType[]` and `Player.buffTime[]` offsets.~~ **Done.** `statLife-0x670` and
  `statLife-0x66C`, each to a 44-element int array — not 22, which is what a first search
  assumed and why it found nothing at all. Confirmed by watching a buff appear: drinking a
  potion put `(type 6, time 28798)` into a free slot, eight minutes minus the two frames it
  had already counted down. The count-down is the part that matters — five earlier samples
  read the arrays as frozen, every one of them taken with the player standing still or the
  game paused, and none of them was evidence of anything.
- What the refresh interval and buff time should be. They trade against each other: the
  time must outlast the interval by enough margin that a stalled trainer does not make the
  buff flicker.
- Whether the game rejects or overwrites a buff slot we populate, and what happens when all
  44 are full.

`Player.AddBuff` is no longer needed and is deliberately not on this list: nothing calls
it, so whether it overwrites or extends stopped mattering.

## Risks & Assumptions

- **Buff slot pressure.** The game caps active buffs (22). A player favoriting many
  potions could fill it and crowd out buffs they wanted. Not a correctness problem, and
  the player controls it with the favorite gate, but worth a line in the README.
- **Rollback**: the cheat is an injection like the others — disable restores the displaced
  bytes and the buffs lapse on their own. No world or save state is written, so there is
  nothing to undo beyond switching it off. This is not the ore extractor.
- **Per-frame cost** is the main technical risk. `inventory_accs` already showed that a
  cheap field test before an expensive call is the difference between playable and not.
- **Interference with normal play is the sharpest risk here.** Every other cheat in this
  family adds an effect; this one re-applies a value the game is already managing, on a
  timer, sixty times a second. The failure mode is not that the cheat does nothing, it is
  that it quietly degrades something the player did by hand -- and they would reasonably
  blame the potion, not the trainer. Hence the explicit criterion above.
- **Assumption**: `buffType` on a consumable is always the buff drinking it would grant.
  If some consumable uses the field differently, that item behaves oddly; the favorite
  gate keeps it contained to items the player chose.
