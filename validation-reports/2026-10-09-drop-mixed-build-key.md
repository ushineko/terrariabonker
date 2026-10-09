# Validation Report — the mixed build key removed

**Date**: 2026-10-09
**Commit**: uncommitted (working tree on `main` at 6f5fb2d)
**Status**: PASSED

## Scope

`1.4.5.7+24893155` named no real build. It paired 1.4.5.7's version with 1.4.5.8's
build id, and came from a version detector that has since been fixed. It was kept in
the anchor ledgers only because verifications had been recorded under it.

What was removed:
- the constant `layout.Build1457s24893155`, and its entry in `SeenBuilds`;
- the key, from the mono ledger (`monoVerified`), from `patch.verifiedBuilds`, and from
  the four `verifiedInstead` ledgers. Every ledger that held it also held 1.4.5.8, so the
  current build stays verified everywhere it was before;
- the comments explaining it, except in completed specs, which keep their history.

## Phase 3: Tests

- **Windows:** `go test -count=1 ./...` passes.
- **Digests re-pinned on purpose:** the version table, the anchor table, and the mono
  cheat fingerprint, whose docstring now records this change after the move it proved.
- **`git diff` reviewed:** the only changes are the removed key, its comments, and the
  constant it replaced in one test.

## Phase 4: Code quality

- Lint reports 0 issues natively and 0 for `GOOS=windows`.
- One stale comment corrected: it said "two 1.4.5.7 builds", and there is now one.
- Status: PASSED.

## Phase 5: Security review

- No code paths changed, only data and comments.
- No dependency changes.
- Status: PASSED.

## Phase 5.5: Release safety

- **Effect on players:** none. The running build's key is 1.4.5.8, which every ledger
  still lists.
- **Old decision records:** a decision saved on someone's machine under the removed key
  matches nothing, as was already the case, because the detector no longer produces
  that key.
- **Rollback:** revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped.
