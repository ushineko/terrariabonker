# Validation Report — the last commands behind the every-write gate (spec 052, slices 24-27)

**Date**: 2026-10-11 22:45
**Commit**: uncommitted (working tree on `main` at 30eb167)
**Status**: PASSED headless. One of the four is confirmed live (`extract-recipes`,
a read). The three that write were not run against the game -- see "Live
confirmation".
**Scope**: under .NET Framework `extract-tick` / `extract-stop`, `freeze` /
`godmode`, the debug `write` and `extract-recipes` run. Each sat behind the
every-write gate. The `Writes` flag itself is unchanged: the CLR entry is still
per-feature.

## What changed

- `internal/cli`: `extract-tick` and `extract-stop` gate on the ore extractor's
  write feature; `freeze` and `godmode` on `WritePlayerStats`; the debug `write`
  on "any write at all"; `extract-recipes` on a new read feature, `ReadRecipes`,
  and then on the build check. `game()` lost its `guard` parameter, which no
  caller passed any more.
- `internal/trainer`: `Freezer.Targets` -- who to hold, asked again when the
  addresses die. A refusal from it is what a run that starts with nobody reports.
- `internal/service`: `StatTargets` (the live character's copies as handles, with
  the entry's numbers); `Recipes` and `recipeBase`; `requireBuild` exported as
  `RequireBuild`.
- `internal/recipes`: `Extract` takes the entry and a base. Finding Main.recipe
  and the two tile-name statics is a resolver per runtime ("mono", "clr").
- `internal/layout`: `RecipeFields` on the entry; `RecipeFromPlayer`,
  `TileLookupFromPlayer` and `MapLegendFromPlayer` in `MainStatics`; `ReadRecipes`
  in both entries' reads; `CLRFields` gains Recipe, Lang, Map.MapHelper,
  Localization.LocalizedText and two Item fields.
- **Behaviour change on both runtimes**: a freeze holds the live character's
  copies only, and is refused when several characters are in memory and which is
  played cannot be told. It used to hold every copy the mono locator found. Not
  watched on Linux.
- **Behaviour change on both runtimes**: `extract-recipes` no longer needs an
  entry that may write, only one that can read recipes and a build that is not
  incompatible. On mono the two conditions admit the same games as before.

## Measurements (Windows, live game, 2026-10-11)

`cmd/clrfields` against the running game; reads only. The output is appended to
`docs/clr-fields-1.4.5.8.txt`.

| What | Result |
| --- | --- |
| Recipe (25 stored fields, 1 complete list, static flag 25/25) | createItem 0x04, requiredItem 0x08, requiredTile 0x18 |
| LocalizedText (5, 5/5) | `_value` 0x08 |
| Item.createTile, Item.placeStyle (measured 2026-10-08) | 0x07C, 0x084 |
| Static offsets | Main.recipe 0x870, Main.player 0x878, MapHelper.tileLookup 0x10D4, Lang._mapLegendCache 0x0BC |
| the whole CLR table | `go run ./cmd/clrfields --verify`: every field agrees, exit 0 |

The static offsets of five classes do not overlap and are in one ascending range
(Chest 0x018.., Lang 0x094.., Recipe 0x228.., Main 0x5F8.., MapHelper 0x10C4..,
LocalizedText 0x10FC..). That another class's static is a fixed distance from
Main.player's slot was an inference from this; the live extraction below is what
checks it.

## Phase 3: Tests

| Where | Command | Result |
| --- | --- | --- |
| Windows 11 | `go test -race ./...` | 27 packages `ok` |
| Windows | `golangci-lint-v2.12.2`, project config, `GOTOOLCHAIN=go1.26.0` | 0 issues |
| Windows, for Linux | the same linter with `GOOS=linux` over `recipes`, `trainer`, `service`, `cli`, `layout` | 0 issues |
| Windows, for Linux | `GOOS=linux CGO_ENABLED=0 go vet` over the non-GUI packages (less `cmd/winrecon`, Windows-only) | clean |
| Windows | `gofmt -l cmd internal` | clean |

The suite was not run on Linux.

**New tests** (CLR numbers are literals from the measurement):
- `recipes`: the fixture's book planted the CLR way and read back, station name
  included; each runtime's numbers find no book in the other's image; no array
  and an absurd length under the CLR; either tile-name static empty leaves the
  book read and its stations unnamed; an entry naming no resolver is refused by
  name. The mono tests are unchanged in what they check and now pass the entry
  and the static block's address; the book assertion gained the lone ingredient
  of the second recipe.
- `layout`: `TestTheCLRRecipeFieldsAreTheTable` -- the fields by name, the three
  distances as differences of the table and as literals, mono's as literals and
  zero. Both table digests changed with the table.
- `service`: one recipe read through Main.player's slot under the CLR, and
  through the static block under mono; no slot is "Main's statics", not "no
  array".
- `trainer`: a run holds only the targets it is given, with a second readable
  player in memory left alone; a refusal to name anyone is what the run reports,
  and nothing is written.
- `cli`: under netfx, godmode holds 390 of 420 at 420; a freeze with two
  characters and no ground truth is refused with memory unchanged; `extract-tick`
  and `extract-stop` pass the gate and write nothing; the debug `write` lands,
  and is refused under a runtime no entry covers; `extract-recipes` reaches the
  statics and writes no file, and is refused on an incompatible build.

**Mutation checks** -- 33 mutants:
- Gates and freeze targets, 8: each gate put back to the every-write gate, the
  debug `write` left ungated, `Targets` not wired in the CLI, `Targets` ignored
  by `Locate`, the refusal dropped. All killed.
- Recipes, 25: each entry-supplied number replaced by mono's constant (the
  three Recipe fields, item type and stack in the output and in an ingredient,
  createTile, placeStyle, the text offset, both array offsets in each of the
  four arrays read), the CLR string reader replaced by mono's, the two name
  slots swapped, the recipe distance negated, an unknown resolver falling
  through to "clr", `recipeBase` fixed to either branch, the command's gate put
  back or removed, the build check removed.
  - 23 killed on the first run.
  - 2 survived because a test was weak: an ingredient array's length read at
    mono's offset, and an ingredient's stack read at mono's. In the CLR image
    the first reads the second element's pointer (clamped to 40) and the second
    reads zero (taken as 1), and the only ingredients checked were a pair, one
    of each. `requireTheBook` now checks the second recipe's `{9, 7}`; both
    killed.

## Phase 4: Code quality

- No shared routine branches on the runtime: the walk reads the entry and asks a
  resolver named by it. `recipeBase` picks the base by whether the entry has a
  distance, the shape `npcBase`, `tileBase` and `worldNameSlot` already have.
- Dead code removed: `game`'s `guard` parameter and both of its branches;
  `recipes.LocalizedTextValue`, a re-export with no reader left; the
  get_LocalPlayer code the recipes fixture planted for a lookup that moved to
  the service.
- Left alone: `entry_netfx.go`'s opening comment still says "never for writing
  yet", which has been out of date since slice 4. `RequireCompatible` keeps one
  caller, `patch status`' early arena.

## Phase 5: Security review

- No hardcoded secrets, no new subprocess, no new network call, no new
  dependency (`govulncheck` not re-run: `go.mod` is unchanged).
- Access control: the freeze's targets are the service's write targets, so it
  can no longer write to a character other than the one being played. The debug
  `write` remains what it was -- an arbitrary write to the user's own game --
  and is still refused where no entry covers the runtime.
- Unsafe file operations: `extract-recipes` writes `data/recipes.json` under the
  working directory, as before. It is now refused on an incompatible build by an
  explicit check rather than as a side effect of the write gate; a test pins it.
- Input validation: the CLR recipe slot is believed only when it holds an array
  whose length is between 1 and 50000, the bound mono's path already had. The
  two name statics are bounds-checked per tile and per legend index, and a
  string that is empty or unprintable is no name.

## Phase 5.5: Release safety

- **Change type**: code and version-table data.
- **Pattern**: additive -- one read feature on both entries, and four commands
  moved onto features the CLR entry already allowed. Two behaviour changes,
  described above.
- **Rollback**: revert the commit and rebuild. Nothing here persists in the
  game or on disk beyond `data/recipes.json`, which only `extract-recipes`
  writes and which is in version control.
- **Rollout**: nothing runs on its own.

## Live confirmation (Windows, 2026-10-11)

Done, reads only:
- `extract-recipes`, run from a scratch directory against the live game under
  .NET Framework, wrote a book whose three sections each equal the checked-in
  `data/recipes.json` (extracted under wine-mono): 3603 recipes, 35 station
  names (Bottle, Anvil, Furnace, Work Bench, Demon Altar, Hellforge, ...), 1923
  tile icons. Run twice, before and after the last code change; same result.
  The files differ in bytes -- key order -- and were compared as parsed JSON.
- `extract-stop --json` ran through the gate and reported `{"stopped":false}`.
- `status` and `build-check` unchanged: the player found, `supported,
  read-only`, the cheats resolving.

Not done -- each is a write to the game and was not made without the maintainer
watching:
- `freeze` / `godmode` on Windows.
- A vein mined through `extract-tick` (the window's path; `extract --watch` ran
  live in slice 14 over the same service code).
- The debug `write`.

## The `Writes` flip, tried and put back

With these four moved, no command sits behind the every-write gate. Setting the
CLR entry's `Writes` to true was tried: seven tests fail, in `layout`
(`TestTheTableIsFrozen`, `TestReadsAndWritesAreSeparate`,
`TestWritesArePerFeature`) and `service` (the unsupported-runtime refusal,
`RequireAnyWrite`, and the two build-decision tests). They pin "the CLR entry is
read-only" and are the only tests of the per-feature mechanism. The flip also
changes what the window does on Windows (the build question, every cheat no
longer off), which has not been watched. It was put back and is left as the next
step, for the maintainer to decide.

## Overall

- All gates passed: YES headless. Live: the read confirmed; three writes pending
  the maintainer.
- Notes: the version in `.tag` is not bumped.
