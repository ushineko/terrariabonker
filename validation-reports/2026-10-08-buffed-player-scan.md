# Validation Report — the player scan finds a buffed player

**Date**: 2026-10-08
**Commit**: uncommitted (working tree on `main` at 0cafddc)
**Status**: PASSED
**Scope**: `locate.FindPlayers`' prefilter bounded current life by the *permanent*
life cap, so a player whose life was above it -- inside the boosted cap, as a
Lifeforce potion leaves them -- was dropped before `ValidBlock` ever saw them. Found
during spec 052 phase 3 step 2, slice 1; fixed at the maintainer's request. It
affected both runtimes. The bound is now the permanent cap plus `BoostHeadroom`, the
most a boosted cap can be; `ValidBlock` still applies the exact rule.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | 27 packages `ok` |
| Linux (njv-cachyos), fresh clone at 0cafddc + this tree | `make test` | 27 packages `ok` |

- `TestFindPlayers` planted two copies -- one buffed, 420 of 400 permanent and 420
  boosted -- and asserted that one was found. It now asserts both, by address and
  fields. Against the old code it fails.
- `TestABuffedCLRPlayerIsFound`: the same case under the CLR entry (480 of 400
  permanent, 500 boosted). Against the old code it fails.
- Mutation: restoring the old prefilter fails both tests.

## Phase 4: Code quality

- Performance, measured on the live Windows game: `FindPlayers` finds the same 7
  copies in 1.29–1.33 s with the fix and 1.30–1.31 s without it.
- `make lint`: 0 issues.
- Status: PASSED.

## Phase 5: Security review

- `govulncheck`: no vulnerabilities found. No dependency changes.
- No write path changed. A player is still accepted only by `ValidBlock` and a
  readable name, so the looser prefilter admits no block that validation rejects.
- Status: PASSED.

## Phase 5.5: Release safety

- Behaviour change on Linux, intended: a buffed player is now found. Before, every
  operation on such a player failed with "no player found", or acted on a non-buffed
  copy only.
- Rollback: revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped (the maintainer's call; this is a user-visible fix).
