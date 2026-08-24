# Spec 038: Restore item edits by what the item is, not where it sat

**Status**: COMPLETE
**Implementation Date**: 2026-08-23

> **Note**: No issue tracker ticket (personal utility).

## Context

Auto-restore reported this on a fresh game:

```
[auto-restore] 7 saved item edit(s) not re-applied — slot(s) 31, 32, 33, 34, 35, 36, 46
no longer hold the item they were saved for (Tungsten Watch, Shield of Cthulhu, Fledgling
Wings, Shiny Red Balloon, Depth Meter, Hermes Boots, Blade Staff); left alone rather than
overwritten
```

Every one of those is noise, and the message hides two separate faults.

### The profile records defaults as though they were edits

The edit dialog is pre-filled with the item's current values and submits all of them, so
`cmd_set_item` records every field rather than the ones that changed. A saved accessory
looks like this:

```json
{"type": 708, "damage": -1, "auto_reuse": 0, "use_time": 100, "use_anim": 100,
 "pick": 0, "tile_boost": 0, "defense": 0, "prefix": 65}
```

`damage: -1`, `use_time: 100`, `pick: 0` are the Tungsten Watch's own defaults. The only
thing the user changed is the prefix.

### Most of what it records does not need restoring at all

Auto-restore exists because Terraria saves an item as **type + stack + prefix** and
regenerates everything else from the type on load. `cmd_set_item`'s own comment says so —
and then saves type, stack and prefix anyway. Those three survive a save/load on their own;
re-applying them achieves nothing.

So of the six accessories in that warning, every "edit" was a prefix, which persisted
perfectly well without help. They were only ever listed because the profile was storing
things it had no reason to store.

### Slot is the wrong key

The remaining case is real. An edited weapon that the player moved to a different slot is
not restored, because the profile is keyed by slot and the check is "does this slot still
hold that type". Moving an item you have edited silently loses the edit — and moving
accessories into the equipment column, which is exactly what spec 032/033 encourage, is
what triggered this whole report.

## Requirements

1. Record only what actually needs restoring: the fields Terraria **regenerates** from the
   item type — damage, auto-reuse, use time, use animation, pickaxe power, tile boost,
   defense. Not type, stack or prefix, which the game persists itself.
2. Record only fields whose value **differs from the item's own default**, so an item the
   user only renamed or re-prefixed records nothing and is not mentioned again.
3. An edit with nothing left to record is **removed from the profile**, not stored empty.
4. Restore finds the item by **what it is**, not by where it was: the saved edit applies to
   the item of that type wherever it now sits.
5. When the type is not in the inventory at all, say so once, quietly — it is an ordinary
   thing to happen and not a failure.
6. Existing profiles are **migrated**, not invalidated: the fields that never needed saving
   are dropped on load, and entries left with nothing are discarded.

### Technical

7. **Defaults come from the ContentSamples template**, which the project already reads for
   the compendium (`content.find_item_templates`, cached per build). Comparing an edit
   against the template is what distinguishes "the user set damage to 45" from "this item's
   damage is 45".
8. **The profile becomes keyed by item type**, since that is the identity being matched.
   Empty-slot markers stay keyed by slot: clearing slot 12 is a statement about the slot,
   not about an item.
9. **Several copies of an edited type**: apply the edit to every copy. A trainer edit is a
   statement about the item, and leaving one of two identical swords unedited would be
   stranger than editing both.
10. The restore report distinguishes *applied*, *not present* and *skipped*, so the log line
    stops implying that an ordinary absence is a problem.

## Risks & Assumptions

- **Type-keyed edits lose per-copy distinction.** Two Zeniths cannot have different saved
  damage. Accepted: nothing in the UI offers that today, and slot-keying already lost the
  edit as soon as the item moved, which is worse.
- **The template cache is per build.** If it cannot be read the comparison has no baseline;
  in that case record the field rather than drop it, so a missing cache degrades to today's
  behaviour rather than to silently forgetting edits.
- **Migration is one-way.** A profile rewritten by this version loses the stack/prefix
  fields it used to carry. They were never restorable, so nothing is lost in practice, but
  an older build reading the file afterwards would see fewer keys.
- **Stack is deliberately not restored.** Terraria saves it. A user who wants 999 of
  something gets it once, and the game keeps it.
- **Rollback.** `git revert`; the profile is disposable and regenerates as edits are made.

## Acceptance Criteria

- [x] A prefix-only edit records nothing and never appears in the restore report again
- [x] An edit that matches the item's defaults records nothing
- [x] Only damage / auto-reuse / use time / use animation / pick / tile boost / defense are
      recorded and restored
- [x] An edited item moved to a different slot is still restored, matched by type
- [x] Every copy of an edited type is restored, not just the first
- [x] An edited type absent from the inventory is reported as absent, not as a failure
- [x] An existing profile is migrated on load: unrestorable fields dropped, emptied entries
      discarded, and the six accessories from the report above disappear
- [x] Empty-slot markers still work and are still keyed by slot
- [x] With the template cache unavailable, edits are still recorded (degrade to today's
      behaviour rather than forgetting them)
- [x] All tests pass headless; flake8 clean on changed files; security review recorded
- [x] README updated; version bumped to 0.30.0 (maintainer confirmed)

## Executive Summary

Auto-restore warned about seven item edits it could not re-apply. All seven were noise, and
the message hid two faults.

The edit dialog submits every field pre-filled with the item's current values, so the
profile recorded an item's own defaults as though the user had chosen them. And it recorded
type, stack and prefix — the three things Terraria writes into the save itself. Six of the
seven "failures" were accessories whose only change was a prefix that had survived on its
own; there was never anything to restore.

The seventh fault is the real one: edits were keyed by slot, so moving an edited item lost
its edit silently — and moving accessories into the equipment column is exactly what specs
032 and 033 encourage.

Now only the fields Terraria regenerates are recorded, only where they differ from the
item's own defaults (compared against the ContentSamples template the compendium already
caches), and restore finds the item by type wherever it now sits, applying to every copy.
An item you are not carrying is reported as *waiting*, not as a failure.

Run against the real profile that produced the warning: **11 entries became 1**. The single
survivor is a Book of Skulls at damage 45 against a default of 29 and use time 12 against
32 — demonstrably an edit. Everything else recorded nothing but defaults.

Reviewers: `Service.record_item_edit` and `restorable_defaults` (what counts as an edit),
the restore loop in `Service.restore` (identity matching and self-pruning), and
`profile._migrate`.

## Testing

354 headless tests, flake8 clean on changed files, `pip-audit 2.10.0` clean.

- `tests/test_profile.py` (6): only the regenerated fields are kept; an edit with nothing
  restorable is not stored at all; and a slot-keyed profile is migrated — the fixture is
  the real one from the report, an accessory whose only change was a prefix alongside every
  default the dialog submitted.
- `tests/test_service.py` (5 rewritten): an edited item is found wherever it now sits;
  every copy is restored, not just the first; an item you are not carrying is `absent`
  rather than skipped **and its edit is kept**; an edit matching the item's defaults is
  forgotten; and a real edit survives the pruning.
- `tests/test_build_ledger.py` (3 rewritten): cheats and item edits stay separate; an
  absent item is worded as waiting, asserted by forbidding the old alarming phrases
  ("no longer hold", "overwritten", "not re-applied", "failed"); and the message says
  nothing about slots, since slots are no longer the identity.
- **Mutation check**: pinning the match back to slot 0 fails both identity tests.

Live, against the real profile and the running game: 11 slot-keyed entries migrated, then
pruned to the 1 genuine edit. Each pruning decision was checked against the game's own
template — Book of Skulls kept at damage 45 vs default 29, the other ten recording only
defaults.
