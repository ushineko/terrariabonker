# Validation Report — the Windows installer (spec 053, Windows half)

**Date**: 2026-10-12 13:00
**Commit**: uncommitted (working tree on `main` at 4134db4, tagged v0.45.0)
**Status**: PASSED headless and by dry run. The wizard has not been run by a
person, and the new CI job has not run yet.
**Scope**: `fynstall.yaml`, the `Makefile`, the build workflow, how the window
finds its CLI, and the documents that describe Windows support. Nothing that
reads or writes the game.

## What changed

- `fynstall.yaml`: one manifest for both platforms (no `targets`), both
  programs by name, a launcher entry with the fields the checked-in desktop
  entry has, and the config directory kept on uninstall.
- `Makefile`: `.exe` on Windows for `build` and `build-gui`; `install-fynstall`
  (fynstall pinned at v0.2.2) and `installer`.
- `.github/workflows/build.yml`: a Windows job, its installers attached to a
  release, one `SHA256SUMS` over everything attached.
- `internal/gui`: `findCLI` — the CLI beside the window, then `PATH`'s.
- `internal/buildinfo`: a test holding `fynstall.yaml`'s version to `.tag`.
- README, AGENTS.md, CONTRIBUTING.md, CLAUDE.md, the PR template: Windows.
- Spec 053, with the Linux half written down and not started.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11, go1.27.2 | `go test -race ./...` | 28 packages `ok` |
| Windows | `golangci-lint-v2.12.2`, project config, `GOTOOLCHAIN=go1.26.0`, `./...` | 0 issues |
| Windows | `gofmt -l cmd internal` | clean |

Not run on Linux.

**New tests**
- `internal/gui/findcli_test.go`: with a CLI beside the window and another on
  `PATH`, the neighbour is driven; with none beside it, `PATH`'s; a window that
  cannot name its own file falls back to `PATH`; with neither, nothing. Real
  files and the real `exec.LookPath`.
- `internal/buildinfo/release_test.go`: `fynstall.yaml`'s `app.version` equals
  `.tag`.

**Mutation checks** — 4 mutants, 4 killed: `PATH` asked first; the neighbour
never found; no fallback to `PATH`; the manifest left at 0.44.1.

## The installers, built and run (Windows 11)

Built in a copy of the working tree, because the maintainer's panel and its
worker were running from `bin\` and Windows will not replace a running
program's file. `make installer COMMIT=4134db4`, MSYS2 make and gcc:

- `go install …/fynstall@v0.2.2` reported `fynstall 0.2.2`.
- `fynstall validate`: valid.
- `dist\`: `terrariabonker-0.45.0-windows-amd64-installer.exe` (86.4 MB),
  `…-cli-installer.exe` (42.6 MB), and an uninstaller for each.
- Both installers answer `--version` with `TerrariaBonker 0.45.0 installer
  (fynstall 0.2.2)`; the binaries inside the tree say `0.45.0 (4134db4)`.
- The command-line installer, `--dry-run --yes`, exit 0: "Upgrades
  TerrariaBonker 0.44.0 to 0.45.0", a per-user install of 5 files in
  `%LOCALAPPDATA%\Programs\io.ushineko.terrariabonker`, the Start Menu
  shortcut, the `HKCU` Uninstall key, the `PATH` entry. Nothing was changed:
  the installed CLI still reports 0.44.0.

Before the `Makefile` change, `make build build-gui` on Windows wrote
`bin/terrariabonker` and `bin/terrariabonker-gui` with no suffix (observed).

Not done:
- The wizard, an actual install or upgrade, the launch from its last page, an
  uninstall. Each replaces the maintainer's installed copy.
- The Windows CI job. It assumes the runner image has `make`; that is checked
  by the first run after the push.
- Anything on Linux: the manifest is built there for the first time by the
  Linux half of the spec.

## Phase 4: Code quality

- `findCLI` takes the two lookups as arguments, so the test drives the real
  `exec.LookPath`; no platform switch, because `LookPath` already knows what a
  program is called on each.
- No dead code, no duplication found. `install.sh` and the release tarball are
  untouched.

## Phase 5: Security review

- No hardcoded secrets, no new network call in either binary, no new
  dependency (`go.mod` unchanged; `govulncheck` v1.9.0 found nothing at
  4134db4, the same module graph).
- The build now fetches a tool: `go install github.com/ushineko/fynstall/cmd/fynstall@v0.2.2`,
  a pinned version through the Go module proxy and checksum database.
- `findCLI` runs the program beside the window in preference to `PATH`'s. On
  Windows both are the user's own files. On Linux the CLI is run under sudo,
  so which file that is matters; the window's own directory is no more
  writable than a `PATH` entry in the user's home, and the Linux half of the
  spec is to measure the sudo path for each way of installing.
- The installers are unsigned. The README says so.
- A per-user install on Windows needs no elevation and the tool needs none.

## Phase 5.5: Release safety

- **Change type**: packaging and build tooling; one window behaviour change
  (which CLI is driven when two exist).
- **Rollback**: revert the commit. An installed copy is removed by its own
  uninstaller; `--cli` overrides `findCLI`.
- **Rollout**: the installers reach a release at the next tag. `.tag` is not
  bumped by this change.

## Overall

- All gates passed: YES headless, with the wizard and CI recorded as not run.
- Notes: v0.45.0 was tagged before this change and its release carries no
  Windows installer.
