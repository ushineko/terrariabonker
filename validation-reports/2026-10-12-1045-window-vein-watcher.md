# Validation Report — the window watches for veins (spec 052, slice 29)

**Date**: 2026-10-12 10:45
**Commit**: uncommitted (working tree on `main` at 1ba5501)
**Status**: PASSED headless. The mechanism is confirmed live from the CLI; a
vein taken through the window has not been seen yet.
**Scope**: the Go window sends `extract-tick` while the Ore extractor patch is
on. A window change on both platforms.

## What was wrong

Found by the maintainer in play on Windows: with Ore extractor on, tin broke one
tile at a time. `internal/gui` held six worker loops (potions, fishing, buffs,
catch, sell, projectiles) and none for veins; nothing in the package named
`extract-tick`. The patch gives the game a queue to mine from and the window
never filled it. Spec 050 lists "vein/extract" in its phase 2 and is marked
complete. The CLI side existed: `extract-tick`, `extract-stop`, both accepted by
the worker.

Two earlier statements were wrong and are corrected in place: spec 052 slice 24
and the reports of 2026-10-11 22:45 and 2026-10-12 00:35 called `extract-tick`
"the window's path".

## What changed

- `internal/gui/client`: `ExtractTickArgv(gems)` and `ExtractStopArgv()`, both
  in `Samples()`.
- `internal/gui`: a `veins` watch, 100 ms, with `extract-stop` on the way down,
  halted with the others at shutdown. `followOreExtractor` sets it from the
  patch's state each time the patches are read back. A "Vein mining" card in
  Effects with one control, "Take gems too", saved with the window's settings;
  changing it while the watcher is up restarts the watcher, because the one in
  the worker was built with the old answer.
- No switch for the watcher itself: it is up exactly while the patch is on.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -race ./internal/gui/... ./internal/cli` | `ok` |
| Windows | `golangci-lint-v2.12.2`, project config, `GOTOOLCHAIN=go1.26.0`, `./...` | 0 issues |
| Windows | `gofmt -l cmd internal` | clean |

Not run on Linux. The full suite was last run on the previous commit; this
change touches `internal/gui` only, and `internal/cli` is run because its parity
test parses every argv the window builds.

**New tests** (`internal/gui/veins_test.go`): the watcher follows the patch --
down before anything is read, down with another patch on, up with the extractor
on, down when it goes off or the patches become unreadable; what it sends is the
gems box as it stands, and `extract-stop` on the way down; the gems box is
remembered; the Effects section builds with the card in either state. The two
existing parity tests cover the new argvs against the real CLI parser.

**Mutation checks** -- 11 mutants, 10 killed: the watcher always up, never set,
up when any patch is read, following a different patch; the tick ignoring the
gems box; the wrong stop command; `--gems` inverted; a different subcommand; the
gems box not saved, not restored.

- 1 survived: removing the call to `followOreExtractor` from `loadPatches`. No
  headless test drives `loadPatches`, which runs the CLI; the package has no
  stand-in for it. That call is the wiring this change exists for, so it is
  covered only by the live check below, which is not done.

## Phase 4: Code quality

- The watch is the existing `watch` type used as the other six are; nothing new
  in the mechanism.
- No dead code, no duplication found.

## Phase 5: Security review

- No hardcoded secrets, no new network call, no new dependency (`govulncheck`
  not re-run: `go.mod` is unchanged). No new subprocess: the tick goes through
  the existing worker, as an explicit argv built in `client`.
- This makes the window write to the world without a click, once the player has
  switched Ore extractor on: breaking a whitelisted tile mines its vein. That is
  what the switch's label says it does. Gems stay off unless asked for.

## Phase 5.5: Release safety

- **Change type**: code-only, in the window.
- **Rollback**: revert the commit and rebuild; or switch Ore extractor off,
  which stops the watcher and disarms.
- **Rollout**: the watcher starts with the window when the patch is already on.

## Live confirmation (Windows, 2026-10-12)

Done:
- The mechanism, from the CLI with the maintainer mining: `extract --watch
  --rounds 20000` reported `25 of 25 tiles mined at (1588, 343)`, `13 of 13 at
  (1565, 332)`, `29 of 29 at (1613, 319)`, each at a median 0.02 s a tile -- 67
  tiles over three veins -- and exited 0 on its round limit. The maintainer
  confirmed the veins going in game.
- The rebuilt window was started with `--cli` pointing at the rebuilt CLI, its
  worker is that binary, and `patch status` reports `ore_extract` on.

Not done:
- A vein taken through the window. Whether the watcher armed in the worker
  cannot be seen from outside it.
- Anything on Linux.

## Overall

- All gates passed: YES headless, with one recorded survivor. Live: pending the
  maintainer's try in the window.
- Notes: the version in `.tag` is not bumped.

## Release as 0.45.0 (added 2026-10-12, at the commit)

- The maintainer reported the port confirmed working in play on Windows,
  the window's vein watcher included. That closes the "Not done" item above for
  Windows and the surviving mutant's live check; it is the maintainer's
  observation, not one made from the build side. Linux is still not run.
- Spec 052 is marked COMPLETE on that report. Its phase 5 (packaging, the
  README's Windows requirements) moves to spec 053.
- `.tag` and `fynstall.yaml` are 0.45.0, confirmed by the maintainer.
- Before the tag, on Windows 11, go1.27.2: `go test -race ./...` passed;
  `golangci-lint-v2.12.2` with the project config and `GOTOOLCHAIN=go1.26.0`
  reported 0 issues; `govulncheck ./...` (v1.9.0, database of 2026-10-08) found no vulnerabilities.
