# Validation Report — Appearance (preferences) section

**Date**: 2026-10-10 23:35
**Commit**: uncommitted (working tree on `main` at 0fa6318)
**Status**: PASSED
**Scope**: the window gains the standard scheme / font / text-size / scale
picker, which the Qt tool had and the Go/fynedesygn port had dropped. It is
`shell.AppearanceSection`, fynedesygn's own preferences pane, added as the last
navigation entry.

## What changed

- `internal/gui/app.go`: `sections()` appends `shell.AppearanceSection(...)` after
  the app's own sections, with a mono sample that reads like the program's log.
- `internal/gui/gui_test.go`: `TestSectionNamesNeedsNoApp` expects "Appearance"
  as the last nav entry and checks the app builders against the other entries
  (Appearance is shell-provided, not an app builder).

## Why

fynedesygn makes appearance a section, not a dialog, deliberately: "these
settings are the whole of what makes the window look like it belongs on the
user's desktop, which is why they are a section rather than a line in a
preferences dialog" (`shell/appearance.go`). The port never added the section, so
the window had no way to change scheme, font or size — the missing "preferences"
the maintainer noticed. Wiring the standard section in is the idiomatic fix and
needs no new UI.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test ./internal/gui/` (cgo, migrated_fynedo) | `ok`, 0 failures |
| Windows | `golangci-lint-v2.12.2`, project config | 0 issues |
| Windows | `gofmt -l` | clean |

**Changed test**: `TestSectionNamesNeedsNoApp` now pins the eight-entry nav
(Appearance last) and checks every entry but Appearance has an app builder.

**Mutation check**: dropping the `AppearanceSection` append → the test fails
(seven entries, no Appearance), which is the pre-change state.

## Phase 4: Code quality

One line added to the section list plus the test update. No duplication, no dead
code. The section is built by the shell; nothing in this repo reimplements it.

## Phase 5: Security review

- No secrets, no subprocess, no network. The section reads and writes the
  window's own appearance settings through the shell's settings store, which is
  the user's file under their config directory.

## Phase 5.5: Release safety

- **Change type**: GUI-only, additive (a navigation entry).
- **Rollback**: revert the one-line append; the window loses the section and is
  as it was. Minutes.
- **Rollout**: a visible, inert section the user opens; nothing runs on its own.

## Overall

- All gates passed: YES
- Notes: the window now has its preferences back. Behaviour toggles
  (auto-restore-on-launch, nav layout) are not part of this section and could be
  a later addition if wanted.
