# Validation Report — the CLR entry may make every write (spec 052, slice 28)

**Date**: 2026-10-12 00:35
**Commit**: uncommitted (working tree on `main` at b5e112c)
**Status**: PASSED headless. Live: the CLI's reports are checked and the window
was started; what the window shows and any write made from it are the
maintainer's to watch -- see "Live confirmation".
**Scope**: `clrEntry.Writes` is true. The maintainer asked for the flip and for
the window to be started (2026-10-12).

## What changed

- `internal/layout/entry_netfx.go`: `Writes: true`, and the entry's opening
  comment, which still said "never for writing yet".
- Tests only, otherwise. No other non-test file changed.

What the flip changes in behaviour, read from the four places that ask `Writes`:

| Where | Before, under .NET Framework | After |
| --- | --- | --- |
| `RequireCompatible` (one caller: `patch status`, before the early arena) | refused, so no early arena | passes; the arena is allocated while the game runs frames |
| `BuildCheck` | `read_only`, never known or recognised, the "can only read so far" message | the build as it is: known and recognised when the entry's anchors are verified against it |
| `AcceptBuild` | refused, nothing recorded | records the decision, with the runtime |
| the window's build gate (`ReadOnly` in the report) | every cheat marked unusable, nothing asked | the same path as Linux: nothing asked for a recognised build, a question otherwise |

No command gains a write: the fifteen cheats in the catalog and every feature a
command passes to `gameWriting` were already in the entry's `WriteFeatures`
(checked by listing both).

The per-feature mechanism is kept: `WriteFeatures`, `RequireWritable`,
`RequireAnyWrite` and the window's read-only branch. No shipped entry is in that
state now; the next runtime to be ported starts in it.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -race ./...` | 27 packages `ok` on the second run; see the flake below |
| Windows | `golangci-lint-v2.12.2`, project config, `GOTOOLCHAIN=go1.26.0` | 0 issues |
| Windows | `gofmt -l cmd internal` | clean |

The suite was not run on Linux. The mono entry is unchanged.

**A flaky test, not from this change**: `TestATickRunsSeveralRoundsAndStopsOnAResult`
(`internal/service`) failed once in the first full run ("a tick ran a single
round"). Repeated 30 times under `-race` on each tree it failed 0 times with the
change and 1 time on the committed tree: it gives a tick 50 ms to run more than
one round, and under the race detector on this machine one round can take that
long. Left alone.

**Tests changed**:
- `layout`: the table digest; `TestReadsAndWritesAreSeparate` expects both
  entries to write; `TestWritesArePerFeature` checks the mechanism on an entry
  derived from the CLR one with `Writes` false and one write named, checks that
  both shipped entries make writes neither names, and checks that a named write
  is refused under an entry that is not enabled.
- `service`: `stillPorting` swaps the table's CLR entry for the same derived
  one. The four tests that pinned "read-only under netfx" run under it and
  assert what they did. `TestRequireAnyWriteRunsWhereAnEntryWritesSomething`
  also checks `RequireWritable` for a named and an unnamed write.
- `service`, new: `TestDotNetFrameworkIsWrittenUnder` -- under the shipped
  entry the every-write gate passes, the report is not read-only and does not
  carry the read-only message, and a decision is written with the runtime and
  then recognised.

**Mutation checks** -- 11 mutants, all killed: `Writes` put back to false;
`CanWrite` without `Writes`, without `WriteFeatures`, without `Enabled`;
`RequireCompatible` never refusing; `RequireAnyWrite` ignoring `WriteFeatures`;
`ReadOnly` fixed false and fixed true; `BuildCheck` not clearing known and
recognised under a read-only entry; `AcceptBuild` not refusing under one;
`AcceptBuild` dropping the runtime. One mutant was first written in a form that
did not compile and was redone.

## Phase 4: Code quality

- Dead code: none added. `WriteFeatures` on the CLR entry no longer decides
  anything while `Writes` is true; kept as the record of what was ported and
  because `TestEverySiteNamesItsEntrysAnchor` and the derived test entries read
  it.
- The stale "never for writing yet" comment noted in the previous report is
  corrected.

## Phase 5: Security review

- No hardcoded secrets, no new subprocess, no new network call, no new
  dependency (`govulncheck` not re-run: `go.mod` is unchanged).
- Access control: this widens what runs under .NET Framework by three things --
  the early arena allocation in `patch status`, a decision file written by
  `accept-build` under the user's config directory, and the window offering its
  cheats. Each write a cheat makes was already allowed from the CLI. A runtime
  with no entry is refused as before, forced or not (pinned).
- File operations: `accepted-builds.json` is written where it is on Linux, by
  the existing code.

## Phase 5.5: Release safety

- **Change type**: one flag in the version table.
- **Rollback**: set `Writes` back to false in `entry_netfx.go` and restore the
  digest, or revert the commit; rebuild. A recorded decision in
  `accepted-builds.json` is ignored under a read-only entry.
- **Rollout**: nothing runs on its own. The window makes the early arena
  allocation when it is open and the game is running frames.

## Live confirmation (Windows, 2026-10-12)

Done, with the rebuilt CLI against the running game (PID 2324):
- `build-check`: `runtime: netfx-4.8.9345.0 (supported)`, `recognised: true
  known-good: true`, no decision, fifteen cheats resolving. Before the change it
  read `supported, read-only`.
- `patch status`: fifteen cheats listed, none on, `build_verified: true`. This
  command may now allocate the arena; whether it did was not checked.
- `status`: the character, HP 470/470, as before. The game stayed responsive.
- The window (`bin/terrariabonker-gui.exe`, built from this tree) was started
  and stayed up, titled `terrariabonker 0.44.1`. No `accepted-builds.json` was
  written, which is what a recognised build should leave.

Not done:
- What the window shows: whether a build question appears, whether the cheats
  are usable. Not looked at from here.
- Any write from the window, and the three from the previous report: a
  `freeze`/`godmode`, a vein mined through `extract-tick`, the debug `write`.

## Overall

- All gates passed: YES headless. Live: the reports confirmed; the window and
  its writes pending the maintainer.
- Notes: the version in `.tag` is not bumped.
