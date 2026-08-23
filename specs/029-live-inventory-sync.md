# Spec 029: Live inventory sync + stale-write guard

**Status**: INCOMPLETE

> **Note**: No issue tracker ticket (personal utility).

## Context

The GUI's inventory grid is a snapshot, refreshed only by the **Refresh** button and 600 ms
after an edit (`main_window.py` `refresh_inventory` / `_on_cell_clicked`). If the player
moves items in-game after that snapshot, the grid is stale, and editing a stale slot is
destructive:

`_apply_item_edit` compares the dialog result against the **cached** `orig_type`. A
same-type field edit therefore sends the cached type plus cached stats; `Service.set_item`
re-reads the slot, sees the live type differs, treats it as a type change, and templates the
cached item over whatever is actually in the slot. A Zenith moved into that slot is replaced
by the Copper Pickaxe the grid still believed was there. The slot-emptied and
slot-now-holds-something-else cases fail the same way.

Two independent problems, and they need different fixes:

1. **Freshness** — the grid should track the game without the user pressing Refresh.
2. **Correctness** — a write built from a stale snapshot must be rejected, not applied.
   Polling only narrows the race (snapshot at T, dialog opened at T+0.5, item moved at
   T+0.9, Apply at T+8); it cannot close it.

### Why sync is not currently affordable (measured, live game, 59 slots)

| step | cost |
| --- | --- |
| `find_players()` — memory scan | 962 ms |
| `resolve_local_player()` — second scan | 1434 ms |
| reading all 59 slots | **2.3 ms** |
| total `Service.inventory()` | 2299 ms |
| full `inventory --all --json` CLI round trip | 2734 ms (median of 6) |

Reading the data is free; 99.9% of a refresh is re-locating the player, repeated on every
invocation because each GUI action spawns a fresh CLI process. The existing 2 s status timer
already pays this: `status --json` measures 3432 ms, i.e. the app burns ~172% of one core
continuously with overlapping scans. Adding a 1 Hz inventory poll to this path is not
possible — ticks would overlap ~3 deep.

## Requirements

1. The inventory grid tracks the running game at ~1 Hz without user action, at negligible
   CPU cost, and without disturbing hover/tooltips or an open edit dialog.
2. A write built from a stale snapshot is refused with a clear message instead of
   overwriting the slot; the grid then refreshes so the user can retry against truth.
3. Behaviour without passwordless sudo is unchanged (spec 025): the GUI degrades with a
   warning, recipes/icons keep working.
4. The CLI keeps its current one-shot contract for external users and scripts.

### Technical

5. **`terrariabonker serve`** — a new privileged subcommand, one long-lived process, that
   reads newline-delimited JSON requests on stdin and writes one JSON response per line on
   stdout. Request `{"id": N, "argv": [...]}`; response `{"id": N, "ok": true, "out": "..."}`
   or `{"id": N, "ok": false, "error": "..."}`. `argv` is dispatched through the **existing**
   argparse parser in-process, so the GUI's `client.*_argv()` builders are unchanged and the
   CLI stays the single contract. Subcommands are allowlisted; anything not on the list is
   refused.
6. **Warm locate.** `Service` caches the located blocks and live block. Before reuse the
   cache is validated cheaply (pid alive; `statLife`/`statLifeMax` plausible and consistent;
   player name still matches at `life_addr`). Validation failure or a pid change forces a
   rescan. One-shot CLI behaviour is unchanged (it locates once and exits).
7. **GUI transport.** One `QProcess` running `sudo -n -E … serve`, driven entirely from the
   Qt event loop — `readyReadStandardOutput` → parse complete lines → dispatch to the pending
   request's callback by `id`. No threads. If the helper cannot start or exits, the GUI falls
   back to the current per-action spawn path and says so once in the log.
8. **Lifetime.** The helper exits on stdin EOF, so it cannot outlive the GUI; the GUI also
   terminates it explicitly on close. It re-locates rather than exiting when the game pid
   changes (game restart).
9. **Sync loop.** A 1 s `QTimer` requests `inventory --all --json` only while the Inventory
   tab is visible and no edit dialog is open, and never overlaps its own in-flight request.
   The existing 2 s status timer keeps its cadence but now runs through the helper.
10. **No-flicker render.** Re-render only cells whose row actually changed. The
    changed-cell diff is a pure function in `invgrid` so it is unit-testable.
10a. **Fresh slot read on open.** Clicking a slot re-reads that one slot through the helper
    and populates the dialog from the result, so the editor always opens on truth rather
    than an up-to-1-second-old row. If the read fails, fall back to the cached row.
11. **Stale-write guard.** `Service.set_item(..., expect_type=None)` re-reads the slot and
    raises `ServiceError` before any write when `expect_type` is not None and the live type
    differs. Surfaced as `set-item --expect-type N`; `client.set_item_argv` gains the
    argument; the GUI always sends the type it displayed. On refusal the GUI shows the
    message, refreshes the grid, and writes nothing.

## Risks & Assumptions

- **Mono may move objects.** The managed heap is GC'd, so a cached `life_addr` can go stale
  or point at a different object. This is why requirement 6 validates before every reuse
  rather than trusting the cache; the existing code already re-resolves `array_addr()` per
  read for the same reason. Validation is a handful of reads (microseconds).
- **A long-lived root process is a privilege boundary.** It is driven by a pipe from an
  unprivileged GUI. Mitigations: allowlisted subcommands, argparse-validated integer
  arguments, no shell, no `eval`/`exec`, no network, explicit argv only, and it acts only on
  the located Terraria pid. This does not widen existing privilege: anything the GUI can ask
  the helper to do, it can already do today by spawning `sudo -n terrariabonker …` itself.
- **Guard scope.** Type mismatch only. Stack/damage/prefix are values the user is explicitly
  setting, so in-game drift there must not block the edit; consumables ticking down while the
  dialog is open would otherwise cause constant false refusals.
- **TOCTOU remains, bounded.** Check and write happen in one process microseconds apart. An
  item moved inside that window is still mis-written; this is inherent to editing a running
  game's memory and is not addressed here.
- **Rollback.** Revert. The helper is opt-in at runtime — if `serve` fails for any reason the
  GUI uses the pre-existing spawn path, so a bad helper degrades to today's behaviour rather
  than breaking the app. The guard is additive (`expect_type=None` preserves current
  semantics for CLI users).
- **Two commits.** The guard does not depend on the helper; land it separately so either can
  be reverted alone.

## Acceptance Criteria

- [x] `Service.set_item(slot, type, expect_type=T)` raises `ServiceError` naming both the
      expected and actual item, and performs **no write**, when the live slot type differs
      (headless test against the synthetic image asserts memory is byte-unchanged)
- [x] `set-item --expect-type` exposes the guard on the CLI; omitting it preserves today's
      behaviour exactly
- [x] The GUI sends `--expect-type` for every slot edit; on refusal it shows the message,
      refreshes the grid, and does not write
- [ ] `terrariabonker serve` answers newline-delimited JSON requests, one response per line,
      matching request `id`; unknown/unallowlisted subcommands are refused without executing
- [ ] The helper exits on stdin EOF (no orphaned root process) and re-locates across a game
      pid change
- [ ] `Service` reuses a validated locate cache; a corrupted/moved `life_addr` forces a
      rescan (headless test asserts the rescan happens rather than reading a stale address)
- [ ] The GUI drives the helper from the Qt event loop only (no threads), and falls back to
      per-action CLI spawn when the helper is unavailable — including the no-passwordless-sudo
      case from spec 025
- [ ] Inventory auto-syncs at 1 Hz while the tab is visible, pauses while an edit dialog is
      open, and never overlaps an in-flight request
- [ ] Clicking a slot re-reads that slot before the dialog opens; the dialog is populated
      from the fresh read, and falls back to the cached row if the read fails
- [ ] Only changed cells re-render (pure `invgrid` diff, unit-tested); hovering a slot for
      several seconds keeps its tooltip
- [ ] Measured on a live game: an inventory tick through the helper costs < 20 ms (recorded
      in the validation report), versus 2734 ms today
- [ ] All tests pass headless; flake8 clean on changed files; security review recorded
- [ ] README updated (auto-sync behaviour, `serve`); version bumped to 0.20.0 (maintainer
      confirmed) in `terrariabonker/__init__.py`, About dialog/titlebar, and README

## Executive Summary

_Populate before opening the PR._

## Testing

_Populate during implementation._
