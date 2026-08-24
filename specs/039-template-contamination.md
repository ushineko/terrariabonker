# Spec 039: Pick an item's template by consensus, not by where it sits

**Status**: COMPLETE
**Implementation Date**: 2026-08-23

> **Note**: No issue tracker ticket (personal utility).

## Context

`content.find_item_templates` is meant to return each item's **pristine** ContentSamples
stats. It can return a live, modified item instead — and it did, for exactly the items the
maintainer had edited:

| item | scan returned | actual template |
| --- | --- | --- |
| Boomstick | damage 31, use time 12 | damage 14, use time 40 |
| Book of Skulls | damage 45, use time 12 | damage 29, use time 32 |
| Muramasa | damage 26 | damage 24 (a prefixed copy won) |

This is not cosmetic. It already caused real damage: spec 038 used the templates as the
baseline for deciding which saved fields were "just the defaults", so an edit was compared
against itself and pruned. Eight profile entries were destroyed before the comparison was
withdrawn in v0.30.1. The Compendium still reads from the same scan, so an item you have
edited is displayed with **your** values as its base stats.

### Why the current rule fails

Templates are found by shape: cluster Item objects by address, and from each run keep the
types appearing exactly once, biggest run first. Two things defeat it.

- A type that appears **more than once inside the big run** is excluded from it, and a
  smaller run — heap objects, chest contents, the player's own inventory — supplies the
  value instead.
- Nothing distinguishes a pristine copy from a modified one, so whichever run wins decides,
  and an edited or prefixed item is as acceptable as the real template.

Two structural signatures were tried and rejected against the live game: the templates are
**not** evenly spaced in memory (zero chains of >50 objects at a constant stride), and they
are not confined to a single address run.

## Requirements

1. An item's template stats are the ones a **pristine** copy has, whatever else is in memory.
2. Editing an item must not change what the Compendium reports as that item's base stats.
3. Nothing that reads templates may be able to mistake the player's own item for the
   template, since those are precisely the ones this program modifies.

### Technical

4. **Exclude the player's own items by address.** `Inventory._item_addr` gives them exactly;
   they are the objects this tool edits, so they are the likeliest contaminant and the
   cheapest to remove.
5. **Prefer unprefixed copies.** A modifier changes damage and use time, so a prefixed copy
   is not a template. Where a type has any copy with prefix 0, only those are considered.
6. **Then take the consensus**: the most common stat tuple among the remaining copies. A
   pristine template agrees with every other pristine copy; an edited one stands alone.
7. The clustering helpers stay for NPCs, whose live objects are told apart a different way
   (their stats are scaled by world difficulty), and whose live set is likewise excluded by
   address via `Main.npc`.

## Risks & Assumptions

- **Consensus can be wrong where every copy is modified.** A chest holding forty identically
  edited swords and no pristine copy would carry the vote. Accepted: the alternative is
  believing an arbitrary copy, which is what happens today.
- **A single-copy type has no consensus to take.** If the only object of a type is the
  player's edited one, excluding it leaves nothing and the type is absent from the catalog
  rather than wrong. Absent is the honest answer.
- **Prefix preference assumes prefix 0 means unmodified**, which is true of modifiers but
  not of this program's own stat edits. Address exclusion covers the tool's own edits; the
  two rules are complementary.
- **Rollback.** `git revert`; the template cache regenerates from the running game.

## Acceptance Criteria

- [x] An item the maintainer has edited reports its **vanilla** base stats, verified against
      known values (Boomstick 14/40, Book of Skulls 29/32)
- [x] A prefixed copy does not win over an unprefixed one (Muramasa 24, not 26)
- [x] The player's own inventory objects are excluded from template selection
- [x] Coverage does not regress: at least as many types are resolved as before
- [x] Editing an item and re-scanning leaves its reported base stats unchanged
- [x] NPC templates likewise exclude the live `Main.npc` objects
- [x] All tests pass headless; flake8 clean on changed files; security review recorded
- [x] README updated; version bumped to 0.31.0 (maintainer confirmed)

## Executive Summary

`find_item_templates` could return a live, modified item as an item's pristine template. It
reported the maintainer's edited Boomstick (damage 31, use time 12) as that item's base
stats instead of 14/40, and spec 038 then used the templates as the baseline for deciding
which saved edits were redundant — comparing an edit against itself and destroying eight of
them before the comparison was withdrawn.

The old rule chose by position: cluster objects by address, keep the types appearing once
per run, biggest run first. Nothing in that distinguishes a pristine copy from a modified
one. Two structural alternatives were tried against the live game and rejected — the
templates are not evenly spaced (zero chains of >50 objects at a constant stride) and are
not confined to one run.

What does distinguish them is agreement. A pristine template matches every other pristine
copy of its type; a modified one stands alone. So: exclude the player's own items by
address, prefer unprefixed copies, then take the most common stat tuple.

The contaminants turned out not to be the player's items at all. A **Superior Muramasa in a
world chest** was being reported as the base Muramasa — Terraria generates chest weapons
with random modifiers, and every chest in the world is in memory. Address exclusion handles
what this program edits; the prefix rule handles what the world generates; consensus handles
the rest.

Reviewers: `content._consensus` and the two filters ahead of it.

## Testing

356 headless tests, flake8 clean on changed files, `pip-audit 2.10.0` clean.

- `tests/test_content.py`: copies that agree are taken as the template; **one modified copy
  does not outvote the pristine ones**; a prefixed copy never wins over an unprefixed one;
  the prefix is not reported as a stat; and excluded objects are not considered.
- The two old tests that encoded the positional rule ("a repeated-type run is not a table",
  "the larger table wins") were replaced rather than adjusted, because the rule they
  described is the one that caused the bug.
- **Both new rules are mutation-checked**, and the exclusion test had to be rewritten to
  earn it: the first version passed even with exclusion disabled, because its two candidates
  tied and the tie happened to resolve the same way. The excluded copies are now the
  majority, so ignoring them changes the answer.

Live, against the running game, checked against values known independently:

| item | before | after |
| --- | --- | --- |
| Boomstick | 31 / 12 (the maintainer's edit) | **14 / 40** |
| Book of Skulls | 45 / 12 (the maintainer's edit) | **29 / 32** |
| Muramasa | 26 (a Superior copy in a chest) | **24** |
| Wooden Boomerang | 11 (a Godly copy) | **10** |
| Ice Blade | 20 / 49 (a Legendary copy) | **17 / 55** |
| Slime Whip | 15 / 30 | 15 / 30 (already correct) |

Coverage improved rather than regressed: 6,157 types before, **6,161** after, in 3.7 s.
