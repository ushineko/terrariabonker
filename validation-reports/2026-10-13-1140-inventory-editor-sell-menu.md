# Validation Report — item editor stats and the sell-list menu (spec 054)

**Date**: 2026-10-13 11:40
**Commit**: uncommitted (working tree on `main` at 618c315); released as v0.45.1
**Status**: PASSED headless. Not driven against the running game.
**Scope**: `internal/gui` (the Inventory view and its editor), `README.md`,
`docs/discovery.md`, `docs/item-fields.md`, `ce/README.md`, `.tag`,
`fynstall.yaml`. Nothing under `internal/cli` or anything that reads or writes
the game.

## What changed

- `internal/gui/itemedit.go` (new): what the editor writes for a set of typed
  values, which modifiers an item is offered, how they are labelled, and what a
  typed item resolves to.
- `internal/gui/views_inventory.go`: a cell takes a right-click and opens a
  menu; the edit dialog gains damage, defense, auto-reuse, use time, use
  animation, pickaxe power and placement reach, and loses its sell-list button;
  cells are redrawn when the sell list changes.
- `internal/gui/views_effects.go`: the sell list is taken through the same
  function the Inventory view uses.
- Documents: see spec 054.
- Version 0.45.0 to 0.45.1.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `make test` (`go test -race ./...`) | every package with tests `ok` |
| Windows 11 | `make lint` (golangci-lint v2.12.2, project config) | 0 issues |
| Windows 11 | `gofmt -l cmd internal` | clean |

Not run on Linux. Coverage not measured.

**New tests** (`internal/gui/itemedit_test.go`, 10):
`TestEveryItemStatCanBeEdited`, `TestOnlyTheChangedFieldsAreWritten`,
`TestADifferentItemArrivesWithItsOwnStats`, `TestTheModifiersOfferedFitTheItem`,
`TestEveryModifierLabelIsItsOwn`, `TestAnItemIsTypedByNameOrNumber`,
`TestTheEditorReadsWhatWasTyped`,
`TestTheStatsAreOffWhileADifferentItemIsNamed`,
`TestRightClickingACellOffersTheSellList`,
`TestACellIsReMarkedWhenTheSellListChanges`.

**Mutation checks** — 9 mutants, 9 killed: every field sent; the menu label
never flips; the list arrives and no cell is redrawn; the damage field built
and never read; shared modifier names not told apart; a right-click does
nothing; a type change sends the old item's stats; the stat fields stay on for
a different item; a sword offered every modifier.

## Built and installed (Windows 11)

Done at 0.45.0, before the version was bumped; not repeated at 0.45.1.

- `make installer` left four files in `dist\`.
- `terrariabonker-0.45.0-windows-amd64-cli-installer.exe --yes` exited 0 and
  replaced the install in `%LOCALAPPDATA%\Programs\io.ushineko.terrariabonker`.
- The installed `terrariabonker version` reported game 1.4.5.8, an exact match.

## Phase 4: Code quality

- Dead code: the `sort` import in `views_effects.go` became unused and was
  removed. None other found.
- Duplication: the sell list had two places that stored it; both now go through
  `takeSellList`.
- Encapsulation: the editor's decisions are in `itemedit.go`, free of widgets,
  and tested without a display.
- Status: PASSED

## Phase 5: Security review

- Dependencies: none added or changed; `govulncheck` not run, per project rule.
- Injection: the CLI is still started with an explicit argument list. A typed
  item name is resolved to a number in the window and only the number is
  passed. Numeric fields are parsed and bounded before a write.
- Secrets: none. Network: no new calls. Files: no new reads or writes.
- A write to an occupied slot still carries `--expect-type`.
- Status: PASSED

## Phase 5.5: Release safety

- Change type: code-only, in the window. The CLI and its flags are unchanged.
- Rollback: revert the commit and rebuild, or install 0.45.0.
- Rollout: immediate.
- Status: PASSED

## Overall

- All gates passed: YES, on Windows.
- Not verified: Linux; the menu and editor against the running game.
