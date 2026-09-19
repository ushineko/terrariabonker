# Contributing to terrariabonker

Thanks for your interest. This is a personal project, but external pull requests are
welcome. This guide explains how to set up, what the code conventions are, and which
policies are enforced on a PR.

## TL;DR

- Fork, branch, make a focused change, add/adjust headless tests, open a PR.
- Keep it scoped: one logical change per PR; do not reformat unrelated code.
- Your PR must: pass `make test` headless, be `make lint`-clean, contain no
  secrets, and have a commit message with **no AI-attribution / `Co-Authored-By`
  trailers**.
- You do **not** need to follow the maintainer's "Ralph" workflow (spec files,
  validation reports). See "Maintainer workflow" below.

## Scope of the project

A live-memory trainer and item editor for **Terraria 1.4.5.8** on Linux (the Windows
build under Proton/wine-mono). It edits your own single-player game's memory over a sudo
`/proc` path and ships no game assets. Contributions should stay within that scope:
single-player quality-of-life editing and cheats. Anti-cheat evasion, multiplayer/server
exploitation, or anything aimed at other people's games is out of scope and will not be
merged.

## Development setup

```bash
git clone git@github.com:ushineko/terrariabonker.git
cd terrariabonker
make test    # runs headless: no game, no root needed
make lint    # the pinned golangci-lint, downloaded on first use
```

- Go 1.26 or newer. The control panel also needs CGO, OpenGL and X11/Wayland headers;
  `make test` does not.
- The full test suite runs against a synthetic in-memory image (`internal/memtest`), so
  you can develop and test without Terraria installed and without root.
- Runtime memory operations self-elevate via `sudo`; the GUI needs passwordless sudo for
  its memory actions (see the README "Requirements"). You only need that for live
  testing, not for the unit tests.

## Coding conventions

The full conventions live in **`AGENTS.md`** — read it before a non-trivial change. The
essentials:

- **Go**: `gofmt`, and `make lint` clean. Prefer the standard library; ask (open an
  issue/PR discussion) before adding a dependency.
- **Architecture**: game logic goes in the common layer (`internal/service` and the
  packages it uses) so the CLI and GUI stay in sync. The GUI runs unprivileged and shells to
  the CLI under sudo — do not add in-process root memory access to the GUI.
- **Offsets are build-specific (1.4.5.8).** Locate by signature/AOB; never hardcode a JIT
  address, and fail safe (no write) when an anchor is not found.
- **Ported cheats**: keep the FearLess "TerrariaReGrind" attribution for any cheat hook
  derived from that Cheat Engine table.
- **Writing style** (README, docs, PR text): factual and plain — no superlatives or
  marketing language.

## Testing requirements

- Add or update tests in `tests/` for any behavior change. Tests must run **headless**
  (no game, no root) against the synthetic image.
- Test behavioral contracts, not internals — a test that breaks on a pure refactor is
  testing the wrong thing. Avoid over-mocking; exercise the real code path against the
  synthetic memory image.

## Security

- No hardcoded secrets or credentials; none in logs or errors.
- No `eval`/`exec` of dynamic input; spawn subprocesses with explicit argv lists (no
  shell-string interpolation).
- No new network calls without prior discussion.

## Commit and PR conventions

- Conventional-style subjects: `feat(...)`, `fix(...)`, `refactor(...)`, `docs(...)`.
- **Do not** add `Co-Authored-By` trailers or AI-attribution footers ("Generated
  with …") to commit messages or the PR description. PRs containing them will be asked
  to amend.
- Keep the diff surgical: no drive-by reformatting or unrelated refactors.
- Do not bump the version or edit `validation-reports/` in a contributor PR — the
  maintainer handles versioning and release tagging (`vX.Y.Z`).
- Describe what changed and why, and how you tested it (paste the `make test` summary).

## What gets enforced on your PR

| Enforced | Not required from you |
| --- | --- |
| `make test` passes headless | Spec files in `specs/` |
| `make lint` clean | Validation reports |
| No secrets; explicit-argv subprocess | Version bump / release tag |
| No AI-attribution / `Co-Authored-By` in commits | The full "Ralph" phase workflow |
| In-scope, single-player only | |

## Maintainer workflow (context, not a requirement)

The maintainer develops spec-driven ("Ralph" methodology): a spec in `specs/` with
acceptance criteria, then implement → validate → security review → finalize → commit →
tag, with a report in `validation-reports/`. This is the project's internal cadence.
**External contributors are not expected to write specs or validation reports.** Bring a
clean, tested, in-scope change and the maintainer will handle spec/version/release
bookkeeping on merge.
