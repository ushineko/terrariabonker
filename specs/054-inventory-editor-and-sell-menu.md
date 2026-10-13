# Spec 054: The item editor's stats, and the sell list on a right-click

**Status**: COMPLETE — checked headless on Windows 11. Not driven against the running game.

> **Note**: This work has no associated issue tracker ticket (personal utility).

Three things the maintainer reported against 0.45.0:

1. The Inventory editor could change an item's type, stack and modifier and nothing else.
   The Python panel it replaced could also change damage and the other stats, and the CLI's
   `set-item` still can.
2. Putting an item on the sell list was a button inside the edit dialog, where it sat beside
   fields it had nothing to do with.
3. The README and the documents under `docs/` and `ce/` described things the program no
   longer does, and did not say which environments it supports.

## Design

### The editor

The dialog has a field for each thing `set-item` writes: item, stack, damage, defense,
modifier, auto-reuse, use time, use animation, pickaxe power, placement reach. What the
fields turn into is decided in `internal/gui/itemedit.go`, apart from the widgets:

- **Only the fields that were changed are written.** The dialog opens showing the item's
  current stats. Sending them all back submits the item's own damage as an explicit edit,
  which lands after a modifier is applied and overwrites what the modifier computed (spec
  046), and re-applies an unchanged modifier, which resets the item to its base stats first.
- **A different item is written as its type and stack alone**, so the game's template
  supplies its stats. The stat fields are disabled while a different item is named.
- **The item field takes a name or a number** and shows what it resolved to.
- **The modifier list holds what the item can take**, by its damage class. An empty slot is
  offered all of them; a modifier the item already carries is always in the list.
  Modifiers that share a name carry their id in the label, because the dropdown reads its
  answer back by label.
- A write to a slot that holds something passes `--expect-type`, as before.

The backend is unchanged. The GUI still reaches memory only through the CLI.

### The sell list

Right-clicking a cell opens a menu: **Edit …**, and **Add to the sell list** or **Take off
the sell list**; an empty cell offers **Place an item…**. The button is gone from the
dialog. A cell's sell-list border is redrawn when the list changes; before, a cell was
redrawn only when its slot changed.

### The documents

- `README.md`: claims the program does not meet are removed (compendium sorting, tooltips
  and spawn distance; a boss confirmation; a modifier dot; "writes nothing to disk"). A
  table of supported environments is added. The GUI section follows the window's real
  sections.
- `docs/discovery.md`: names the Go tools and `internal/layout` in place of retired Python
  files.
- `ce/README.md`, `docs/item-fields.md`: marked as dated records, with the Python tool names
  mapped to their replacements.

## Acceptance criteria

- [x] Damage, defense, auto-reuse, use time, use animation, pickaxe power and placement
      reach can each be changed from the dialog, and each reaches `set-item` as its own flag.
- [x] An edit that changes one field writes that field and no other.
- [x] Naming a different item writes type and stack only, and the stat fields are disabled
      while it is named.
- [x] The item field accepts a name or a number.
- [x] A weapon is not offered accessory modifiers; two modifiers with one name are two
      distinct choices.
- [x] Right-clicking an item offers the sell list, and the label says which way it will go.
- [x] The dialog has no sell-list control.
- [x] A cell's border follows the sell list when the list changes.
- [x] The README states the supported game versions and operating systems.
- [x] `make test` and `make lint` pass on Windows.
- [ ] `make test` and `make lint` on Linux — not run; CI runs them on push.
- [ ] The menu and the editor driven against the running game — not done.

## Found and left

- The Compendium spawns a boss at once, with no confirmation. The Python panel asked first.
  The README now says what happens; restoring the confirmation is a separate change.
- `assets/screenshot-inventory.png` shows the old hint line. `tools/screenshot.sh` runs on
  Linux only.
- `tools/__pycache__/extract_prefix_stats.cpython-312.pyc` is tracked and has no source.
- The count on the sell card under Effects is not refreshed when the list changes from the
  Inventory menu.
