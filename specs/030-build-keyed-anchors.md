# Spec 030: Build-keyed anchor ledger, multi-site patches, and honest cheat status

**Status**: COMPLETE
**Implementation Date**: 2026-08-23

> **Note**: No issue tracker ticket (personal utility).

## Context

Three cheats (`mining`, `reach`, `max_minions`) stopped applying. The reported reason was
wrong in two different ways, and both are worth fixing.

**What actually happened.** `Player.ResetEffects` is JIT'd **twice** in the running process —
one copy in the 16.5 MB JIT arena, one in a 320 KB arena, byte-identical for 256 bytes (later
differences are just relative call displacements). The `reset_block` and `reset_minions`
anchors therefore match twice, and `Patcher._resolve` requires a unique match:

```
reset_block      2 hits   0x1715981f, 0x1edc0c67   -> mining, reach
reset_minions    2 hits   0x17159159, 0x1edc05a1   -> max_minions
place / getranges / get_spawn_rate / player_teleport   1 hit each
```

The six working cheats are unaffected because injections resolve with `_resolve_all`, which
already tolerates several sites (`loot` legitimately matches 4).

**Why it was misdiagnosed.** `_resolve` raises `"anchor 'X' is not unique (game updated?
re-derive AOBs with CE)"`. Terraria *had* just rebuilt (buildid 24825745 → 24893155), so the
message looked authoritative — but 7 of 9 anchors still match on the new build with identical
field displacements. The message blames a cause it has not checked.

**What the UI did with it.** `restore` returned these as `pending`, the GUI retried on a
timer, and each pass appended another `[auto-restore]` line. There was no indication that a
cheat was unavailable, no reason, and no distinction between "not JIT'd yet, will retry" and
"cannot resolve, will never succeed".

## Requirements

1. A byte-patch cheat whose anchor matches several identical sites applies to **all** of
   them, so the cheat works regardless of which JIT copy executes. Disabling reverts all.
2. Anchors record which build they were **verified** on. The running build is identified by
   `version+buildid`; a cheat that resolves on an unverified build still works, but says so.
3. Failure reasons are specific and truthful: "matched N sites", "not found", "not yet
   compiled", never a guess about the cause.
4. The GUI shows cheat availability in the UI itself — not by repeating log lines — and shows
   the build id next to the version.
5. Auto-restore stops retrying what cannot succeed, and reports once rather than per attempt.

### Technical

6. **Build key.** `version.build_key(version, buildid)` → `"1.4.5.7+24893155"`, with the
   verified key(s) declared in `version.py` next to `KNOWN_VERSION`/`KNOWN_BUILDID`.
7. **Anchor ledger.** `ANCHORS` entries become `Anchor(pattern, verified=frozenset({...}))`.
   The byte patterns stay in `patcher.py` next to the comments that explain how each was
   derived; the ledger only adds provenance. Unknown build keys are not an error.
8. **Resolution result.** `Patcher.resolve(anchor_key)` returns a `Resolution(sites, ok,
   reason, verified)` instead of raising for the ambiguous case. `Cheat` patches every site;
   the per-pid state records the full site list so disable reverts each one. `unique=True`
   stays available per anchor for a site where patching a twin would be harmful.
9. **Status contract.** `patch status --json` gains, per cheat: `available` (bool),
   `verified` (bool), `reason` (str, empty when available), `sites` (int count); and
   top-level `build` (the key) and `build_verified`. This is what the GUI renders.
10. **GUI.** Header shows `Terraria <version> (build <buildid>)`. A build banner — same
    mechanism as the existing `sudo_warn` — appears when the build is unverified or any cheat
    is unavailable, summarising counts and naming the unavailable cheats with their reasons.
    An unavailable cheat's checkbox is disabled with the reason as its tooltip; an unverified
    but working cheat is marked but stays usable.
11. **Auto-restore.** Retry only while a pass still makes progress: a method that has not
    JIT-compiled yet resolves on a later pass, but a cheat that cannot resolve on this build
    never will. One summary line per game pid, and failures surface in the banner rather
    than the log.
12. **One panel at a time.** The GUI takes an `flock` under `XDG_RUNTIME_DIR` (the config
    directory is root-owned — the CLI creates it under sudo — so the unprivileged GUI cannot
    write a lock there). A second launch explains itself and exits without starting a worker.
    Two panels would mean two privileged workers, two 1 Hz syncs and two auto-restore loops
    racing on the same `patches.json`.

- **The `sites` number in the status meant two different things**, found while checking
  spec 034's open criterion. For a cheat that is off it is the anchor scan's match count;
  for one that is on it was the *scan cache*, which an earlier process fills and a later one
  does not — so `loot`, genuinely patched at four sites, reported "0 sites" while reporting
  itself available. Fixed to report what is installed when a cheat is applied. The
  availability flag was always right; only the count was misleading.

- **The version is read by frequency, and for ~21 seconds after launch the frequencies
  lie.** Measured across a real launch from the panel: from the instant the process appears
  until the game reaches its menu, the only version-shaped candidates are `1.4.5.8` and
  `2.0.50727` — a string constant in the exe and the mono runtime's own version — at one
  occurrence each. `max()` broke that tie by scan order, which is how a startup misread could
  report either of them with confidence and abort auto-restore for the whole session. The
  live version appears 2-4 times, and only once the game is up. Two rules follow: no
  component of a game version is anywhere near 1000, and one occurrence is not evidence.
  Both failures now produce `None`, which classifies as "unknown" and is retried, rather
  than a confident wrong answer.

## Risks & Assumptions

- **Patching a stale JIT copy.** Assumed inert: it is never executed. The risk is the reverse
  — the *live* copy is missed if a re-JIT happens after we resolve. Existing per-pid state
  already re-resolves on a new pid; a mid-session re-JIT remains a known gap (below).
- **Why two copies exist is not established.** Mono re-JIT, a second code manager arena, or a
  domain event are all plausible; this spec does not depend on the cause, only on the copies
  being identical where we patch.
- **The ledger is provenance, not a gate.** A cheat that resolves on an unverified build is
  offered, with a marker. Making it a hard gate would have disabled 7 working cheats after
  today's rebuild (decision recorded: verified-ledger over strict keying).
- **Rollback.** `git revert`. The anchor patterns themselves are unchanged, so reverting
  restores exactly today's behaviour.

## Acceptance Criteria

- [x] `Anchor` carries `verified` build keys; `version.build_key()` produces `"1.4.5.7+24893155"`
- [x] A byte-patch cheat with an anchor matching N identical sites patches all N; the per-pid
      state records every site and disable reverts every one (headless test over a synthetic
      image with two identical copies)
- [x] Resolution reports a specific reason — `matched N sites`, `not found`, `not yet
      compiled` — and never attributes a cause it has not checked
- [x] `patch status --json` carries `available` / `verified` / `reason` / `sites` per cheat and
      `build` / `build_verified` at top level
- [x] The GUI header shows the build id next to the version
- [x] A build banner appears when the build is unverified or any cheat is unavailable, naming
      the unavailable cheats and their reasons; it disappears when everything resolves
- [x] An unavailable cheat's checkbox is disabled and its tooltip states the reason
- [x] Auto-restore logs one summary per pid and does not retry a cheat when a pass makes
      no progress
- [x] A second GUI launch refuses with an explanation and starts no second worker; the lock
      is released automatically when the holder dies
- [x] Live: `mining`, `reach` and `max_minions` apply on the running game (both `ResetEffects`
      copies patched) and take effect in-game
- [x] All tests pass headless; flake8 clean on changed files; security review recorded
- [x] README updated; version bump confirmed by the maintainer

## Executive Summary

Three cheats had stopped applying, and the reported reason was wrong: `Player.ResetEffects`
is JIT'd twice in the running process, so the byte-patch anchors matched twice and
resolution demanded a unique hit. The failure text blamed a game update it had never
checked — and a rebuild had in fact just landed, which made the wrong explanation
convincing. Byte patches now apply to every identical copy (the live one is whichever it
is), failure reasons state only what was observed, and anchors carry a ledger of the builds
they were confirmed on so the panel can distinguish "unavailable here" from "works but
unproven on this build". The GUI shows that in the UI — build key in the header, a banner
naming unavailable cheats, disabled checkboxes with reasons — instead of repeating an
`[auto-restore]` line on every retry.

Reviewers: `Patcher.resolution`/`_resolve_sites` (multi-site patching), the `_VERIFIED_BUILDS`
ledger, and `client.build_banner`.

## Testing

187 headless tests, flake8 clean on changed files, `pip-audit 2.10.0` clean. New
`tests/test_build_ledger.py` (20) covers build keys, per-anchor verification, resolution of
two identical copies, the `unique` refusal, honest reasons, patch/revert across every site,
old single-address state migration, the `details()` contract and the banner text.

Live on the running game: `patch status` showed all nine cheats resolving on
`1.4.5.7+24893155`, `restore` returned `pending: []` for the first time with `sites=2` on the
three ResetEffects cheats, and the maintainer confirmed mining, reach and the minion cap
working in-game. A second GUI launch was verified to start no second worker.
