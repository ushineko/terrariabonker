# Spec 031: Remember the panel's window geometry

**Status**: COMPLETE
**Implementation Date**: 2026-08-23

> **Note**: No issue tracker ticket (personal utility).

## Context

The panel reopens at its natural minimum every time, so a user who sizes and places it to
suit their desktop has to redo it on every launch.

Size and position turn out to be different problems on this desktop. Measured with a probe
window under this KDE Wayland session:

```
asked for      : (700, 400)
Qt pos()       : (700, 400)      <- Qt reports the value it was given
KWin says      : 1116, 1762      <- where the window actually is
```

`QWidget.move()` is a silent no-op and `pos()` returns the requested value rather than the
truth, which matches the warning already recorded in `AGENTS.md`. An app cannot save a
position it is not permitted to read, so guessing from Qt's numbers would persist nonsense.
`resize()` works normally.

## Requirements

1. The panel reopens at the size the user left it, falling back to the natural minimum.
2. Position is remembered across restarts on KDE, without the app inventing coordinates.
3. Nothing breaks on a non-KDE desktop, and the installer stays runnable there.
4. Uninstalling removes whatever the installer added.

### Technical

5. **Size** is stored by the GUI in `~/.cache/terrariabonker/window.json`. Ownership follows
   who writes: this file is written only by the unprivileged panel, so it belongs to the
   user, and the config directory can be root-owned from sudo memory commands (the same
   reason already recorded for the sprite cache). Implausible values are ignored so a stale
   size from an unplugged monitor cannot open an unusable window, and a failed write never
   prevents the window from closing.
6. **Position** is delegated to KWin, which has a "Remember" rule type for exactly this.
   `tools/kwin_rule.py --install/--remove` registers a rule matched on `wmclass=terrariabonker`
   **and** `title="terrariabonker v"` with `titlematch=2` (substring), with
   `positionrule=4` / `screenrule=4` (the idiom used by the existing rules on this
   desktop), via `kwriteconfig6`, then asks KWin to reconfigure so it applies without a
   re-login. The title clause is not decoration — see the follow-up finding below. `install.sh` calls it, `uninstall.sh` calls `--remove`, and both are no-ops
   when the KDE config tools are absent.
7. Size is left out of the KWin rule so the two mechanisms cannot fight over it: the app
   owns size (and works on any desktop), KWin owns position and screen.

## Risks & Assumptions

- **The installer writes to the user's KWin config.** `kwriteconfig6` rewrites
  `kwinrulesrc` wholesale — sections reordered, keys sorted — which is the same
  normalisation any KDE settings dialog performs. Verified lossless on a real 13-rule file:
  same sections in and out, no key or value changed, and `--remove` restored the original
  rule list exactly.
- **Rule index selection** takes `max(existing numeric) + 1`, and an existing
  terrariabonker rule is updated rather than duplicated, so re-running the installer is safe.
- **Non-KDE desktops** get size persistence only; position falls to the window manager's own
  placement, which is the status quo.
- **A wmclass-only rule captures the app's dialogs too — found later, in spec 035.** Every
  modal opened from the panel (recipe view, compendium entry, confirmations) began appearing
  pinned to the screen's upper-left instead of centred on its parent. The cause was this
  rule: KWin matched *every* window of the process, dialogs included, and applied the
  remembered top-level position to each. It was our own regression, not a Qt or theme
  problem.

  The first fix attempted — adding `types=1` to restrict the rule to normal windows — **did
  not work**; the behaviour was unchanged and was re-measured three ways to confirm. What
  works is narrowing the match to the main window by title as well as class:
  `title="terrariabonker v"` with `titlematch=2` (substring), which the titlebar carries and
  the dialogs do not. `--remove` also strips the now-unused `types` key.

  The general lesson for any future window rule here: match the main window specifically, or
  the rule silently becomes a rule about dialogs.
- **Rollback.** `git revert` plus `tools/kwin_rule.py --remove`; the cache file is
  disposable.

## Acceptance Criteria

- [x] The panel reopens at the size it was closed at; an unset, corrupt or implausible
      saved size falls back to the natural minimum
- [x] A failed settings write does not prevent the window from closing
- [x] Size state lives under `~/.cache`, not the possibly-root-owned config directory
- [x] `tools/kwin_rule.py` adds a `wmclass=terrariabonker` rule with `positionrule`/
      `screenrule` set to Remember, is idempotent on re-run, and removes cleanly
- [x] The rule matches only the main window, not the app's dialogs: narrowed with
      `title="terrariabonker v"` / `titlematch=2` after the wmclass-only form was found to
      pin every modal to the upper-left (maintainer-confirmed fixed)
- [x] The rule survives a `kwinrulesrc` round trip without altering any other rule's content
- [x] `install.sh` registers the rule and `uninstall.sh` removes it; both succeed when the
      KDE tools are missing
- [x] All tests pass headless; flake8 clean on changed files; security review recorded
- [x] README updated; version bump confirmed by the maintainer

## Executive Summary

The panel now reopens at the size it was closed at, and KWin remembers where it was. The
split exists because Qt's position API is unusable here: a probe asked for (700,400), Qt
reported (700,400), and KWin had placed the window at (1116,1762) — so the app owns size
(which `resize()` sets correctly) and the compositor owns placement through a "Remember"
window rule the installer registers and the uninstaller removes.

Reviewers: `gui/uistate.py` (why size lives under `~/.cache`) and `tools/kwin_rule.py` (the
kwinrulesrc edit and its removal path).

## Testing

`tests/test_uistate.py` (6): round trip, unset, corrupt file, implausible sizes rejected, a
failed write never blocking window close, and the path living under `~/.cache`.

Live: seeded 1100x1050 and KWin reported 1108x1086 (exactly plus decoration); a size below
the layout minimum clamps to the minimum, as Qt does for any window. `tools/kwin_rule.py`
was run install -> install -> remove against the real 13-rule `kwinrulesrc`: idempotent on
re-run, and removal restored the original rule list with every other rule's content
unchanged (verified by parsing both files and diffing section by section).
