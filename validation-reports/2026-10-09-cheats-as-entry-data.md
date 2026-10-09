# Validation Report — in-place cheats as entry data; mining and reach on the CLR

**Date**: 2026-10-09 10:19
**Commit**: uncommitted (working tree on `main` at b243a4f)
**Status**: PASSED
**Scope**: spec 052, slice 7.

- The in-place cheats' sites, their anchors and the player fields they set are now data on
  each version-table entry (`layout.CheatSite`, `layout.AnchorDef`, `Entry.PlayerValues`).
  `patch.Cheat` keeps only what does not vary by runtime.
- The CLR entry gets mining and reach.
- Code that differs by runtime is now a module selected by name:
  - the live-player finder (`locate.LiveFinder`) replaces the `ByStatics` branch in
    `service.resolveLive`;
  - injection sets and encoders are registries.
- `layout.Derive` builds a variant entry from a base without sharing its storage.
- Build keys are declared once.
- The rule is recorded in `AGENTS.md`.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` (per-user dirs redirected to scratch) | all packages `ok` |
| Linux (njv-cachyos): fresh checkout of b243a4f plus this tree | `make test` (race detector) | 27 packages `ok` |
| Windows, live game | `build-check` | mining and reach resolved, one site each; the other 13 cheats report "has no code site under netfx-4.8.1 yet" |
| Windows, live game | `patch enable mining`, `patch enable reach`, then disable both | the maintainer confirmed faster mining and longer placement reach in play. After disabling, the site read back from the game as `89 96 70 05 00 00 D9 E8 D9 9E 14 05 00 00`, which is the original bytes |

**New tests**

- `TestTheMonoCheatsAreWhatTheyWere` checks that the move did not change the mono cheats.
  Before the move, the mono anchors' patterns, ledgers and sites, the encoders' output and
  the value fields were printed and hashed. The test rebuilds the same text from the entry
  and requires the same SHA-256.
- `TestEachCheatSiteStoresToTheFieldItsValueSets`: under both entries, the displacement
  in each site's original instruction, minus statLife's offset, equals the `PlayerValues`
  offset the cheat writes to.
- `TestTheCheatValuesAreTheTables` and `TestEverySiteNamesItsEntrysAnchor`.
- `TestEveryEncoderASiteNamesExists` and `TestEveryBuildKeyIsADeclaredOne`.
- `TestUnderTheCLRMiningAndReachArePatched`: on the synthetic image, the CLR bytes and
  values are written, and disabling restores the code exactly.
- `TestUnderTheCLRACheatWithoutASiteIsRefused`: the status shows the reason, and enabling
  is refused with the image unchanged.
- `TestEveryEntrysWayOfFindingTheLivePlayerExists` and
  `TestTheZeroEntryFindsNoLivePlayer`.
- `TestADerivedEntryLeavesItsBaseAlone` and `TestADerivedEntrysListsAreItsOwn`.
- The version-table digest was re-pinned on purpose. The new entry data was reviewed
  against the dump, and the 1.4.5.8 ledger entry was added after the live confirmation.

**Mutation checks**

All of these mutants were killed:
- the CLR mining patch offset;
- the CLR pickSpeed value offset;
- `Service.Equip` not called;
- the per-cheat write gate removed;
- the scanner not looking anchors up through the entry;
- the no-site reason dropped from the status;
- the live-finder registry swapped, both ways;
- the live-finder registry missing an entry;
- `Derive` sharing any one of its maps or lists (each one tried in turn);
- an undeclared build key.

One mutant survived:
- **Mutation:** build-check reads its ledger from the global injection anchors instead of
  the entry's anchors.
- **Why it survived:** it is equivalent on today's data. Every mono ledger lists the same
  builds, and the CLR's answer is forced to "not known" because that entry is read-only.
- **What changed anyway:** build-check now reads `patch.AnchorsFor(entry)` directly.

## Phase 4: Code quality

- `make lint`: 0 issues on Linux (after clearing a stale lint cache that pointed at a
  deleted directory).
- On Windows, the native lint and the `GOOS=windows` lint of every non-GUI package: 0
  issues each.
- **Dead code:**
  - the moved anchors were removed from `patch/anchors.go`;
  - the package-level `IsEnabled` and `AnchorKey` were removed;
  - `NeedsArena` was added and then removed: it was untested and is unneeded while the
    status gate keeps the arena off read-only entries.
- **Duplication:** none added. The two runtimes' live-player code stays in their own
  files, behind one interface.
- Status: PASSED.

## Phase 5: Security review

- **Dependencies:** `go.mod` and `go.sum` are unchanged. `govulncheck ./...` on Linux
  found no vulnerabilities.
- **No new network calls, subprocesses or secrets.**
- **Write paths:** a cheat is a per-entry write feature. A cheat's value goes only to the
  live character's copies, the same `writeTargets` used by every other write. Under an
  entry with no site for a cheat, the cheat is refused before the game is attached for
  writing.
- **Arena allocation:** `patch status` allocates the arena only where the build gate allows
  writes. Before this change, status was refused outright on such a build.
- Status: PASSED.

## Phase 5.5: Release safety

- **Change type:** code only.
- **Linux:** the mono cheat data is identical by digest. One behaviour change: `patch
  status` no longer refuses on an incompatible build. It reads, and skips allocating the
  arena.
- **Windows:** mining and reach become available. Every other cheat is reported as not
  available yet.
- **Rollback:** revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- **Version:** not bumped (the maintainer's call).
- **Follow-up:** `patch.Anchor.Variants` is keyed by build alone, which overlaps with
  entries plus `Derive`. It is unused, and folding it into entries is a separate change.
