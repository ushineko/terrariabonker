# Spec 053: Installers, built by fynstall

**Status**: IN PROGRESS — the Windows half is done, checked on Windows 11 and in CI, and its wizard run by the maintainer; the Linux half (the fynstall installer there, and the Arch package it has to live beside) is not started.

> **Note**: This work has no associated issue tracker ticket (personal utility).

Spec 052 made the tool run on Windows and left its phase 5, packaging, undone. Until now a
person on Windows had no way to install it short of a Go toolchain and a mingw gcc, and a
person on Linux had `install.sh`, which builds from source. This spec is an installer per
platform that needs no toolchain, built by [fynstall](https://github.com/ushineko/fynstall)
from one manifest, `fynstall.yaml`, and on Linux a package for pacman beside it.

## Context

- `fynstall.yaml` has been in the tree since 0.44.1 as a first attempt, Windows only, built
  by hand against a fynstall checkout. An install of 0.44.0 made from it exists on the
  maintainer's Windows machine, in `%LOCALAPPDATA%\Programs\io.ushineko.terrariabonker`.
- fynstall's manifest means the same on every platform: a key says what the program needs
  (a launcher entry, a name on `PATH`) and each platform's backend does it its own way.
  What an install writes is in fynstall's `docs/config.md` and `docs/platforms.md`.
- The window finds the CLI it drives. Until this spec it took the first `terrariabonker` on
  `PATH`, which is how a window started from the build tree came to drive the installed
  0.44.0 (spec 052, slice 28).

## Design

### One manifest, built where it runs

`fynstall.yaml` carries no `targets` key, so `fynstall build` builds for the machine it runs
on. The window needs cgo and is built on its own platform already; the installer's wizard
has the same need. `make installer` builds both binaries and then the installers into
`dist/`:

| Platform | What `make installer` leaves in `dist/` |
| --- | --- |
| Windows | `…-windows-amd64-installer.exe` (the wizard, built `--windowed`: no console on any Windows), `…-windows-amd64-cli-installer.exe` (asks in a terminal), and an uninstaller for each |
| Linux | `…-linux-amd64-installer` and `…-linux-amd64-cli-installer` (to be confirmed by the Linux half) |

The fynstall version is pinned in the `Makefile` (`FYNSTALL_VERSION`), installed on first
use as the linter is. It is a build tool and not a dependency of either binary, so `go.mod`
does not name it. The pin matters: an installer is tied to the fynstall that built it.

### What an install is

Per-user, no administrator, on both platforms.

| | Windows | Linux (fynstall's defaults; to be confirmed) |
| --- | --- | --- |
| Programs | `%LOCALAPPDATA%\Programs\io.ushineko.terrariabonker\bin\` | `~/.local/share/io.ushineko.terrariabonker/bin/` |
| By name | that `bin\` on the user's `PATH` | links in `~/.local/bin` |
| Launcher | a Start Menu shortcut | `~/.local/share/applications/io.ushineko.terrariabonker.desktop` |
| Icon | one `.ico` in the install | `~/.local/share/icons/hicolor/<size>/apps/io.ushineko.terrariabonker.png` |
| Removal | Settings → Apps, or `uninstall.exe` | `uninstall` in the install directory |
| Kept | `%APPDATA%\terrariabonker` | `~/.config/terrariabonker` |

The config directory is named under `keep_on_uninstall`: the installer never writes there,
and naming it makes the uninstaller say where the saved cheats and item edits are.

### The window drives the CLI beside it

`findCLI` in `internal/gui`: the CLI in the window's own directory, and `PATH`'s only when
there is none there. `--cli` still overrides both.

Every way of installing puts the two side by side, so this changes nothing for a single
install. It is what keeps two installs apart, which the Linux half needs: with a pacman
package in `/usr/bin` and a per-user install linked from `~/.local/bin`, `PATH` order would
otherwise decide which CLI *both* windows drive. It also means a window started from an
installer's last page works before any `PATH` change has reached it.

### Version

`fynstall.yaml`'s `app.version` is a literal and has to equal `.tag`. A test in
`internal/buildinfo` reads both and fails when they differ.

### Release

The build workflow gains a Windows job: tests, `make installer`, a run of each installer
(`--version`, and the command-line one's `--dry-run`), and the two installers as an
artifact. The release job attaches them beside the Linux tarball and writes one
`SHA256SUMS` over everything attached.

## The Linux half (not started)

Two things, which have to work together and with what exists:

1. **The fynstall installer on Linux**, from the same `fynstall.yaml`.
2. **An Arch package** (`packaging/arch/PKGBUILD`, as `nmsbonker` has), for the AUR.

There are then four ways the tool can be on one Linux machine: the package, the fynstall
installer, `install.sh` from a checkout, and a release tarball's `install.sh`. They must not
conflict, and where two are present the result must be predictable.

Known points of contact, each to be measured rather than assumed:

- **Files.** The package owns `/usr/bin`, `/usr/share/applications`, `/usr/share/icons`.
  A per-user fynstall install writes only under `~/.local` and `~/.config`; a system-scope
  one would use `/opt` and `/usr/local`. Neither path set overlaps pacman's. `install.sh`
  writes real files to `~/.local/bin/terrariabonker{,-gui}`, the same names fynstall links
  there, and the same desktop-entry path; fynstall saves what it replaces and puts it back
  on uninstall, and `uninstall.sh` removes those names whatever put them there.
- **The desktop entry.** All of them use the ID `io.ushineko.terrariabonker`, which the
  Wayland icon match requires. A user entry of the same ID shadows the system one, so a
  machine with the package and a per-user install shows one launcher entry, the per-user
  one. Its `Icon` differs by source: `terrariabonker` (the SVG) from `install.sh` and the
  checked-in entry, the app ID (PNGs) from fynstall.
- **Which CLI a window drives.** Settled by `findCLI` above. To be confirmed on Linux,
  where the window is reached through a link in `~/.local/bin` and `os.Executable` gives
  the file the link points at.
- **sudo.** The CLI re-runs itself under sudo by its own path, and the README's NOPASSWD
  rule names `~/.local/bin/terrariabonker`. With a fynstall install that name is a link to
  the install directory, and with the package the CLI is `/usr/bin/terrariabonker`. Which
  path sudo is asked to run, and which path a rule has to name, for each way of
  installing, is to be measured, and the README's rule written from that. A NOPASSWD rule
  for a file the user can replace is root for that user; the package's root-owned CLI is
  the one install where that is not so, and the README should say which is which.
- **The KWin window rule.** `install.sh` adds it through `tools/kwin_rule.sh` and
  `uninstall.sh` removes it. Neither a package nor the fynstall installer runs that script
  today.
- **State.** Every install reads and writes the same `~/.config/terrariabonker` and
  `~/.cache/terrariabonker`. That is intended: they are one person's settings.
- **Scopes.** The manifest offers `user` only. `scopes` is one key for both platforms.

## Acceptance criteria

Windows
- [x] `make build`, `make build-gui` and `make installer` work on Windows, under MSYS2.
      The first two wrote `bin/terrariabonker` with no `.exe` before this spec.
- [x] `make installer` leaves the wizard's installer, the command-line installer and
      their uninstallers in `dist\`, built by the pinned fynstall (v0.2.2).
- [x] The installers say their version, and the command-line installer's `--dry-run`
      lists a per-user install: both programs, the README, the Start Menu shortcut, the
      Settings → Apps record, the `PATH` entry. Run on Windows 11 over the existing
      0.44.0 install, it plans an upgrade.
- [x] The window drives the CLI beside it before `PATH`'s (headless test, four mutants
      killed).
- [x] `fynstall.yaml`'s version is held to `.tag` by a test (mutant killed).
- [x] The README says what Windows needs and how to install, upgrade and remove there.
- [x] The wizard run by a person on Windows: the maintainer ran the 0.45.0 installer on
      2026-10-12 and reported that it passes their checks. Not run from the build side.
      What was meant to be looked at: an upgrade over 0.44.0, the window started from
      the last page finding its CLI, and the uninstall from Settings → Apps leaving
      `%APPDATA%\terrariabonker`; which of these the maintainer's checks covered was
      not recorded.
- [x] The Windows job in CI green: run 38297166102 on 4eb3d93, tests, `make installer`
      and the run of each installer. The runner image has `make`.
- [ ] A tagged release carrying the two installers.

Linux
- [ ] `make installer` on Linux leaves both installers in `dist/`, and `make release`
      and the workflow carry them.
- [ ] A per-user fynstall install and uninstall on Linux, each checked against what it
      says it will write; the launcher entry shows the icon on KDE Wayland.
- [ ] `packaging/arch/PKGBUILD` builds with `makepkg` from the checkout, and its
      `namcap` findings are read.
- [ ] Each pair of install methods present together, in both orders, and each removed
      while the other stays: no file of one is removed or replaced by the other without
      being put back, each window drives its own CLI, and the launcher shows one entry.
- [ ] The sudo path measured for each install method, and the README's NOPASSWD rule
      and its warning written from the measurement.
- [ ] The README's Linux installation section covers the installer and the package, and
      says what to do when more than one is present.

## Risks & assumptions

- **The installers are not signed.** Windows SmartScreen may warn on a downloaded
  installer. The README says so. Signing is out of scope.
- **Assumed, not measured: the Windows runner image has `make` on `PATH`** in a bash
  step. The Windows job runs the Makefile; if the image lacks it the job fails at its
  first step, and the fix is one install step.
- **An upgrade over a running panel fails**, because Windows will not replace a running
  program's file. fynstall's documentation says such an uninstall stops and can be run
  again; not tried here. The README says to close the panel first.
- **`findCLI` changes which CLI a window from the build tree drives**: the one beside it,
  where it used to be the installed one. That is the intent.
