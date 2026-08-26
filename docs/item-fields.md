# What an item actually knows about itself

Recon, 2026-08-25, on Terraria 1.4.5.8+24893155. The question: are per-item behaviours —
the Book of Skulls passing through blocks, Hermes Boots making you run faster — visible as
data the trainer can read and edit?

The answer is three-tiered, and the tiers behave very differently. Weapons carry a lot of
structured data. Projectiles carry a second layer that holds most of what makes a weapon
feel distinctive. Accessories carry **nothing at all**.

## Tier 1: weapons — structured, on the Item

These sit on the `Item` object beside the fields the trainer already edits. Offsets are
from the object base; verified against items whose real values are known, not read off a
pattern.

| Offset | Field | Evidence it is what it says |
|---|---|---|
| `0x078` | `useStyle` | 5 for the two guns/books, 1 for swung items |
| `0x0B0` | `knockBack` (float) | 3.5 Book of Skulls, 6.6 Boomstick |
| `0x0CC` | `scale` (float) | 0.9 Book of Skulls, 1.0 everything else |
| `0x0FC` | `shoot` | projectile id; Starfury 9, Water Bolt 27, Space Gun 20 |
| `0x100` | `shootSpeed` (float) | Space Gun 10.0, Starfury 25.0 |
| `0x10C` | `useAmmo` | Pulse Bow 40 = Wooden Arrow, Boomstick 97 = Musket Ball |
| `0x11C` | `mana` | Space Gun 6, Water Bolt 10 — both match the wiki exactly |
| `0x124` | `value` | sell price in copper; Wooden Sword 100, Water Bolt 75000 |

`0x150` looked like `crit` (7 on the Pulse Bow, 0 elsewhere) but one sample is not
evidence and it is recorded here as unconfirmed.

**A caution about scanning for these.** A naive "first object whose type field matches"
scan returns false positives — an Iron Broadsword lookup came back with `damage` of
-2042162171. `content.py` takes a consensus across candidates for exactly this reason, and
anything new must do the same.

## Tier 2: projectiles — a second template set, reachable the same way

`ContentSamples.ProjectilesByType` is findable without an anchor. `Main.projectile` is a
1001-element object array, so searching for a mono szarray of that length whose elements
share a vtable yields the `Projectile` vtable; scanning for that vtable then finds **1100
templates**, one per projectile id. `Projectile.type` is at `0x094`.

This is where "the Book of Skulls goes through blocks" actually lives — not on the item.
The item only says *which* projectile it fires; everything about how that projectile
behaves is on the projectile.

| Offset | Field | Evidence |
|---|---|---|
| `0x0D4` | `penetrate` | Wooden Arrow 1, Water Bolt 10, skull 3, some -1 (infinite) |
| `0x0D0`, `0x100` | tile/collision flags | the two bools that separate the skull from an arrow |

`0x0DC` carries values identical to `0x0D4` on every template — probably the "restore to"
copy the game resets `penetrate` from. Worth resolving before either is written to.

Which of `0x0D0` / `0x100` is `tileCollide` is **not** yet isolated: the Book of Skulls
skull (projectile 837) reads 1 and 0, a Wooden Arrow reads 1 and 1. One of them is the
pass-through-blocks flag; telling them apart needs a projectile whose behaviour is known
in the other direction.

**Editing here is global.** A projectile template is shared by every use of that
projectile id, which can include enemy attacks. This is unlike an item edit, which touches
one slot.

## Tier 3: accessories — there is no data

Hermes Boots (run speed) and Cloud in a Bottle (double jump) do entirely different things.
Their templates differ in **13 bytes out of 512**, and every one of them is identity
rather than behaviour: the item id, a name pointer, a colour, and two sprite dimensions.
Hermes Boots against Spectre Boots — same effect *plus flight* — differ in 15.

There is no run-speed field, no jump-height field, nothing to read and nothing to edit.
The effects are code: `Player.ApplyEquipFunctional` switches on `item.type` and writes to
player fields (`accRunSpeed` and friends). That method is ~11.6 KB, which is what a switch
over every accessory in the game looks like.

So an "accessory tuning" feature cannot be built by editing items. The routes that exist
are: write the *player* fields the switch writes (which the trainer already does for reach
and mining speed), or patch the switch itself for one accessory at a time.

## Consequences for a feature

- A weapon editor is straightforward and additive to what `set-item` already does.
- A projectile editor is a genuinely new capability and the most powerful of the three,
  but its edits are global and it needs the `0x0DC` and `tileCollide` questions answered
  first.
- "Accessory options" as a data-driven feature is not available. Framing it that way in
  any UI would promise something the game does not store.
