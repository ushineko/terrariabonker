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
| `0x150` | `crit` | two reforges of a Minishark: 0 → 3 for Sighted's +3, then → 5 for Demonic's +5 |

Two more, added while scoping the fishing cheat (spec 042) and verified the same way:

| Offset | Field | Evidence it is what it says |
|---|---|---|
| `0x058` | `fishingPole` | exactly 7 items carry it, all rods, matching the game's own powers |
| `0x05C` | `bait` | 13 items, no rods; Master Bait 50, Sluggy 25, Firefly 20, Snail 10 |

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
| `0x0DC` | `maxPenetrate` | equals `penetrate` on every template; the field exists in the class, so this is it rather than an unexplained copy |
| `0x100` | `tileCollide` | 290/293 against the game's own SetDefaults; see below |
| `0x0D0` | `friendly` | 584/592 the same way |
| `0x0B0` | `aiStyle` | 765/780 |
| `0x104` | `extraUpdates` | 172/173 — the per-frame update multiplier, i.e. projectile speed |
| `0x088` | `bobber` | true for 19 of 1100, including every bobber the fishing rods shoot |
| `0x08C` | `scale` | Space Gun 0.65, Crystal Storm 1.2, Starfury 0.8; and writing 2.5 on live skulls was visibly bigger in game |
| `0x098` | `alpha` | 255 on the skull and Ball of Fire, 0 on a Wooden Arrow |
| `0x034`, `0x038` | `width`, `height` | skull 26, arrow 10, Ball of Fire 4 |

`0x100` correlates perfectly with which items pass through blocks, taking each item's
`shoot` field to name its projectile. Note this only works for weapons that carry their own
projectile: guns and bows take theirs from the **ammo** (the Minishark's `shoot` is a
placeholder, while the Musket Ball's is the real one).

| passes through blocks (`0`) | collides (`1`) |
|---|---|
| Book of Skulls 837, Vilethorn 7, Nettle Burst 150, Starfury 9 | Demon Scythe 45, Water Bolt 27, Wooden Arrow 1, Magic Missile 16, Crystal Storm 94, Space Gun 20 |

**But writing it did not change behaviour.** 437 live skulls were set to `1` and still passed
through blocks. So either the field is read only at spawn, or the skull's AI moves it
directly and never consults collision — the correlation above is real either way, but it is
not established as *causal*. The control experiment (switching it off on a projectile that
normally collides) has not been run: see the note on measurement below.

### Offsets solved against the game's own code

The above was guesswork from correlations until the obvious was pointed out: read what the
game *declares*. `Projectile.SetDefaults` is a flat `if (type == N) { width = ...; aiStyle
= ...; }` chain, so parsing its IL yields 884 projectiles with their field values by name.
Matching those against the template bytes solves the offsets instead of inferring them:

| Field | Offset | Templates agreeing with the declared value |
|---|---|---|
| `penetrate` | `0x0D4` | 178/178 |
| `alpha` | `0x098` | 356/356 |
| `timeLeft` | `0x0B4` | 241/242 |
| `extraUpdates` | `0x104` | 172/173 |
| `tileCollide` | `0x100` | 290/293 |
| `aiStyle` | `0x0B0` | 765/780 |
| `friendly` | `0x0D0` | 584/592 |
| `hostile` | `0x0C8` | *not* solvable this way — settled later from runtime metadata |
| `width`, `height` | `0x034`, `0x038` | 720/845 |

The shortfalls are where a `DefaultTo*` helper or a shared tail overwrites what the case
declared, not disagreement about the offset.

**A field is only pinned when its declared values vary.** `SetDefaults` almost always
declares `hostile = 1`, so every offset that happens to hold 1 scores equally and three tie
— that one is not solved, and was briefly written down as `0x030` before the check was run.
It is `0x0C8`, and all three tied candidates were wrong: `0x030` is `direction`, `0x078` is
`active`, `0x0C4` is `spriteDirection`. A tie among wrong answers looks exactly like a tie
among plausible ones, which is the argument for not settling such fields by scoring at all.
`tileCollide` survives the same check honestly: it is declared `0` 266 times and `1` 27
times, and only `0x100` fits both. `friendly` likewise (575 ones, 17 zeros, only `0x0D0`).
`aiStyle` and `timeLeft` are decisive on their own, with 210 and 40 distinct values. `tileCollide` at `0x100` and `friendly` at
`0x0D0` close the question this file left open, and on far better evidence than the
behavioural correlation above.

`0x0B4` was briefly retracted as `timeLeft` because a live skull read a flat 0. **That
retraction was wrong** — 242 projectiles declare a `timeLeft` that matches `0x0B4` exactly.
One live reading was weaker evidence than the game's own source, and the 0 remains
unexplained rather than explanatory.

**Reading the IL first would have saved the whole afternoon.** The same lesson the fishing
recon recorded — two failed memory-diff sessions, then an afternoon of IL that settled it —
and it was not applied here until the maintainer asked why not.

### Editing a template does nothing

Tested directly on 2026-08-27: the skull's template was given `penetrate -1`,
`tileCollide 1` and `scale 2.5`, and the game was unaffected. `ContentSamples
.ProjectilesByType` is a lookup table built at load for queries; `Projectile.SetDefaults`
sets a new projectile's fields from code, and never consults it. The template was restored.

This is the opposite of items, where the trainer's editor works by copying template bytes
**into the live item object** — the template is where the bytes come from, not something
the game reads.

### Editing a live projectile works, and has to be enforced rather than triggered

Writes to a live `Projectile` stick: `scale`, `penetrate` and `0x100` all held for 500 ms+
with no sign of the game overwriting them, and a `scale` of 2.5 is visibly bigger in game
(most obvious in water, where the projectile is slow enough to look at).

**Detecting new projectiles by slot occupancy does not work.** A fast weapon reuses slots
between polls, so a slot never looks new and almost nothing gets patched: 60 seconds of
sustained fire produced three detections. Writing the desired value to every active
projectile on every pass is both simpler and correct — a full pass costs 2.7 ms.

### The open question, and how to answer it

> **Superseded.** Everything from here to the end of this tier was measured with a
> probe that read `Entity.wet` as `Projectile.active` and could therefore only see
> projectiles that were underwater. The A/B table below is void. See
> "The offset that was wrong for eight releases" at the end of this tier.

`tileCollide` is the right field at the right offset, the write persists, and
`HandleMovement` reads it — yet 437 skulls forced to `1` still passed through blocks. That
is unresolved. It is not the offset and not the write; the candidates are that the value is
consumed at spawn, or that `aiStyle 1` reaches the tile check by a path the flag does not
gate.

**First A/B result, and it does not say what either prediction expected.** With
`tileCollide` forced to 1 on even-numbered slots and odd slots left alone, over 90 seconds
of firing:

| group | in blocks | in open | % in blocks | mean speed |
|---|---|---|---|---|
| control (flag left at 0) | 0 | 3561 | 0.0% | 3.39 |
| patched (flag forced to 1) | 3672 | 8288 | 30.7% | 3.79 |

The patched skulls are **not stopping** — their mean speed is if anything slightly higher.
So "forcing the flag makes them collide" is not supported. But they also behave differently
from the control in two ways: they are sampled 3.4× as often (so they persist longer) and
they spend a third of that time inside terrain, which the control never does at all.

**This experiment is missing its control.** The split is by slot parity, and no run has been
done with the same split and *nothing written*, so an even/odd difference unrelated to the
edit has not been ruled out. Until that null run exists, the table above is an observation
and not a finding. Two attempts at it caught no projectiles.

`tools/projectile_probe.py` is how to do both, without anyone watching a clock:

    sudo python3 tools/projectile_probe.py --type 837 --ab --watch     # the null run
    sudo python3 tools/projectile_probe.py --type 837 --set tileCollide=1 --ab

    sudo python3 tools/projectile_probe.py --type 837 --set tileCollide=1 --ab

It enforces the value on every pass, leaves odd-numbered slots as a control, and reports
what fraction of each group is standing inside a solid tile according to the tile map. If
the patched skulls stay out of blocks and the control does not, the flag works live and the
editor idea is proven; if both sit inside blocks equally, it does not.

### A note on measuring this at all

Several conclusions in this file were nearly drawn from windows in which nothing was fired,
including one that was written down and retracted the same hour. A probe that says "fire
now" cannot be followed by someone who only sees its output afterwards. Four separate attempts to co-ordinate "fire now" with a probe window produced four windows
containing something other than what was asked for — twice nothing at all, twice only
summoned minions — and one of those silences was briefly read as a result about the game.
A seven-minute background run fared no better.

The fix is not a longer window. It is that the probe belongs to the player:
`tools/projectile_probe.py` is run by whoever is holding the mouse, which removes the
co-ordination entirely. The same lesson the fishing recon recorded about liveness controls,
arriving from a different direction.

**Most of that diagnosis was wrong, and the paragraph above is kept as written because the
error is the point.** The windows were not empty and the player was firing throughout. The
probe read `Entity.wet` as `Projectile.active`, so a projectile in flight — dry — was
filtered out before anything else ran. Every "nothing was fired" was the tool failing to
see, and it was reported to the player as a fact about their game twice, against their
explicit correction both times. A measurement that disagrees with the person watching the
screen is a claim about the instrument first.

### The offset that was wrong for eight releases

`Projectile.active` is at **`0x078`**. It was read at `0x03C` from the first projectile
work until v0.40.0, and `0x03C` is `Entity.wet`.

Nothing caught it, and the reason is worth recording. The only projectile this project
ever looked at was a fishing bobber — and **a bobber floats in water, so `wet` is true
exactly when a live bobber exists.** The wrong offset was indistinguishable from the right
one across the entire feature that used it: 617 tests, an auto-fishing session that landed
23 fish unattended, and every hour of play agreed with it. The first thing to disagree was
a projectile that flies through air.

It also means the collision A/B above measured only projectiles that were underwater, and
that the "skulls get bigger the moment they hit the water" observation — reported from the
game and filed as a coincidence — was the truest reading anyone took. The probe could only
*find*, and therefore only *patch*, wet projectiles. The size change appeared at the
waterline because that is where each skull became visible to the tool.

Expected fallout, predicted from the field layout: bobber detection should fail in **lava**
and honey, where `lavaWet` (`0x040`) and `honeyWet` (`0x03E`) are set and `wet` is not — a
bobber floating in lava would have read `wet = 0` and been invisible to every bobber check.

**Confirmed in game after the fix**: auto-fishing works in both **lava** (Golden Fishing
Rod, Magma Snail bait) and **honey**.

Worth recording as the one prediction in this whole investigation made from the code
*before* being observed, and held — twice. Every other model here was reasoned into place
and turned out wrong. The difference is that this one was derived from a layout the runtime
had already confirmed, rather than from inference about a layout nobody had checked.

### Ask the runtime instead of inferring

`tools/monofields.py` reads field offsets **by name** out of mono's own metadata.
`MonoClassField` on 32-bit is `{MonoType *type; const char *name; MonoClass *parent; int
offset;}`, so locating a name string and then a pointer to it puts the offset 8 bytes
further on. It deliberately does not walk `MonoVTable` or `MonoClass`, whose layouts shift
between mono builds; avoiding them is what should let this survive a game update.

    sudo python3 tools/monofields.py --verify        # every constant, against the runtime

`--verify` checks 42 offsets across `Entity`, `Projectile`, `Item` and `Player` and exits
non-zero on any disagreement. It is the answer to a problem this project kept re-meeting:
a test that plants a fixture through a constant and reads it back through the same constant
proves only that the constant equals itself. That is how `active` survived, and how a
mutation moving `crit` from `0x150` to `0x154` passed the whole suite in v0.39.0.

Two records it settled immediately:

| Field | Believed | Runtime |
|---|---|---|
| `Projectile.active` | `0x03C` (= `wet`) | **`0x078`** |
| `Projectile.hostile` | unresolved, briefly `0x030` | **`0x0C8`** (a bool) |
| `Item.armorPenetration` | predicted `0x154`, unwritten | `0x154` — confirmed |
| `Item.bonusTagDamage` | predicted `0x158`, unwritten | `0x158` — confirmed |
| `Item.reuseDelay` | unknown | **`0x164`** |

The two `Item` fields v0.39.0 declined to write were right all along. They remain
unwritten: verifying an offset removes the reason to refuse, but writing new fields into
saved items is a behaviour change and belongs to a spec, not to a recon note.

Field **widths** come from the same table and matter as much as offsets. `hostile` is one
byte with `reflected` packed at `0x0C9`, so the four-byte writes the probe used would have
clobbered a neighbour.

### The A/B, re-run with a probe that can see

Forcing `tileCollide = 0` on even-numbered slots, control on odd, one 45-second run,
firing at a wall. Two results, and the first explains the whole preceding investigation.

**`BookOfSkullsSkull` (837) already has `tileCollide = 0`.** All 89 newly-seen skulls read
0 *before any write*. The skulls ignore terrain by design, so forcing the flag either way
is a null operation — control and patched are indistinguishable (67.0% vs 64.6% of samples
inside solid tiles, 6.533 vs 6.558 displacement per sample). Every earlier session spent
its effort enforcing a value the game had already set, on the one projectile in the game
least able to demonstrate the effect.

**`BoneGloveProj` (532) proves the edit works.** It defaults to `tileCollide = 1` (20 of
20), and forcing 0 changed its behaviour decisively:

| type 532 | in blocks | moved/sample |
|---|---|---|
| control (untouched) | 10.6% | 8.10 |
| patched (`tileCollide = 0`) | **57.5%** | **27.97** |

5.4× the time inside terrain and 3.4× the displacement, against a slot-parity control in
the same run and the same wall. A projectile that passes through walls instead of stopping
at them. **Live projectile field editing works**, and this is the first measurement in this
file that says so on evidence rather than impression.

The probe now reports every field's value *before* it writes it, and flags the case where
the value was already what we were about to enforce. That check would have ended the
original investigation in one run.

### `tileCollide` is not the only thing terrain does to a projectile

Reported from play: the Book of Skulls goes through *some* tiles, not all — despite
`tileCollide = 0`. The answer is a hardcoded special case in `AI_001`, and it is neither
`tileCollide` nor `penetrate`:

```csharp
if (type == 837) {                      // BookOfSkullsSkull
    int drain = 33;
    if (timeLeft > drain && WorldGen.SolidTile(Center.ToTileCoordinates()))
        timeLeft -= drain;
}
```

**Every tick its centre is inside a solid tile, the skull loses 33 ticks of life.** It
spawns with `timeLeft = 480`, so terrain does not stop it — terrain ages it ~33× faster.
The drain stops once `timeLeft <= 33`, so a skull gets about 14 draining ticks plus the
remaining 18: ~32 ticks inside rock, and at the measured 4.50 px/tick that is roughly
**9 tiles** of continuous stone before it expires.

Two things follow.

**`penetrate` is unrelated.** It is the number of NPCs a projectile passes through (3 for
skulls) and never decrements on tiles. The two are easy to conflate because both read as
"how much can it go through", and they are separate systems.

**The lever for this weapon is `timeLeft`, not `tileCollide`.** Enforcing `timeLeft` every
poll cancels the drain outright, which is exactly what spec 047's field list already
allows. A per-projectile behaviour that looked like a collision setting turned out to be a
lifetime setting, and only the game's own code said so.

The general warning for the editor: a projectile's observable behaviour is not necessarily
governed by the field that appears to name it. `AI_001` alone writes `tileCollide` in 13
places and reads it in none, and special-cases type 837 five separate times.

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


## What a modifier writes (spec 046)

A modifier is not a display value. `Item.Prefix` multiplies the item's own fields and
stores the results, so the prefix byte at `+0x15C` is only the name the tooltip prints —
which is why assigning it alone produced a Godly weapon with nothing Godly about it.

Confirmed by reforging a **Minishark** to **Sighted** at the Goblin Tinkerer on
1.4.5.8+24893155 and diffing the item's 512 bytes before and after. Sighted is
`damage ×1.1, crit +3`:

| Offset | Field | Before → after |
|---|---|---|
| `0x0AC` | `damage` | 12 → 13 (`round(12 × 1.1)`) |
| `0x150` | `crit` | 0 → **3** — the +3 itself, which is what pins this offset |
| `0x0F8` | `rare` | 2 → 3 — a modifier raises the rarity tier |
| `0x124` | `value` | 350000 → 475844 |
| `0x15C` | `prefix` | 0 → 16 |

Nothing was written to find this: the game was allowed to do it and the bytes were read
afterwards, which is the only way to pin an offset without risking an item on a guess.

**`armorPenetration` (`0x154`?) and `bonusTagDamage` (`0x158`?)** are the next two ints and
are predicted by the same declaration order that placed `crit` correctly — but Sighted
grants neither, so neither was observed changing. They remain unwritten. A reforge that
rolls a modifier granting one would settle them the same way.

**When the game resets an item to base, and when it does not.** Two reforges of the same
Minishark, which had been edited to damage 12 and use time 4 against a template base of 6
and 8:

| Reforge | Item had | damage | useTime | crit |
|---|---|---|---|---|
| → Sighted (`×1.1`, `+3`) | no modifier | 12 → **13** = `12 × 1.1` | 4 → 4 | 0 → 3 |
| → Demonic (`×1.15`, `+5`) | Sighted | 13 → **7** = `6 × 1.15` | 4 → **8** | 3 → **5** |

The first multiplied the item's *current* stats; the second reset it to the template base
first — wiping the edited use time back to 8 — and applied the modifier to that. The
difference is whether there was an existing modifier to strip: a prefix-less item is taken
to already be its base, so nothing is reset, while replacing a modifier goes through
defaults.

So an item a trainer has edited will keep those edits through its first reforge and lose
them at the second. That also explains a **Molten Hamaxe** seen earlier carrying its
Legendary knockback bonus while its damage and use time read as base — a partially-bonused
item is what an edit and a reforge leave behind between them, not a bug.

The trainer recomputes from the template base every time, which matches the game's
behaviour for the case that recurs (replacing a modifier) and differs for the first one on
an already-edited item, where the game would have kept the edit.
