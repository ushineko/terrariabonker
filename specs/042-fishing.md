# Spec 042: Fishing — a kit, bait that never runs out, and no waiting

**Status**: INCOMPLETE

> **Note**: No issue tracker ticket (personal utility).

Three related cheats behind one group, because nobody wants one of them on its own:

1. **Set me up to fish** — if you have no rod, you get one; if you have no bait, you get
   some. Fish anywhere without having gone shopping first.
2. **Fishing power** — a configurable number rather than whatever your rod happens to be.
3. **Insta-fishing** — the bobber catches at once instead of waiting for a nibble.

## Acceptance criteria

- [ ] With no rod and no bait, switching the cheat on leaves you able to fish.
- [ ] Bait never runs out while the cheat is on: the stack does not drop, however many
      casts are made.
- [ ] Fishing power is a tunable, and the rod in hand reports the configured number.
- [ ] Insta-fishing: a cast produces a catch without the usual wait, repeatedly.
- [ ] Switching the cheat off restores normal fishing — the wait comes back and bait is
      consumed again — with the game still running.
- [ ] Nothing is given twice: toggling the cheat repeatedly does not fill the inventory
      with rods, and a rod the player already carries is used rather than duplicated.
- [ ] The cheat never overwrites a slot holding something else.
- [ ] Works from the CLI and the Effects tab, sharing one implementation.
- [ ] Verified in-game on the current build, with the build recorded in the ledger.

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
- **Insta-fishing**: find the live bobber in `Main.projectile` (`bobber` set, and active)
  and clear whatever holds its remaining wait. One write per bobber per round.

## Recon still needed

- **The bobber's wait timer.** Which field counts down between cast and nibble is not
  known. Method: cast a line, sample the live bobber's struct repeatedly, and take the
  field that decreases while the player is fishing and stops when they are not. This is
  the same differential that found the buff arrays, and it has the same trap — a sample
  taken while the game is paused shows nothing moving and proves nothing, so the probe
  must carry its own liveness control.
- **Whether the catch is worth influencing separately.** Fishing power decides quality;
  insta-fishing decides speed. If they turn out to be the same field in practice, the two
  toggles should be merged rather than shipped as a distinction that is not real.
- **`Main.projectile`'s address.** Found structurally during recon (a 1001-element object
  array whose elements share a vtable), but that search has not been made repeatable or
  tested. It needs the same treatment as the other locators before anything depends on it.

## Risks & Assumptions

- **Item edits persist.** Writing fishing power to a rod changes an item in the player's
  save, unlike everything else in this cheat. That is how the existing item editor already
  behaves, and `profile.py` restores such edits — but it means "switch it off" does not
  put the rod's original power back unless we record it. Decide before building: either
  record and restore the original value, or say plainly in the README that the rod stays
  upgraded.
- **A byte field caps the tunable at 255.** Higher is not "more"; it wraps.
- **Rollback**: no patching, so nothing to restore in the game's code. Given items and an
  edited rod are the exceptions, per the point above.
- **Assumption**: bait is consumed by decrementing the stack. If it is consumed some other
  way, pinning the stack will not be enough and the criterion must change rather than the
  claim being quietly weakened.
