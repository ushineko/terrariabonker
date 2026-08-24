# Spec 035: Item compendium tab

**Status**: INCOMPLETE — Phase 1 (catalog + browse) implemented and validated;
Phase 2 (NPC stats + spawn) and Phase 3 (NPC sprites) open
**Implementation Date**: 2026-08-23 (Phase 1)

> **Note**: No issue tracker ticket (personal utility).

## Context

A browsable catalog of everything in the game: what it is, what it does, a link to the wiki,
and a way to put it in front of you. The Recipes tab already does a fraction of this — an
icon grid of the ~3,200 *craftable* items — so this is that idea taken to its conclusion,
across all items and all NPCs.

### Delivered in three phases

At the maintainer's request the work is split, with a validation stop after each:

| Phase | Scope | State |
| --- | --- | --- |
| 1 | Catalog: read every item's stats, browse/filter/sort, tooltip, wiki, give | implemented |
| 2 | NPC stats and real kinds, `SpawnOnPlayer`, placement offset, boss gate | open |
| 3 | NPC sprites from the 838 `Content/Images/NPC_*.xnb` sheets | open |

### What the recon found

**Names and text are all in the exe**, in the localization resources `tools/extract_item_names`
already walks (it just ignores everything except `ItemName`):

| key family | entries |
| --- | --- |
| `ItemName` | 6,186 |
| `ItemTooltip` | 2,790 |
| `NPCName` + `SpecialNPCName` | 730 |
| `CommonItemTooltip` | 78 (shared snippets referenced by tooltips) |

**Stats are only real at runtime.** They come from `SetDefaults`, so the authority is
`ContentSamples`, which holds a template of every item and NPC:

```
ContentSamples::ItemsByType          ContentSamples::NpcsByNetId
ContentSamples::NpcBestiaryRarityStars
```

`Item.get_OriginalDamage` shows how it is reached — `ldsfld ItemsByType; ldfld type;
callvirt get_Item` — so it is a **`Dictionary<int, Item>`**, not an array. Walking a mono
dictionary is the one genuinely fiddly piece of this feature.

**Spawning an item needs no new machinery.** `Service.give_item` has placed fully-statted
items since v0.2.2 by copying the ContentSamples template block into a slot.

**Placing the spawn is possible too, without a second managed call.** `NPC.AnyNPCs` is a
tiny method whose body is `ldsfld Main::npc; ldelem.ref; ldfld NPC::active` — the same shape
used to derive `Main.player` for `resolve_local_player`. Locating it yields `Main.npc`'s
address, so after the spawn the new NPC can be found in that array and its `position`
written directly. Spawning offset or offscreen is therefore a memory write, not a second
call. (`NPC.NewNPC` would take an explicit position but its first argument is an
`IEntitySource` object, which is awkward to produce from a cave.)

**NPC artwork is available**: 838 `Content/Images/NPC_*.xnb` sheets, the same format
`sprites.py` already decodes for items.

**Spawning an NPC needs a managed call, and an easy one:**

```
Terraria.NPC::SpawnOnPlayer(int plr, int Type)   ; static, two int args
IL_0000: ldsfld Main::netMode
IL_0005: ldc.i4.1
IL_0008: ret                                     ; bails only on a multiplayer client
```

Compared with the map-ping teleport (an instance call, five arguments, tile→pixel
conversion), this is a static two-argument call, and the code-cave call machinery already
exists. `Main.myPlayer` is already read by `resolve_local_player`.

## Requirements

1. A **Compendium** tab listing every item and every NPC, searchable by name or ID.
2. Each entry shows its **kind** — items as weapon / tool / accessory / armour / block /
   potion / material, NPCs as town NPC / monster / boss — plus its stats and, for items, the
   game's own tooltip text.
3. A **wiki** button per entry opening `https://terraria.wiki.gg/wiki/<Name>` in the user's
   browser. The app itself makes no network request.
4. **Give** for an item: it appears in the first empty inventory slot.
5. **Spawn** for an NPC, at a configurable distance from the player rather than on top of
   them — including far enough to be offscreen.
6. **Bosses are gated**: an explicit confirmation naming the boss, then a visible countdown
   before it actually spawns, cancellable. A misclick must not end a character.
7. Browsing is fast: no per-entry memory scan while scrolling.

### Technical

8. **Offline data.** Extend `tools/extract_item_names` to emit item tooltips and NPC names
   alongside the existing name map, into `data/tooltips.json` and `data/npcs.json`. Tooltip
   values referencing `CommonItemTooltip.*` are resolved at extraction time so the runtime
   carries plain strings.
9. **Runtime stats index — the fallback route was taken, deliberately.** The spec named the
   `Dictionary<int, Item>` walk as primary and a vtable scan as fallback. As built it is the
   scan, because the dictionary walk depends on *mono's* field layout rather than Terraria's:
   it can break on a runtime update that the spec-030 build ledger — which is keyed on the
   game's version+buildid — would not even notice, and so would fail in the one way this
   project has no defence against. The scan depends only on Terraria's own object layout.

   `content.find_item_templates` scans the writable regions for objects carrying the `Item`
   vtable (the scan `_template_block` already does for a single type), then picks the template
   table out by **shape**: the templates are one object per type, while live items — the
   inventory, chests, dropped items — repeat types heavily. Addresses are clustered into runs
   (`CLUSTER_GAP = 0x400000`) and a run is a template table when it holds about one object per
   distinct type (`ONE_TO_ONE = 1.05`). Largest table wins where a type appears in two, so a
   chest that happens to look one-to-one cannot outrank the real table.

   Measured live: 169,637 Item-shaped objects found in 463 ms; the one-to-one runs cover
   **6,162 distinct types**, i.e. every item the game has a template for. Cached per build
   under `~/.cache/terrariabonker/templates-<build>.json` (1.8 MiB), so reopening the tab
   does not rescan.

9b. **Six field offsets were derived for this, by differencing templates.** The project knew
   `type 0x6C`, `accessory 0x7D`, `useTime 0x84`, `createTile 0xA0` and `rare 0xF8`. Kind
   classification needed more, and they were obtained by differencing the template blocks of
   items with known values rather than by reading IL: `healLife 0xB4`, `healMana 0xB8`,
   `headSlot 0xD8`, `bodySlot 0xDC`, `legSlot 0xE0`, `buffType 0x130`. They live beside the
   existing constants in `inventory.py` and are covered by the classification tests.
10. **Kind classification** is derived from the template's own fields:
   `accessory`, `damage` (split by `melee`/`ranged`/`magic`/`summon`), `pick`,
   `headSlot`/`bodySlot`/`legSlot`, `createTile`, `healLife`/`healMana`/`buffType`. NPCs use
   `boss`, `townNPC` and `friendly` — **not yet read**, so every NPC currently reports kind
   `NPC` (Phase 2).

   **Order matters, and got this wrong once.** Filtering potions on `healLife`/`healMana`
   alone found only 16, because buff potions carry neither — they carry `buffType`. Adding
   `buffType` took potions to 236 but silently reclassified 28 summon staffs as potions, since
   a staff grants its minion as a buff. The damage test therefore runs *before* the buff test.
   Both cases are pinned by tests (`test_a_summon_staff_is_a_weapon_not_a_potion`,
   `test_buff_potions_count_as_potions`).
11. **NPC spawn** is a managed-call injection of `NPC.SpawnOnPlayer(Main.myPlayer, type)`,
    modelled on the teleport stub, and recorded in the spec-030 ledger.
12. **Spawn placement.** `SpawnOnPlayer` puts the NPC on the player; the stub then locates
    it in `Main.npc[]` (address derived from `NPC.AnyNPCs`, the `Main.player` trick) and
    writes its `position` offset by the configured distance, on the side the player faces
    away from. Distance is a tunable value, with a preset that clears the screen.
13. **Boss gate.** Kind `boss` requires a confirmation dialog naming it, then a countdown
    (default 5 s) shown in the tab with a cancel button; the spawn fires only when it
    elapses. The countdown is a `QTimer` on the existing event loop.
14. **The GUI** reuses the recipe browser's filter-proxy and sprite-cache machinery, but
    **not its icon grid** — that part of the spec did not survive contact. A grid cell cannot
    show a full item name plus its kind without clipping one of them, and 6,958 entries are
    browsed by name, not by picture. It is a sortable `QTreeView` over a
    `QStandardItemModel` behind a `QSortFilterProxyModel`, with the sprite as the row icon:
    columns Name · Kind · Damage · Defense · Rarity · ID.

    Numeric cells sort on the real number even where the cell is blank, so "no damage" groups
    together instead of scattering. `setStretchLastSection(False)` with Name as the stretching
    column: Qt stretches the last section by default, which handed ID the whole slack and left
    its header stranded away from its right-aligned values.
15. **NPC sprites** extend `sprites.py` to decode `Content/Images/NPC_*.xnb` into the same
    cache, cropping to the first frame as the animated item strips already do. The cache
    scope string is bumped so existing caches re-extract.
16. The wiki button uses `QProcess.startDetached("xdg-open", …)`, exactly as the Steam launch
    button does, so nothing runs as root and no network call originates in the app.
17. **One dialog per double-click.** `doubleClicked` and `activated` both fire for a
    double-click under this style, and a re-entrancy flag does not help: `exec()` runs a
    nested event loop, so the second signal is delivered *after* the dialog closes and simply
    opens another one. Only `doubleClicked` is connected; Enter is wired separately as a
    `QShortcut` so keyboard use still works. Pinned by a test that emits both signals.
18. **Localization references must resolve.** The extractor's reference resolver only handled
    `CommonItemTooltip.*`; generalising it to any `{$Category.Key}` took unresolved
    references from 19 to 0 in NPC names and 534 to 5 in tooltips (2 to 0 in item names).
    Tests assert the remainder stays at zero for names.
19. **Code-patch tooltips got a crispness pass** in the same round of UI feedback: all 12
    cheat notes rewritten to tooltip length, re-flowed through `gui/uitext.wrap`, and the
    patch list split into one tab per section (Build / Combat / Accessories / Misc) so it
    scales as cheats are added. Tests cap note length and forbid the repeated
    "a game restart clears it" sentence.

## Risks & Assumptions

- **The dictionary walk was the risky part, and was not taken.** Mono's `Dictionary` field
  order is a runtime implementation detail, not a Terraria one, so it can change with the
  mono version rather than with the game build — invisible to a ledger keyed on the game's
  version+buildid. The shipped route is the vtable scan, which depends only on Terraria's own
  layout. Its own risk is different and milder: the shape heuristic could in principle pick a
  wrong run. It is bounded by taking the largest one-to-one run and covered by tests for both
  failure directions (a repeated-type run must not be mistaken for the table; the larger table
  wins when a type appears in two).
- **The template cache is root-owned inside the user's cache directory.** The privileged
  worker writes `~/.cache/terrariabonker/templates-<build>.json`, so the file lands
  `root:root` in a directory the user owns. Two consequences: the user cannot clear their own
  cache without `sudo`, and the directory being user-writable means root parses JSON from a
  path an unprivileged process could replace. Impact is bounded — the cache feeds the
  compendium *display* only; `give_item` does its own live template scan and never reads it —
  and `json.load` executes nothing. Recorded rather than fixed in Phase 1; the fix is to
  `chown` to `SUDO_UID`/`SUDO_GID` on write.
- **The build key is safe to use as a filename.** It is interpolated into the cache path, and
  both halves are constrained at their source: the version is matched by a digits-and-dots
  regex, the Steam buildid by `(\d+)`, each falling back to `?`. No separator or `..` can
  reach the path.
- **Spawning a boss is destructive**, which is why it is gated twice: a confirmation naming
  it, then a cancellable countdown. `SpawnOnPlayer` would otherwise drop it on top of the
  player at full HP, and a summon in the wrong world state may simply be unwinnable.
- **Repositioning after spawn may not suit every NPC.** Some bosses run an entry animation or
  anchor themselves on spawn, so moving them a frame later could look wrong or be undone.
  Distance is configurable and defaults conservatively; per-NPC quirks are accepted.
- **`SpawnOnPlayer` bails on `netMode == 1`.** Single-player is netMode 0, so this is fine,
  but the cheat should say so rather than silently doing nothing if the user is a client.
- **Wiki article names are not always the display name.** Most match; some redirect, a few
  differ. Acceptable — the browser handles a redirect, and a miss lands on a search page.
- **6,186 entries in a Qt view** needs the same proxy-model treatment the recipe grid already
  uses; a naive widget-per-entry layout will not do.
- **Tooltip text contains formatting tokens** (`{0}`, colour codes). They are displayed as
  the game stores them unless trivially strippable. Five tooltips still carry an unresolved
  reference after the resolver generalisation; they render as the raw token.
- **NPC kinds are a placeholder in Phase 1.** Every NPC reports kind `NPC` because the flags
  that distinguish town/boss/monster live on the NPC template, which needs `Main.npc` —
  Phase 2 work. `npc_kind()` is written and tested against synthetic stats, but nothing calls
  it with real data yet.
- **Wiki URLs are not percent-encoded.** The scheme and host are a hardcoded prefix and names
  come from the game's own localization, so the result is always a well-formed
  `https://terraria.wiki.gg/` URL handed to `xdg-open` as its own argv element — no shell,
  no injection. Punctuation in a name (apostrophes) is legal in a URL and the wiki resolves
  it.
- **Rollback.** `git revert`. The tab is additive; the only memory-writing parts are the
  existing `give_item` and the new spawn injection, which is off unless used.

## Acceptance Criteria

### Phase 1 — catalog and browsing

- [x] A **Compendium** tab lists all items and all NPCs (6,195 items + 763 NPCs), filterable
      by name or ID and by kind, and scrolls smoothly with the full set loaded
- [x] Item entries show a kind tag, the game's own tooltip text, and stats
      (damage / defense / rarity / use time / pickaxe), sortable by column
- [x] Item entries show their sprite from the existing icon cache
- [x] The stats index is built once and cached per build; reopening the tab does not rescan
      (6,162 types, 1.8 MiB at `~/.cache/terrariabonker/templates-1.4.5.7-24893155.json`)
- [x] `tools/extract_item_names` emits tooltips and NPC names, and the regenerated data files
      are committed (`items.json` 6,195, `tooltips.json` 2,789, `npcs.json` 763)
- [x] A double-click opens exactly one dialog, and Enter opens one from the keyboard
- [x] The app makes no network request of its own: the wiki button hands the URL to
      `xdg-open` via `QProcess.startDetached` with the URL as its own argv element
- [ ] The wiki button opens the correct article in the default browser — NOT YET EXERCISED
      live. The code path is in place and unit-tested for URL shape; nobody has clicked it
      in a running session
- [ ] **Give** from a compendium entry puts the item in the first empty inventory slot — NOT
      YET EXERCISED live from this tab. It reuses `give_item`, which has worked since v0.2.2,
      but the compendium's own button has not been clicked in a running session
- [x] All tests pass headless (269); flake8 clean on changed code; security review recorded
- [x] The template cache is handed back to the invoking user rather than left root-owned
      in their cache directory (`proc.give_back_to_user`)
- [x] README updated; version bumped to 0.25.0 (maintainer confirmed)

### Phase 2 — NPC stats and spawning (open)

- [ ] NPC entries show life / damage / defense and a real kind (town NPC / monster / boss)
      rather than the Phase 1 placeholder
- [ ] **Spawn** places the selected NPC at the configured distance, verified in-game with a
      harmless monster, including a distance that puts it offscreen
- [ ] Spawning a **boss** requires an explicit confirmation naming it, followed by a
      cancellable countdown; cancelling spawns nothing
- [ ] The spawn anchor carries the build key it was confirmed on

### Phase 3 — NPC artwork (open)

- [ ] NPC entries show their sprite, decoded from `Content/Images/NPC_*.xnb` into the existing
      cache

## Executive Summary

Phase 1 of the compendium: a Compendium tab that lists all 6,195 items and 763 NPCs,
filterable by kind and by name or ID, sortable on damage / defense / rarity / ID, showing each
item's sprite, kind, the game's own tooltip, and a wiki link.

The one genuinely uncertain piece was reading item stats, which exist only at runtime. The
spec's primary route was walking `ContentSamples.ItemsByType` as a mono `Dictionary`; that was
**deliberately not taken**, because it depends on the mono runtime's field layout rather than
Terraria's and could therefore break in a way the build ledger — keyed on the game's
version+buildid — would never see. The shipped route scans the writable regions for objects
carrying the `Item` vtable and identifies the template table by its shape: templates are one
object per type, live items repeat types heavily. 169,637 candidates in 463 ms yield all 6,162
typed templates, cached per build.

Reviewers: `content.find_item_templates` (the clustering and the one-to-one test),
`content.item_kind` (the ordering — the damage test must precede the buff test, or 28 summon
staffs become potions), and `Service._item_template_cache`.

Deferred with the maintainer's agreement: NPC stats and spawning (Phase 2) and NPC sprites
(Phase 3). Every NPC currently reports kind `NPC`.

## Testing

269 headless tests (42 new), flake8 clean on changed code, `pip-audit 2.10.0` clean.

- `tests/test_content.py` (18): the template scan finds one object per type, ignores
  non-Item objects and absurd type values, does not mistake a repeated-type run for the
  table, and prefers the larger table when a type appears in two; the kind ladder in both
  directions (accessory beats damage, vanity armour with no defense is still armour, tool
  beats weapon, weapons split by damage class, block before material, **summon staff is a
  weapon not a potion**, **buff potions are potions**); wiki URL shape; and no unresolved
  localization references remain in item or NPC names.
- `tests/test_compendium_tab.py` (8): a double-click opens **exactly one** dialog (the test
  emits both `doubleClicked` and `activated`, which is the bug it was written for); rows
  carry name/kind/stats/id; the kind filter and the name-or-ID search narrow the list; ID
  sorts numerically; blank stat cells still sort by their real value; the last column does
  not swallow the slack; numeric headers are right-aligned like their values.
- `tests/test_uitext.py` (7): notes wrap within the tooltip width, blank lines survive,
  source line breaks are re-flowed rather than inherited, and every cheat note stays
  tooltip-sized without repeating what the group tooltip already says.
- `tests/test_gui_construct.py` (3): the main window builds with every tab, every cheat has a
  checkbox, and code patches are split into section tabs. This file exists because the GUI
  failed to start after the Compendium tab was added — a log callback was bound to a widget
  built later — and the smoke test was confirmed to catch that bug before the fix landed.
- `tests/test_cache_ownership.py` (6): the ownership guard in all four states (unprivileged,
  root under sudo, a real root shell with no `SUDO_UID`, a junk `SUDO_UID`), a chown failure
  is survivable, and the cache write path calls it for both the directory and the file.
  Confirmed load-bearing by removing the two calls and watching the test fail.

Live, maintainer-validated across several rounds of UI feedback: the icon grid was replaced
with a sortable list after it clipped names; stat columns were added for sorting; the potion
kind was found incomplete (16 → 236) and the summon-staff regression that fix introduced was
caught and fixed; header/value alignment was corrected; cheat tooltips were rewritten for
crispness and split into section tabs; and dialogs were found pinned to the window's
upper-left by our own spec-031 KWin rule (see that spec).

Not yet exercised live: the Wiki and Give buttons on a compendium entry.
