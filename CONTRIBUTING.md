<!-- Generated from the shared contributing policy. Edit the template and the project fragments, then re-render; do not hand-edit this file. -->

# Contributing to terrariabonker

Thanks for your interest. This is a personal project, and external pull requests
are welcome. This guide states the policy a PR is held to, and what the
maintainers will do with it.

## TL;DR

- Only elected maintainers merge. See [MAINTAINERS.md](MAINTAINERS.md).
- AI-written and hand-written PRs are both accepted, and both get the same
  review. Either may be rejected if it does not meet the standards below.
- Every PR needs tests **and**, for anything touching hardware or device
  support, an end-to-end reading from a real system — pasted into the PR.
  A PR without that collateral may be rejected.
- Keep the diff scoped: one logical change, no drive-by reformatting.
- No `Co-Authored-By` trailers and no AI-attribution footers in commits or the
  PR description.

## Who merges

Merge rights belong to the maintainers listed in
[MAINTAINERS.md](MAINTAINERS.md), and to nobody else. No contributor — human or
agent — merges their own PR, and no PR lands without a maintainer's approving
review.

Maintainers are elected, not self-appointed. The process, the current roster,
and how to be considered are all in [MAINTAINERS.md](MAINTAINERS.md).

A maintainer may merge a PR as-is, modify it before merging, hold it pending
changes, or close it. Closing is not a judgement of the contributor; it most
often means the change does not fit the project's direction, and that is the
maintainer's call to make.

## AI-written contributions

Code written with an AI assistant is fine. It is reviewed exactly like
hand-written code, held to the same standards, and modified by maintainers where
needed.

One rule makes that workable: **you must understand what you submitted.** If you
cannot explain what the change does, how it behaves at the edges, and why it
fits the existing design, the PR will be closed. Review questions go to the
person who opened the PR, not back to a model.

Specifically, a PR is likely to be rejected if it:

- adds a plausible-looking abstraction the project did not ask for,
- restates existing behaviour in new words without changing it,
- carries generated commentary, diagnosis dumps, or implementation-plan prose
  in the diff or the PR body,
- reformats or "improves" code outside the change,
- or claims a test or an e2e reading that was not actually run.

Do not add `Co-Authored-By` trailers or "Generated with …" footers to commits or
to the PR description. A PR containing them will be asked to amend.

## Tests and e2e collateral

Two things are required, and they are separate requirements.

**1. Tests.** Any behaviour change adds or updates tests. Tests encode
behavioural contracts — what the code does — not internals. A test that breaks
on a pure refactor with no behaviour change is testing the wrong thing. Paste
the test run summary into the PR.

**2. An e2e reading from a real system.**

terrariabonker edits a running game's memory, and its anchors are specific to
one game build. The synthetic memory image the tests run against will agree
with a signature that no longer matches the real binary. Any change to an
offset, a signature, a cheat hook, or the memory write path needs a reading
from a real running game.

Paste, at minimum:

- the Terraria build and the Proton / wine-mono version;
- the `terrariabonker` command you ran and its real output, including the
  anchor or signature resolution step;
- **confirmation the effect was observed in game**, not merely that a write
  returned success;
- for a new or changed signature: evidence it resolved, and evidence it fails
  safe (no write) when the anchor is not found;
- for the GUI: confirmation the unprivileged GUI reached the effect through the
  CLI under sudo, since the GUI must never gain in-process root memory access.

A reading against the synthetic image (`internal/memtest`) is a test, not an
e2e reading. Single-player only.

Report what you actually observed. Describe the hardware, the OS and the
software versions involved, the command you ran, and its real output. "Works on
my machine" is not a reading. If a reading cannot be taken — no access to the
device, platform not available to you — say so plainly in the PR and say what
*was* verified; a maintainer will decide whether to take the reading or hold the
PR. Claiming a reading that was not taken is the one thing that will get a
contributor's future PRs declined on sight.

## Scope

A live-memory trainer and item editor for Terraria on Linux (the Windows build
under Proton/wine-mono). It edits your own single-player game's memory over a
sudo `/proc` path and ships no game assets.

In scope: single-player quality-of-life editing and cheats. Out of scope, and
will not be merged: anti-cheat evasion, multiplayer or server exploitation, and
anything aimed at other people's games.

Architecture constraints that are not negotiable in a PR:

- Game logic lives in the common layer (`internal/service` and what it uses) so
  the CLI and the GUI stay in step.
- The GUI runs unprivileged and shells to the CLI under sudo. Do not add
  in-process root memory access to the GUI.
- Offsets are build-specific. Locate by signature/AOB, never hardcode a JIT
  address, and fail safe — no write — when an anchor is not found.
- Keep the FearLess "TerrariaReGrind" attribution on any cheat hook derived
  from that Cheat Engine table.

Ported cheats keep their upstream attribution. The full conventions are in
[AGENTS.md](AGENTS.md); read it before a non-trivial change.

Changes that alter the project's direction — the interaction model, the
architecture, persistence formats, or the public interface — start as a GitHub
Discussion, not as a PR. A PR that changes direction without prior alignment
will likely be closed regardless of its quality.

## Development setup

```bash
git clone git@github.com:ushineko/terrariabonker.git
cd terrariabonker
make test         # headless: no game, no root needed
make lint         # the pinned golangci-lint, downloaded on first use
make build        # CLI
make build-gui    # control panel (needs CGO, OpenGL and X11/Wayland headers)
```

Go 1.26 or newer. The full test suite runs against a synthetic in-memory image
(`internal/memtest`), so you can develop and test with no Terraria installed
and no root. Runtime memory operations self-elevate via `sudo`; the GUI needs
passwordless sudo for its memory actions (see the README "Requirements"). That
is only needed for live testing, not for the tests.

## What gets checked on your PR

| Required from you | Not required from you |
| --- | --- |
| `make test` passes headless | Spec files in `specs/` |
| `make lint` is clean | Validation reports |
| Tests for the behaviour you changed | Version bump or release tag |
| An e2e reading from a real running game | `validation-reports/` edits |
| Explicit-argv subprocesses; no shell-string interpolation | |
| In scope: single-player only | |
| No secrets in code, logs, or pasted output | |
| No `Co-Authored-By` / AI-attribution trailers | |

The maintainer's own workflow (spec files under `specs/`, validation reports,
release tagging) is internal cadence. **External contributors are not expected
to write specs or validation reports, bump versions, or tag releases.** Bring a
clean, tested, in-scope change with its e2e reading and the maintainers handle
the bookkeeping on merge.

## Security and dependencies

- No hardcoded secrets or credentials, and none in logs or error messages.
- No `eval`/`exec` of dynamic input. Spawn subprocesses with explicit argument
  lists, never by interpolating into a shell string.
- No new network calls without prior discussion.
- Prefer the standard library. Open a Discussion before adding a dependency;
  a new third-party module in a PR is a decision for the maintainers, not a
  detail of the change.

## Commit and PR conventions

- Conventional-style subjects: `feat(...)`, `fix(...)`, `refactor(...)`,
  `docs(...)`, `test(...)`.
- One logical change per PR. Split unrelated work.
- No secrets or credentials, in code, in logs, in error messages, or in pasted
  e2e output. Redact serial numbers and hostnames if you would rather not
  publish them.
- Reference a related issue in the commit body with `refs #<number>`.
- Describe what changed, why, and how it was verified.

## Questions

Open a GitHub Discussion. Issues are for reproducible bug reports and
maintainer-created work items.

---

This policy is shared across the project author's public repositories; the
canonical copy lives in a private sysadmin repository and is rendered into each
project. Project-specific sections (scope, setup, checks, e2e) differ per repo;
the governance sections do not.
