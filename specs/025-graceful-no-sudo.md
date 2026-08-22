# Spec 025: Graceful behavior when passwordless sudo is missing

**Status**: COMPLETE
**Implementation Date**: 2026-08-22

> **Note**: No issue tracker ticket (personal utility).

## Context

The trainer edits game memory via `sudo` (ptrace_scope=1). The GUI runs unprivileged and
shells each memory action out to the CLI under sudo. Without **passwordless** sudo the GUI
can't answer a password prompt (its `QProcess` has no TTY), so memory actions previously
failed **silently** — the confusing "nothing happens" case. A hard refusal is the wrong
answer: the CLI works fine with interactive sudo, and the recipe browser / item icons are
unprivileged. So: degrade gracefully with a clear, actionable warning.

## Requirements

1. Do **not** refuse to start. Unprivileged features (recipe browser, item icons) keep
   working; the CLI keeps working with an interactive password prompt.
2. Detect missing passwordless sudo at startup and show a clear, actionable warning.
3. Memory actions fail **fast** (with a reason), not silently or hung.

### Technical

4. GUI sudo spawns use `sudo -n` (non-interactive) — without a password cached, the call
   returns immediately with "a password is required" instead of blocking on a prompt that
   can't be answered in a `QProcess`.
5. `_passwordless_sudo()` runs `sudo -n true` at startup; `_check_sudo` shows a persistent
   warning banner when it fails, pointing at the README's NOPASSWD sudoers instructions.
   The banner is hidden when sudo is fine.
6. README documents the requirement and a concrete, tightly-scoped NOPASSWD example.

## Risks & Assumptions

- **Detection freshness.** Checked at startup; sudoers config rarely changes mid-session,
  and the `-n` spawns fail-fast anyway if it does.
- **Scope.** Only messaging/behavior when sudo is unavailable; the privileged path is
  unchanged when sudo works.
- **Rollback.** `git revert`. GUI-only; no memory or state changes.

## Acceptance Criteria

- [x] GUI starts without passwordless sudo; recipe browser / item icons still work; no hard
      refusal
- [x] A clear warning banner appears when `sudo -n true` fails (verified via an offscreen
      render with sudo simulated unavailable), and is hidden when sudo works
- [x] Memory spawns use `sudo -n` so they fail fast with a reason instead of hanging/silent
- [x] README documents the passwordless-sudo requirement + a concrete NOPASSWD example
- [x] 115 tests pass; flake8 clean; version 0.17.1 (user-approved)

## Executive Summary

When passwordless sudo isn't configured, the GUI no longer fails silently: it shows a
persistent, actionable warning banner (pointing at the README's NOPASSWD instructions),
switches its memory spawns to non-interactive `sudo -n` so they fail fast with a reason,
and keeps the unprivileged features (recipe browser, item icons) working — rather than
refusing to start. Reviewers: `_cli_args` (`-n`), `_passwordless_sudo`/`_check_sudo`, the
banner in `_build`, and the README Requirements note.

## Testing

Offscreen render with `_passwordless_sudo` stubbed to False confirmed the banner shows and
the tabs remain usable; with sudo available the banner stays hidden. 115 tests pass; flake8
clean.
