# Validation Report — the life caps, named by the runtime

**Date**: 2026-10-08
**Commit**: uncommitted (working tree on `main` at 3e01e78)
**Status**: PASSED
**Scope**: `statLifeMax` and `statLifeMax2` had been named the wrong way round in
`internal/layout` since the project began. Measured with `cmd/monofields` against the
live Linux game: `statLifeMax` (the permanent cap) at 0x730, statLife − 8;
`statLifeMax2` (the cap in effect) at 0x734, statLife − 4. The constants, their
callers, the mono entry's shape and the tests that pinned the swap are corrected, and
`monofields --verify` now checks all six life and mana fields.

## How it was found

The stat-write slice of spec 052 needed the CLR player handle to heal to full. A test
of that failed against the CLR's runtime-reported names, which put `statLifeMax` first.
The game's metadata declares the six fields as `statLifeMax, statLifeMax2, statLife,
statMana, statManaMax, statManaMax2` (RIDs 1871–1876), and both runtimes keep 4-byte
fields in declaration order (measured on Item and Projectile). The mono constants said
`Max2` first. `monofields` settled it.

The swap began in `docs/discovery.md`'s first dump, where both caps read 100 and the
labels `max2 max` were a guess nothing could contradict. The comments beside the
constants ("the permanent cap", "the cap in effect") were right; the names were wrong.

## What it broke, and what changes

| Where | Before | After |
| --- | --- | --- |
| `locate.ValidBlock` / scan | took the permanent cap for the boosted one: **a player whose cap in effect was above the permanent one (Lifeforce, max-life accessories) was never found**; the prefilter also dropped any cap in effect above 500 | finds them (the mono entry's shape says `statLifeMax` first, as measured) |
| `player.ManaFull`, the mana freeze | filled to `statManaMax`, the permanent cap — short under a mana boost | fills to `statManaMax2`, the cap in effect |
| heal to full, godmode, `status` max HP | used the cap in effect through the swapped name | unchanged: still the cap in effect, now through the right name |
| spec 052, `docs/discovery.md` | recorded the CLR as storing the caps in the "opposite order" | corrected: same order on both runtimes; the "difference" was the swapped names |

The prefilter fix committed earlier the same day (c107140) was only effective in its
test fixture, which was planted with the same swap; with this change it is effective on
the real game.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -count=1 ./...` | 27 packages `ok` |
| Linux (njv-cachyos), fresh clone at 3e01e78 + this tree | `make test` | 27 packages `ok` |
| Linux, live game | `monofields --verify` (with the six new fields) | all agree: 0x730, 0x734, 0x738, 0x73C, 0x740, 0x744 |
| Linux, live game, in a world, unbuffed | `status`, installed v0.42.0 and this tree | identical: 2 copies, "terrariabonker", HP 470/470, mana 200/200 |
| Linux, live game, a copy put in the buffed state | wrote `statLifeMax2` = 564 (permanent 470) into both copies of the character, read-only otherwise; the live copy reset to 470 within 2 s (the game recomputes it each frame), the load-time snapshot held 564 | **installed v0.42.0: 1 copy** (the buffed one rejected); **this tree: 2 copies**. The original value was then written back and read back as 470; the installed build found both copies again |

- `TestABuffedMonoPlayerIsFound` (new): two players planted in measured storage order,
  one buffed (400 permanent, 480 in effect), one with a cap in effect of 600. Failed
  against the old code; passes.
- Corrected to the measurement, each having pinned the swap: `TestFindPlayers` (its
  buffed copy was planted swapped this morning), the golden offsets in
  `layout_test.go`, the mono shape, `player`'s field-reading test, the `mana_full`
  write digest (the intended change), and the two CLR tests' displayed max HP (the cap
  in effect, as `status` has always shown under mono).
- `TestABuffedPlayerIsHeldAtTheCapsInEffect` (new, trainer): godmode and the mana
  freeze hold the caps in effect.

**Mutation checks**

| Mutant | Result |
| --- | --- |
| mono shape back to the swapped order | killed |
| mana fills to the permanent cap | killed |
| life fills to the permanent cap | killed |
| `status` shows the permanent cap | killed |
| godmode holds the permanent cap | **survived at first** — the trainer's tests planted equal caps. Buffed test added; killed |
| mana freeze holds the permanent cap | **survived at first**, same reason; killed |

## Phase 4: Code quality

- `make lint`: 0 issues. `GOOS=windows` lint of every non-GUI package: 0 issues.
- Status: PASSED.

## Phase 5: Security review

- `govulncheck`: no vulnerabilities found. No dependency changes.
- No write path added. Writes now target the fields their names say.
- Status: PASSED.

## Phase 5.5: Release safety

- Behaviour changes on Linux, all fixes: a buffed player is found; mana-to-full and
  the mana freeze reach the cap in effect. Unbuffed play is unchanged, checked on the
  live game.
- Rollback: revert the commit.
- Status: PASSED.

## Overall

- All gates passed: YES
- Version: not bumped (the maintainer's call; user-visible fixes on Linux).
