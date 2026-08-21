# Spec 001: Terraria /proc memory trainer

**Status**: COMPLETE
**Implementation Date**: 2026-08-20

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo.

## Context

Terraria 1.4.5.7 runs as the 32-bit Windows build under Proton (wine-mono). The
game's Player object is managed memory, GC-allocated, but wine-mono's GC does not
move objects, so a live trainer that reads and writes `/proc/<pid>/mem` is stable
within a world. `ptrace_scope=1` requires the trainer to run as root, which it
achieves by re-execing under sudo.

This spec covers a command-line trainer that finds the player from scratch (no
hardcoded address), reports and edits HP and mana, and holds values against the
game (godmode, infinite mana). Offsets are derived live; see docs/discovery.md.

## Requirements

### Functional

1. Detect the running Terraria process automatically.
2. Locate the Player object with no hardcoded address, using a signature scan
   validated by Terraria invariants and the character-name string.
3. Report every player copy found, with a best-effort mark on the live one.
4. Set current HP and mana (to a number or to max).
5. Set permanent max HP and max mana.
6. Freeze HP to max (godmode) and mana to max, held against the game until stopped.
7. Gate all memory writes on the game build; refuse an incompatible major/minor/
   patch version unless `--force`, warn on a hotfix or buildid drift.
8. Re-locate automatically if the world is reloaded mid-freeze.

### Technical

9. System Python (`/usr/bin/python3`); numpy for scanning. No conda dependency.
10. Read/write isolated in `proc.py`; no Terraria knowledge there.
11. Writes apply to all matched copies (the inert snapshots ignore them), so no
    fragile live-copy detection is required for correctness.
12. Unit tests run headless with no game and no root, via an in-memory fake.

## Risks & Assumptions

- **Offsets are build-specific.** They will break on a Terraria update. Mitigated
  by the version gate (`version.py`) and a locator that fails safe: a shifted
  layout matches nothing rather than writing to a wrong address.
- **Godmode is a freeze, not a code patch.** A single hit exceeding current HP can
  register within a frame before the next rewrite. Mitigated by `set-max-hp` for
  headroom. Documented, not eliminated.
- **Writes need root.** The tool self-elevates via sudo; passwordless sudo makes
  it seamless. No credential is stored or logged.
- **Rollback**: the tool holds no persistent state and writes nothing to disk.
  Stopping it (Ctrl-C) ends all effects; edited values are normal game values the
  game continues to manage. Uninstall removes only a symlink and a desktop entry.
- Writing to the inert snapshot copies was verified reversible and gameplay-inert
  before the all-copies design was adopted.

## Acceptance Criteria

- [x] The running Terraria process is detected automatically
- [x] The Player object is located with no hardcoded address
- [x] `status` lists every player copy and marks the live one when determinable
- [x] `set-hp` / `set-mana` change current values (number or `max`)
- [x] `set-max-hp` / `set-max-mana` change permanent maximums
- [x] `godmode` / `freeze --godmode` hold HP at max against live damage
- [x] `freeze --mana` holds mana at max
- [x] `version` reports detected version, buildid and compatibility
- [x] Mutating commands abort on an incompatible build without `--force`
- [x] The freeze loop re-locates when reads start failing (world reload)
- [x] Unit tests pass headless with no game and no root

## Executive Summary

A from-scratch command-line trainer for Terraria 1.4.5.7 (Proton/wine-mono) that
finds the Player object by signature scan over `/proc/<pid>/mem`, then reads,
writes and freezes HP and mana. It self-elevates via sudo, gates writes on the
exact game build, and freezes all matched player copies so no fragile live-copy
detection is needed. Reviewers should start at `locate.py` (the signature and
validation) and `docs/discovery.md` (how the offsets were derived).

## Testing

`tests/test_locate.py`, `tests/test_player.py` - 13 tests, all passing, run
headless against an in-memory fake process (`tests/conftest.py`). No test needs
the game or root. Live validation against the running game confirmed automatic
process detection, from-scratch location, the version gate, and HP editing.
