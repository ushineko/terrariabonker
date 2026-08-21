# Spec 003: Shared service layer (view parity)

**Status**: COMPLETE
**Implementation Date**: 2026-08-20

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

The CLI and GUI had drifted: the GUI called `inventory --json` before the CLI
registered that flag, a runtime gap invisible to tests. Following the jira-viewer
dual-shell design, this introduces a view-neutral core both shells consume and
tests that make such drift a build failure. The privilege boundary (GUI is
unprivileged; memory access needs root) means the GUI cannot call the core
in-process, so it crosses a sudo subprocess boundary through a single client
module that mirrors the core.

## Requirements

1. A toolkit-free `service.py` core: every view-facing operation (`snapshot`,
   `set_hp`, `set_item`, `give_item`, `fast_mining`, `long_reach`, …) returning
   plain dataclasses, with shared behaviour (live-copy selection, give-to-first-
   empty-slot) defined once.
2. `cli.py` becomes a thin adapter over the service; `--json` is the subprocess
   contract.
3. `gui/client.py`: the single definition of the CLI argv the GUI issues, plus the
   `--json` parsers. Toolkit-free so it is testable without Qt.
4. The GUI consumes only `client` (no raw command strings).
5. Tests enforce: the core imports no GUI toolkit or argparse; the GUI client
   imports no toolkit; every client command exists in the CLI parser; every argv
   the client emits parses cleanly.

## Risks & Assumptions

- **Privilege boundary retained**: the GUI still shells to the CLI via sudo; the
  "shared layer" is the operation/data-shape contract, enforced by parity tests,
  not an in-process call. Documented as the one deviation from jira-viewer.
- **Rollback**: pure refactor; `git revert`. No behaviour change intended beyond
  moving the give-to-empty-slot logic into the shared core.
- No new dependencies, no network, no secrets.

## Acceptance Criteria

- [x] `service.py` holds all operations and returns dataclasses
- [x] `cli.py` is a thin adapter; `status`/`inventory` support `--json`
- [x] `gui/client.py` is the sole subprocess-contract definition
- [x] GUI uses only `client` (verified: no raw argv literals in `main_window`)
- [x] `give` moved into the service (CLI `give` command + GUI both use it)
- [x] `test_view_parity.py`: client↔CLI parity + toolkit-free core/client
- [x] `test_service.py`: service operations verified against a fake game image
- [x] 41 tests pass headless; lint clean; GUI event loop clean offscreen

## Executive Summary

Refactors the trainer to a view-neutral service core with a single GUI subprocess
client and parity tests, adopting the jira-viewer dual-shell pattern (adapted for
the sudo privilege boundary). The `inventory --json` class of CLI/GUI drift is now
a test failure. Reviewers should start at `service.py` (the core), `gui/client.py`
(the contract), and `tests/test_view_parity.py` (the guardrail).

## Testing

`tests/test_service.py` and `tests/test_view_parity.py` added; full suite 41 tests,
all passing headless. GUI verified by offscreen show + event loop; CLI verified
live against the running game through the service.
