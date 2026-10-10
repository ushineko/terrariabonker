# Validation Report — CLR projectile reads (auto-fishing, read side)

**Date**: 2026-10-10 15:32
**Commit**: uncommitted (working tree on `main` at d87b6c5)
**Status**: PASSED (lint tool blocked — see Phase 4)
**Scope**: spec 052 — auto-fishing part 1 of 2 (the read side). The projectile
array and a fishing bobber's state now read under .NET Framework. The auto_use
stub and enabling the `catch` command on the CLR are part 2 (they need a live
hook anchor in `Player.Update`).

## What changed

- `layout.ProjectileShape` (new): per-runtime projectile layout as entry data —
  the object-relative offsets of the two float-array references the bite condition
  reads (`ai`, `localAI`) and the two one-byte flags (`active`, `bobber`), how many
  projectiles the array holds, and a resolver name. The szarray offsets (length,
  first element) are the entry's existing `Shapes`. Added `layout.ReadProjectiles`,
  granted to both entries.
- `internal/projectile`: made entry-aware. The readers are now methods on a
  `View` built by `Locate(mem, entry, base)`; the two array-finding strategies
  (`mono`, `clr`) are a registry keyed by the shape's resolver name — no reader
  branches on runtime, mirroring `internal/tiles`. The bobber logic
  (`Reeling`/`Biting`/`Catch`/`Counter`) is unchanged float math. The projectile
  *editor* (`Sweep`) is untouched and still mono-only; it shares the free
  `flagSet` helper.
- `internal/service`: `projectiles()` returns the cached `View` under the running
  entry; `ProjectileArray()` kept for the editor returns `view.Arr()`;
  `projectileBase(entry)` resolves the base per runtime (Main's static block under
  mono, Main.projectile's reference-static slot under the CLR), the same shape as
  `tileBase`. `takeBite`/`tryRecast`/`CatchTick` read through the view.

## CLR projectile model (measured, CLRFields 2026-10-08)

`Main.projectile` is a `Projectile[]` reference static (slot = Main.player −
0x48, `Statics.ProjectileFromPlayer`). A `Projectile` keeps `ai` at +0x40 and
`localAI` at +0x44 (float[] references, three floats at the array's own +0x08),
`active` at +0x102 and `bobber` at +0x104 (one-byte bools). The array holds 1001
projectiles, length at +0x04 and first element at +0x08 (the CLR szarray shape).
Mono's own offsets (ai +0x44, localAI +0x48, active +0x78, bobber +0x88) are
unchanged.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | all packages `ok` |
| Windows, `GOOS=windows` | `go build ./...`, `go vet ./...` (changed pkgs) | clean |

**New test**

- `TestCLRProjectileReads`: a 1001-slot `Projectile[]` planted at the CLR offsets,
  reached through the reference-static slot, with three bobbers in known states
  (waiting, a fish on the line, one already being reeled) and the rest a shared
  inactive filler. `Locate` finds the array by shape; `FindBobbers` returns the
  three, `FindBite` picks the fish and reads its catch (2290). Mono's own
  projectile tests were moved onto the `View` API and still pass unchanged.

**Mutation checks** — all killed (`TestCLRProjectileReads`):
- the CLR active offset set to mono's 0x78 → no bobbers found;
- the CLR ai offset set to localAI's 0x44 → the bite condition misreads.

## Phase 4: Code quality

- `gofmt` clean. `go vet` clean natively and for `GOOS=windows`.
- **`golangci-lint` could not run.** The Go toolchain is now go1.27.0; the pinned
  `golangci-lint` v2.12.2 is a go1.26 build and panics parsing go1.27 source
  (`file requires newer Go version go1.27 (application built with go1.26)`). This
  is an environment mismatch, not a finding in this change. Needs a go1.27-built
  golangci-lint — flagged to the maintainer (tool install/upgrade needs approval).
- The two array resolvers are distinct modules selected by name, per the
  architecture rule; no shared reader branches on runtime.
- Status: PASSED, with the lint tool unavailable noted above.

## Phase 5: Security review

- No dependency changes, no new network calls. Read-only projectile access; the
  write path (auto_use) is part 2.
- Status: PASSED.

## Phase 5.5: Release safety

- **Linux:** unchanged — the mono resolver and shape reproduce the previous
  behaviour, pinned by the existing projectile tests (moved onto the `View` API).
- **Windows:** gains projectile reads (`ReadProjectiles`). No write path added, and
  no command exercises them on the CLR yet, so there is no user-facing behaviour
  change on either runtime until part 2.
- **Rollback:** revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES (lint tool blocked by the go1.27 toolchain; `gofmt` and
  `go vet` clean).
- Version: not bumped.
- **Next:** the auto_use stub on .NET Framework — a per-frame hook at
  `Player.Update`'s entry that sets `controlUseItem` (+0x7e4) and `releaseUseItem`
  (+0x7f1) when armed, then enabling `catch`. The hook anchor is a live JIT
  pattern, so it needs the game running to discover and confirm.
