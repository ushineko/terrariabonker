# Spec 032: Make the vanity accessory slots functional

**Status**: INCOMPLETE

> **Note**: No issue tracker ticket (personal utility).

## Context

The request was "more accessories", in either of the two shapes tModLoader mods use: add
accessory slots (needs new UI), or let accessories work from inventory/bank slots (no UI).
Static recon on the shipped 1.4.5.7 assembly found a third option that needs no UI *and* no
new slots — Terraria already has **seven more accessory slots on screen**, and already
half-runs them.

### What the game does today

`Player.UpdateEquips` (read from `Terraria.exe` metadata, see "Recon" below) contains four
loops. Two matter here:

```
loop 2:  for (i = 0; i < 10; i++)                    // armour + functional accessories
             item = GetEffectiveArmor(i)
             if (!item.IsAir && IsItemSlotUnlockedAndUsable(i)
                 && (!item.expertOnly || Main.expertMode)
                 && UpdateEquips_CanItemGrantBenefits(i, item)) {
                 if (item.accessory) GrantPrefixBenefits(item)   // Menacing, Warding, ...
                 GrantArmorBenefits(item)                        // per-item effects
             }

loop 4:  for (k = 3; k < 10; k++)                    // the accessory effects themselves
             if (IsItemSlotUnlockedAndUsable(k))
                 ApplyEquipFunctional(k, GetEffectiveArmor(k))
```

`Player.UpdateVisibleAccessories` runs its own pair of loops — `3..9` and **`13..19`** — and
for both calls `ApplyEquipVanity`, whose first act is `RefreshInfoAccsFromItemType`, followed
by the wing *visual*, werewolf/drone flags and shaders.

That asymmetry is exactly what the maintainer observed in-game before any patch existed:
a Depth Meter or Tungsten Watch placed in a vanity slot **works**, while Hermes Boots or
wings placed there do nothing. Info accessories come from `ApplyEquipVanity`; everything else
needs `ApplyEquipFunctional`, which vanilla never calls for `13..19`.

### Why this is cheap to change

- `armor = new Item[20]`: `0-2` armour, `3-9` accessories, `10-12` vanity armour,
  **`13-19` vanity accessories**. Indices `10..19` are already valid — no array to grow.
- `IsItemSlotUnlockedAndUsable` already knows the vanity mirror: it gates `8`/`18` on the
  Demon Heart (`extraAccessory` + expert) and `9`/`19` on master mode, and returns true
  otherwise.
- `UpdateEquips_CanItemGrantBenefits` switches on cases `0..9` only, and its **default
  returns true**, so slots `10..19` pass the gate unchanged.
- `ApplyEquipFunctional` already refuses `expertOnly` items itself, so widening the loop
  cannot smuggle those in.
- The vanity slots already accept only accessories, are already drawn, and are already saved
  by the game. Nothing about the UI or the save format changes.

### The one hazard

`ApplyEquipFunctional(int slot, Item item)` uses `slot` for exactly one purpose —
`hideVisibleAccessory[slot]`, at seven sites — and that array is `new bool[10]`. Passing a
slot ≥ 10 is an `IndexOutOfRangeException` on every frame. The slot must therefore be clamped
below 10 before the call; since its only use is the "hide this accessory's visual" lookup,
the clamp costs nothing but which checkbox governs the vanity item's visual.

## Requirements

1. A toggleable cheat that makes items in the seven **vanity accessory slots** grant their
   full effects, exactly as if they were in a functional slot.
2. Prefix bonuses (Menacing, Warding, …) on those items apply too, so an accessory behaves
   the same in either column.
3. The Demon Heart and master-mode gating on the mirrored slots (`18`, `19`) is left as
   vanilla has it; this cheat does not unlock slots the player has not earned.
4. Disabling restores the original bytes; a game restart clears it, like every other code
   patch.
5. It persists and auto-restores through the existing profile machinery, with no special
   cases.

### Technical

6. **Two patch sites, one toggle.**
   - loop 4's bound `k < 10` → `k < 20`, so `ApplyEquipFunctional` runs for `13..19`
     (and `10..12`; see Risks).
   - loop 2's bound `i < 10` → `i < 20`, so `GrantPrefixBenefits` / `GrantArmorBenefits`
     also see them. This site needs no cave: neither callee takes a slot.
7. **Slot clamp cave.** At loop 4's call site, a stub passes `slot - 10` when `slot >= 10`,
   keeping `hideVisibleAccessory[slot]` in bounds. This is the same shape as the existing
   `spawn_rate` / `teleport` injections, and reuses `Patcher._find_cave`.
8. **`Cheat` gains multiple edits.** Today a `Cheat` is one anchor plus one patch. This cheat
   needs two byte edits under one toggle, so `Cheat` grows a tuple of edits and the existing
   single-edit entries are expressed through the same field. `is_enabled` is true when the
   edits are applied; `disable` reverts each.
9. **Anchors** for both bounds are derived from the JIT'd body of `UpdateEquips`, anchored on
   its distinctive immediates — `4743` (Football), `4131` (Void Bag), the `58`/`57` inventory
   bounds — and recorded in the ledger from spec 030 with the build key they were confirmed
   on. Multi-site resolution from spec 030 covers `UpdateEquips` being JIT'd more than once,
   as `ResetEffects` already is.
10. **Recon tooling is committed.** The IL dumper written for this investigation
    (`System.Reflection.Metadata`, no Cheat Engine or Wine needed) lands as `tools/ilrecon/`
    so these findings are reproducible after a game update, alongside the `ce/` scripts.
    Findings are written up in `ce/ACCESSORY_FINDINGS.md`.

## Risks & Assumptions

- **Vanity armour (`10..12`) rides along.** Widening the bound to 20 also feeds vanity armour
  through `ApplyEquipFunctional` and `GrantArmorBenefits`. Armour types are not in the
  accessory type switch so it is expected to be inert, but "expected" is not "verified" — it
  must be tested, and the fallback is a cave that skips `10..12`.
- **The clamp changes whose "hide visual" checkbox applies.** A vanity accessory in slot 13
  reads `hideVisibleAccessory[3]`. Cosmetic, and only when the player hides accessories.
- **Duplicate accessories stack.** The same accessory in a functional and a vanity slot
  applies twice. Vanilla's UI prevents duplicates within the functional column
  (`HasIncompatibleAccessory`); this cheat sidesteps that across columns. Treated as intended
  for a trainer, but it is a behaviour change worth stating.
- **Wings.** With wings in both columns, `ApplyEquipFunctional` sets wing stats twice and the
  last write wins; the vanity wing's *visual* already applied before this cheat.
- **An out-of-range slot crashes the game every frame.** This is the failure mode to guard:
  the clamp is load-bearing, and the acceptance criteria require it to be exercised on a
  vanity slot that is actually occupied.
- **Rollback.** `git revert`, and the cheat is off by default; disabling restores the
  original bytes. No save-format change, so a world touched with it on is unaffected once it
  is off.
- **Out of scope.** Accessories taking effect from *inventory or bank* slots (the second
  shape from the original request) is a bigger change: `GetEffectiveArmor` indexes
  `armor[20]`, so it needs the item source redirected in the cave, plus a decision about
  per-frame cost over 59 inventory slots. It stays a follow-up.

## Acceptance Criteria

- [ ] A `vanity_accs` cheat appears on the Trainer tab, off by default, with the ReGrind-style
      note explaining what it does
- [ ] Enabling it makes an accessory in a vanity slot grant its full effect in-game —
      verified with a movement accessory (Hermes Boots) and wings, not only an info accessory
- [ ] A prefix bonus on a vanity-slot accessory applies (Menacing damage, Warding defense)
- [ ] The Demon Heart / master-mode slots (`18`, `19`) stay gated exactly as vanilla gates
      `8`/`9`
- [ ] Disabling restores the original bytes at every patched site and every JIT copy, and the
      effects stop in-game
- [ ] The clamp is exercised with all seven vanity slots occupied, with no exception spam and
      no crash over several minutes of play
- [ ] Vanity armour in `10..12` is confirmed inert (or the cave skips it)
- [ ] `Cheat` supports multiple edits under one toggle; existing cheats keep their behaviour
      (headless tests over the synthetic image)
- [ ] Anchors carry the build key they were confirmed on, and resolve correctly when
      `UpdateEquips` is JIT'd more than once
- [ ] The cheat persists and auto-restores like the others
- [ ] `tools/ilrecon/` and `ce/ACCESSORY_FINDINGS.md` committed so the derivation is
      reproducible
- [ ] All tests pass headless; flake8 clean on changed files; security review recorded
- [ ] README updated; version bump confirmed by the maintainer

## Recon (already done, 2026-08-23)

Static, against the installed `Terraria.exe` (build 24893155) with a purpose-built IL dumper
— no Cheat Engine, no Wine, no running game:

| finding | evidence |
| --- | --- |
| `armor` is 20 items | `Player::.ctor` `ldc.i4.s 20; newarr Terraria.Item` |
| `hideVisibleAccessory` is 10 bools | `Player::.ctor` `ldc.i4.s 10; newarr System.Boolean` |
| slot is used only for the hide lookup | 7 × `ldfld hideVisibleAccessory; ldarg.1; ldelem.u1` in `ApplyEquipFunctional` |
| benefit gate defaults to true | `UpdateEquips_CanItemGrantBenefits` switch covers `0..9`; default is `ldc.i4.1; ret` |
| vanity mirror is already understood by the game | `IsItemSlotUnlockedAndUsable` special-cases `8`/`18` and `9`/`19` |
| info accessories come from vanity | `ApplyEquipVanity` → `RefreshInfoAccsFromItemType`, called from `UpdateVisibleAccessories`' `13..19` loop |
| inventory already feeds info/mechanical accs | `UpdateEquips` loop 1 over `inventory[0..57]` |

PASS 2 (the JIT'd code) is also done — located by scanning for `UpdateEquips`' distinctive
immediates and disassembled with `objdump -b binary -m i386`, again with nothing installed:

- The slot argument is already in **`eax`** when it is stored to `[esp+0x4]`, so the clamp
  needs no `ebp` displacement and does not depend on the JIT's frame layout.
- The loop-4 bound sits nine bytes past the call, so **one anchor covers both edits** there.
- Both anchors verified to resolve **uniquely** against the live process; the loop-2 bound
  needed the `item.accessory` (`+0x7D`) prologue to be specific, since the bare
  increment/compare tail matched unrelated code.

Full derivation, anchors and patch bytes: `ce/ACCESSORY_FINDINGS.md`.

## Executive Summary

_Populate before opening the PR._

## Testing

_Populate during implementation._
