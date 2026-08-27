# Spec 046: A modifier assigned in the editor gives its effects

**Status**: INCOMPLETE — implemented and tested headless; awaiting in-game confirmation of
the reported case, and a decision on repairing items that already carry a cosmetic
modifier. `crit`/`armorPenetration`/`bonusTagDamage` remain unverified and unapplied.

> **Note**: This work has no associated issue tracker ticket (personal utility).

Reported from the game: a Spider Staff set to **Godly** in the inventory editor shows the
name and gets none of the bonuses. It is not specific to that item — no prefix the editor
assigns does anything at all.

## Context

**The prefix byte is a label, not an effect.** `Item.Prefix` multiplies the item's own
fields and stores the results; the byte at `+0x15C` is what the tooltip reads to print the
name. The trainer writes only the byte, so every modifier it assigns is cosmetic.

From the game's IL (`Item::Prefix`, reading its use of
`TryGetPrefixStatMultipliersForItem`'s out-params):

| Field | Applied as |
|---|---|
| `damage` | × multiplier, rounded |
| `knockBack` | × |
| `useAnimation`, `useTime`, `reuseDelay` | × (one multiplier for all three) |
| `scale` | × |
| `shootSpeed` | × |
| `mana` | × |
| `crit`, `bonusTagDamage`, `armorPenetration` | **+** (added, not multiplied) |

**Why it looked item-specific.** It is not — but which fields a modifier touches decides
how invisible it is. Godly (59) is `damage ×1.15, knockBack ×1.15, crit +5`, and a summon
staff's damage is the only one of those a player would notice; a size-only modifier like
Large is entirely inert. There is no item for which this currently works.

**The multiplier table is already extracted**, from the IL rather than transcribed: 82
prefixes, spot-checked against the wiki (Large `scale ×1.12`; Dangerous `dmg ×1.05,
crit +2, scale ×1.05`; Legendary and Unreal exact). Transcribing 82 entries by hand is how
a wrong number gets written down as fact, so the extraction is kept as a tool.

## Field offsets

Four of the six multiplied fields are already derived and verified in
`docs/item-fields.md`; the additive three are not.

| Field | Offset | Status |
|---|---|---|
| `damage` | `0x0AC` | verified (`inventory.ITEM_DAMAGE`, in use) |
| `useAnimation` / `useTime` | `0x080` / `0x084` | verified (in use) |
| `knockBack` | `0x0B0` | verified — 5.5 Copper Broadsword, 6.5 Meowmere, both matching the wiki |
| `scale` | `0x0CC` | verified (`docs/item-fields.md`) |
| `shootSpeed` | `0x100` | verified (`docs/item-fields.md`) |
| `mana` | `0x11C` | verified (`docs/item-fields.md`) |
| `crit` | `0x150`? | **unverified** — declaration order only |
| `armorPenetration` | `0x154`? | **unverified** |
| `bonusTagDamage` | `0x158`? | **unverified** |
| `reuseDelay` | ? | **unknown** — sits after six bools, so not simply `+4` |

The three unverified ones come from `Item`'s field declaration order, which predicted four
already-trusted offsets exactly (`healLife 0x0B4`, `healMana 0x0B8`, `consumable 0x0BD`,
`autoReuse 0x0BE`) — good evidence for the method, not for these three specifically. Every
template sampled reads 0 at all three, so the data cannot tell them apart from padding.

**How to settle them** (in order of cost): write a candidate and read the tooltip in-game —
a Godly weapon should say "+5% critical strike chance" — which the maintainer can do in a
minute; or reforge an item at the Goblin Tinkerer and diff its bytes before and after,
which is the definitive version and needs no guess at all.

## Design

**Recompute from the item's base stats, never from its current ones.** A modifier is a
multiplier on the pristine item, so applying Godly twice must not compound to ×1.32. The
ContentSamples template is the base, and `Service._template_block` already fetches it.

**Ordering: template → prefix → explicit field edits.** The dialog's own numbers win over
the modifier, so a user who sets both damage and a prefix gets the damage they typed. This
is the one visible behaviour change: setting a modifier on an item whose stats were tuned
earlier resets those stats to base-times-modifier, because there is no way to tell a
previous manual edit from a previous modifier.

**`crit` and friends are outside the template copy span** (`ITEM_COPY_LO..HI` is
`0x1C..0x140`), which is also why the prefix byte survives a template copy. They must be
read from the template object directly rather than from the copied block.

## Acceptance criteria

- [ ] Assigning a modifier applies every field it multiplies, computed from the item's
      template base — verified in-game on the reported case: a Godly Spider Staff shows
      raised damage and knockback in its tooltip. *(Second half fixed after a follow-up
      report — see "only works the first time" below. Still needs an in-game look.)*
- [x] Assigning the same modifier twice leaves identical stats (no compounding), and
      switching modifier A → B gives the same result as applying B to a pristine item.
      *(Every scaled field is written from base, not only the ones the new modifier
      touches — the first implementation left the old modifier's damage behind, which is
      what the switching test caught.)*
- [x] Clearing the modifier (prefix 0) returns the item to its base stats.
- [x] The multiplier table is generated from the game rather than hand-written, with the
      generator kept in `tools/` and the provenance recorded.
      *(`tools/extract_prefix_stats.py` → `data/prefix_stats.json`, 78 modifiers.)*
- [x] `crit`, `armorPenetration` and `bonusTagDamage` are either verified and applied, or
      **not applied and documented as not applied**. *(Not applied. `apply_prefix_stats`
      returns them in `skipped`, and a test asserts Godly reports `crit` as skipped rather
      than dropping it quietly.)*
- [x] Explicit field edits in the same dialog submission win over the modifier's values.
- [x] Headless tests: the multipliers land on the right fields, applying twice is
      idempotent, and prefix 0 restores base. *(10 tests; five mutations caught, including
      the original bug and the compounding one.)*
- [ ] `docs/item-fields.md` gains whatever offsets this derives, with the evidence for each.

## Found on the first dry run: the maintainer's own Spider Staff

Before writing anything, the fix was run in report-only mode against the reported item:

```
slot 7: Spider Staff [Godly]
    damage     base 26   now 52   -> would become 29.9
    knockback  base 3.0  now 3.0  -> would become 3.45
```

**`now 52` is a manual edit**, not a modifier. So repairing this item would *lower* its
damage from 52 to 30, because the modifier is computed from the pristine 26. That is the
"manual stat edits are lost" risk in Risks below, arriving immediately and on the very item
that prompted the report.

It argues against repairing existing items automatically. Re-assigning a modifier is the
repair, and it should stay something the player chooses per item — with the dialog's own
damage field (which wins over the modifier) as the way to keep a tuned number.

Also seen and **not explained**: a Molten Hamaxe reads `knockback 8.05` (its Legendary
bonus, applied) while its damage and use time read as base. A partially-bonused item does
not fit either "editor set the byte only" or "reforged in the game", and guessing at it
here would be inventing history. Worth a look if it recurs.

## The follow-up report: "only works the first time"

The first fix was right and still looked broken, because the bug had a second half in a
place the spec had not looked: **the dialog submitted every field on every OK**, populated
from the item's current stats.

So the item's own damage came back as an *explicit edit*, which by this spec's own ordering
lands after the modifier and overwrites what it computed. Only the fields the dialog does
not carry — knockback, scale, shootSpeed, mana — ever survived, which is exactly the shape
of "it worked, sort of, once": the visible bonuses that stuck were the ones nothing
overwrote.

The dialog now sends **only the fields the user changed**, and a submission that changed
nothing writes nothing at all. That was worth fixing on its own: pressing OK on an untouched
dialog rewrote every field of the item, which is a write to the save for no reason.

The lesson for the ordering rule in Design: "explicit field edits win over the modifier"
is correct, but only if *explicit* means the user typed it. An echo is not an edit.

## Risks & Assumptions

- **This writes to the save.** Item stats persist, so a wrong offset corrupts an item
  permanently rather than failing a test. The three unverified offsets are the hazard, and
  the spec's answer is not to write them until they are verified.
- **Manual stat edits are lost when a modifier is applied** (see Design). Reasonable, and
  the alternative is worse — but it is a behaviour change and belongs in the dialog's
  wording, not just here.
- **`reuseDelay` is unknown and mostly zero**, so leaving it unapplied is very nearly
  harmless; "very nearly" is doing work in that sentence and it should be stated in the
  docs rather than assumed.
- **The prefix a modifier rolls is class-filtered** and `prefixes.py` already knows the
  pools; this spec does not change which modifiers may be assigned, only what happens when
  one is.
- **Rollback**: `git revert`. Items already given a cosmetic modifier keep it; re-assigning
  the same modifier after the fix will apply the effects, which is the intended repair.

## Alternatives considered

- **Call `Item.Prefix` in the game** rather than replicating it. Exact by construction —
  it would apply every field including the three unverified ones, and stay correct across
  updates. Rejected for now: it needs a managed call with an `Item*` argument, and the
  project has exactly one of those (teleport) which was its most delicate injection. Worth
  revisiting if the field list grows.
- **Multiply the item's current values** instead of recomputing from base. Preserves manual
  edits, but compounds on every re-apply and drifts with rounding. Rejected.
- **Hide modifiers from the editor until they work.** Rejected: they are useful for the
  name alone (the compendium shows modified names), and removing a working feature to fix a
  broken one is a bad trade.
