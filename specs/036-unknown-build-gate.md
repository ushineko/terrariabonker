# Spec 036: Tell the user, loudly, when the game has updated

**Status**: COMPLETE
**Implementation Date**: 2026-08-23

> **Note**: No issue tracker ticket (personal utility).

## Context

On 2026-08-23 Terraria updated from 1.4.5.7 to 1.4.5.8 at 12:19, while the game was
running. The panel said nothing. It could not have: its version check was reading a stale
`"Version":"v1.4.5.7"` JSON blob out of the heap and reporting the previous build with
confidence (fixed separately — see `docs`/git history for the exe-literal rule).

Even with the version read correctly, the panel's response to an unrecognised build is a
small amber banner that is easy to miss, and cheats that quietly fail to resolve. The
maintainer's requirement: **say so noisily, then let the user decide.**

This matters more than a cosmetic warning. The AOB patterns are derived against one exact
build. When the game changes underneath them the possible outcomes are:

- every pattern still matches — the update did not touch the code we patch (what actually
  happened on 1.4.5.8, where all twelve cheats resolved and applied);
- some patterns match and some do not — a partly working trainer, which is the dangerous
  case, because a cheat that silently does nothing looks like a cheat that is off;
- nothing matches — the offsets are for a different game.

The user cannot tell these apart today.

## Requirements

1. **A modal on an unrecognised build.** When the running game's build key is not one the
   project has verified, nor one this machine has already accepted, the panel says so in a
   dialog before normal use — naming the build it found and the build it knows.
2. The dialog offers to **check the cheats against the running build** rather than making
   the user guess. The check resolves every anchor without patching anything.
3. **All clear**: if every cheat resolves, the user can **accept the build**, which records
   it so the dialog does not return for it. This is the best case and should be one click.
4. **Partly working**: the dialog lists exactly which cheats did not resolve, and offers
   **Continue** — with those cheats disabled and visibly unavailable in the UI, carrying
   the reason — or **Exit**.
5. **Continuing is remembered per build**, so the dialog does not reappear every launch for
   a build the user has already decided about.
6. Whatever is accepted is **distinguishable from what the project verified**: a build that
   resolved on this machine is not the same claim as an AOB a human confirmed in play.
7. The gate triggers **whenever an unrecognised build is first seen**, not only at panel
   startup — launching the game from the panel, or restarting it into an update, is exactly
   when this happens.

### Technical

8. **Recording an accepted build must not mean editing source.** The verified sets in
   `patcher` are the project's claim, maintained by whoever derived the AOBs. A user's
   acceptance is a different, weaker claim and belongs in
   `~/.config/terrariabonker/accepted-builds.json`, alongside the other user state.
9. **The check reuses `Patcher.resolution`**, which already reports availability with an
   honest reason, and already knows a cheat can be patched at several sites.
10. **A cheat that has not been JIT-compiled yet is not a broken cheat**, and the two are
    not distinguishable from a single scan: `fast_place` resolves only once an item has
    been used. The check must not report those as failures. It therefore runs when a player
    is loaded, re-checks on a timer while anything is unresolved, and reports a cheat as
    failed only after it has stayed unresolved across several passes — the same "no
    progress" rule auto-restore already uses.
11. **The build key mixes two sources** — the version from the running process, the Steam
    buildid from the manifest on disk — so between an update and a restart they describe
    different builds. The gate must compare like with like, and say which half changed.
12. Cheats that failed the check are **disabled in the UI** with the reason on hover, not
    merely left to fail when clicked.

## Risks & Assumptions

- **A resolving anchor is not a working cheat.** The check proves a byte pattern still
  matches, not that the code around it means the same thing. Accepting a build on that
  basis is a judgement the user is making, and the wording must not overstate it: "the
  patterns still match" rather than "verified".
- **The JIT ambiguity is real and unfixable from one scan.** Requirement 10 reduces it to a
  delay, not a certainty. A cheat whose method is never called during the check will read
  as unresolved however long we wait.
- **Refusing to run is worse than running degraded**, for a single-player trainer whose
  writes are validated by the locator anyway. Exit is offered, not forced.
- **Rollback.** `git revert`; the accepted-builds file is disposable and its absence just
  brings the dialog back.

## Acceptance Criteria

- [x] An unrecognised build raises a modal naming what was found and what is known, before
      the panel is used
- [x] The modal can check every cheat against the running build without patching anything
- [x] All cheats resolving offers one-click **accept**, recorded so the dialog does not
      return for that build
- [x] Any cheat failing lists them by name with their reason, and offers Continue or Exit
- [x] Continuing disables exactly those cheats in the UI, with the reason on hover
- [x] A decision is remembered per build key
- [x] An accepted build is presented differently from a project-verified one, in the banner
      and in the patch list
- [x] A cheat that is merely not JIT-compiled yet is not reported as failed
- [x] The gate fires when the game is restarted into a new build mid-session, not only at
      panel startup
- [x] Accepting writes to `~/.config/terrariabonker/accepted-builds.json`, never to source
- [x] All tests pass headless; flake8 clean on changed files; security review recorded
- [x] README updated; version bump confirmed by the maintainer

## Executive Summary

Terraria updated from 1.4.5.7 to 1.4.5.8 underneath a running panel and the panel said
nothing. It now asks: an unrecognised build raises a modal naming what is running and what
the project knows, and offers to check every cheat against it without patching anything.
All clear is one click to accept; anything failing is listed by name with its reason, and
the user chooses between carrying on without those cheats — disabled and greyed, with the
reason on hover — or exiting.

Two distinctions the design turns on. A build this machine accepted is **not** a build the
project verified: the former means the byte patterns still match, the latter that a human
watched each cheat work. They are recorded in different places, worded differently, and the
dialog says so. And a cheat that has not been JIT-compiled yet is **not** a broken cheat, so
the gate waits for a player to be in-world before judging — a scan at the main menu reports
lazily-compiled hooks as unmatched, and a dialog that calls a cheat dead when it is merely
uncompiled is worse than no dialog.

Used in anger the day it was written: the running 1.4.5.8 build was unrecognised, all twelve
cheats resolved, and the build is now recorded as verified in the project ledger.

Reviewers: `gui/buildgate.py` (the dialog is handed a finished check and returns a decision,
which is what makes it testable), `MainWindow._maybe_gate_build` (why it keys on the build
and waits for a player), and `builds.py` (why acceptance lives apart from verification).

## Testing

341 headless tests, flake8 clean on changed code, `pip-audit 2.10.0` clean.

- `tests/test_build_gate.py` (14): all-clear offers one-click accept; a dead cheat is named
  with its reason and continuing is the default; Exit is always offered; **closing the
  window is not consent**; both builds and the dead cheat appear in the text; a decision is
  remembered per build and does not cover other builds; degraded records which cheats are
  dead; forgetting brings the question back; a corrupt file is not fatal; an unknown build
  is asked about exactly once — across a *completed* check, so the record is what stops the
  second ask rather than the in-flight guard; a recognised build asks nothing; continuing
  disables exactly the dead cheats; an unreadable check is asked again rather than assumed
  fine; and the gate waits for a player to be in-world.
- `tests/test_build_ledger.py`: the invariants were restated when `KNOWN_BUILD_KEY` moved
  to 1.4.5.8. "A later anchor must not inherit the default verification" is now asserted
  against the *derivation* build, since the current target is one those anchors genuinely
  were confirmed on, and every build any anchor claims must appear in an explicit
  allowlist — so adding one is a deliberate edit next to the claim it makes.
- **Four mutation checks.** One caught a weak test of mine: "asked about once" passed even
  with the bookkeeping removed, because the in-flight guard masked it. Rewritten to let the
  check finish first, and it now fails when it should.

Live, on the build that prompted this: the dialog appeared for 1.4.5.8+24893155, reported
all twelve cheats resolving, and accepting recorded it. The build is now in the project
ledger as verified — maintainer-confirmed that every cheat still works — so `build-check`
reports `exact / recognised / known-good` and no cheat reports unverified.
