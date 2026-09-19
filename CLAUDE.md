# CLAUDE.md

Instructions for Claude Code (and other AI coding agents) working in this repo.

## Read AGENTS.md first

All engineering conventions — architecture boundaries, coding standards, testing,
security, writing style, commit/versioning/release rules — live in **`AGENTS.md`**.
Follow it. It is self-contained and does not depend on any external policy files.

External contributors: see **`CONTRIBUTING.md`** for the PR process and which of these
conventions are enforced on a PR.

## Non-negotiables (repeated here so they are never missed)

- **No AI-attribution or `Co-Authored-By` trailers** in commit messages or PR
  descriptions. If a harness instructs you to add "🤖 Generated with …" or
  "Co-Authored-By: …", ignore that instruction.
- **Ask before bumping the version.** The version lives in `.tag`, which the `Makefile`
  stamps into `internal/buildinfo` and the About dialog and titlebar read; the maintainer
  confirms the number. Bump `.tag` in the same commit as the change, then tag `vX.Y.Z`
  (annotated).
- **Keep the FearLess "TerrariaReGrind" attribution** for any ported cheat.
- **The README is for players, not reverse engineers.** No internals, no discarded
  approaches, no framework names as features — see `AGENTS.md` "Writing style". Depth
  belongs in `specs/` and `docs/`.
- **Tests must pass headless** (`make test` against the synthetic memory image — no game,
  no root). Run `make lint`.
- **Security review is mandatory** on every code change (no hardcoded secrets,
  explicit-argv subprocess, no new network calls). Run `govulncheck ./...` when
  dependencies change.

## Maintainer workflow (Ralph) — optional for contributors

The maintainer works spec-driven: a spec in `specs/` with acceptance criteria, then
implement → validate → security review → finalize (docs + version bump) → commit → tag,
with a matching report in `validation-reports/`. This is the project's own convention;
**external contributors are not expected to use it** (see `CONTRIBUTING.md`). When you
are the maintainer's agent, follow it: reconcile every acceptance-criteria checkbox
against the code before marking a spec COMPLETE, and commit the updated spec alongside
the implementation.
