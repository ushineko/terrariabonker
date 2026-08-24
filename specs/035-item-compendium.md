# Spec 035: Item compendium tab

**Status**: COMPLETE — all three phases implemented and validated in-game
**Implementation Date**: 2026-08-23 (Phases 1 and 2)

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
| 1 | Catalog: read every item's stats, browse/filter/sort, tooltip, wiki, give | done |
| 2 | NPC stats and real kinds, spawning, placement offset, boss gate | done |
| 3 | NPC sprites from the `Content/Images/NPC_*.xnb` sheets | done |

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

10. **Six field offsets were derived for this, by differencing templates.** The project knew
   `type 0x6C`, `accessory 0x7D`, `useTime 0x84`, `createTile 0xA0` and `rare 0xF8`. Kind
   classification needed more, and they were obtained by differencing the template blocks of
   items with known values rather than by reading IL: `healLife 0xB4`, `healMana 0xB8`,
   `headSlot 0xD8`, `bodySlot 0xDC`, `legSlot 0xE0`, `buffType 0x130`. They live beside the
   existing constants in `inventory.py` and are covered by the classification tests.
11. **Kind classification** is derived from the template's own fields:
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
12. **NPC spawn is a template copy, not a managed call — the spec's plan was dropped.**
    Recon corrected two things behind that plan. First, `NPC.SpawnOnPlayer` does not take
    two ints: it takes **six** arguments, `(int plr, int Type, float, float, float, float)`,
    confirmed at three independent call sites (`ItemCheck_UseBossSpawners`, `WorldGen::CheckOrb`,
    `SpawnMechQueen`), all pushing four float zeros. Second, those floats are not a spawn
    offset — `SpawnBoss` passes them straight through to `NewNPC` as `ai[0..3]`.

    More importantly, a managed call has no natural trigger here. The teleport stub is
    driven by a player action it already hooks; an on-demand spawn would need a stub sitting
    in a per-frame method polling a flag we write from outside. That is live JIT patching in
    a hot path for something the project can already do without any code injection:
    `give_item` has spawned fully-statted **items** since v0.2.2 by copying a ContentSamples
    template into a slot. NPCs are the same trick one level up, because every `Main.npc`
    slot is a real NPC object allocated at world load.

    `Service.spawn_npc` therefore copies the template over a free slot, sets the position,
    and writes `active` **last** — until that byte is set the game skips the slot, so it
    never sees a half-built NPC. No cave, no managed call, nothing to record in the ledger.
13. **The copy must skip every reference field**, or the spawned NPC and the template would
    share arrays. Those are `0x044..0x06F`; what is left is two pointer-free spans,
    `NPC_COPY_SPANS = ((0x2C, 0x44), (0x70, 0x298))`, covering direction, size and the whole
    stat block. The object header, `whoAmI` and the entity's position/velocity are set
    explicitly rather than copied.

    A first pointer map also flagged `0x080..0x08F`; those are floats (0.5, 0.25, 0.375)
    whose bit patterns happen to land in a readable region. Copying them as if they were
    references would have been the exact bug this rule exists to prevent.
14. **Spawn placement** is `player.position` offset by the configured distance opposite the
    way the player faces, clamped away from tile 0 so a spawn near the world edge cannot
    land outside the map. Distance is a spinbox on the entry dialog, defaulting to 25 tiles.
15. **Boss gate.** Kind `Boss` requires a confirmation dialog naming it, then a countdown
    (default 5 s) shown **in the tab** with a cancel button — not in a modal, because a
    modal the user must keep open in order to cancel is worse than a line they can cancel
    from at any moment. The countdown is a `QTimer` on the existing event loop.
16. **The GUI** reuses the recipe browser's filter-proxy and sprite-cache machinery, but
    **not its icon grid** — that part of the spec did not survive contact. A grid cell cannot
    show a full item name plus its kind without clipping one of them, and 6,958 entries are
    browsed by name, not by picture. It is a sortable `QTreeView` over a
    `QStandardItemModel` behind a `QSortFilterProxyModel`, with the sprite as the row icon:
    columns Name · Kind · Damage · Defense · Rarity · ID.

    Numeric cells sort on the real number even where the cell is blank, so "no damage" groups
    together instead of scattering. `setStretchLastSection(False)` with Name as the stretching
    column: Qt stretches the last section by default, which handed ID the whole slack and left
    its header stranded away from its right-aligned values.
17. **NPC sprites** extend `sprites.py` to decode `Content/Images/NPC_*.xnb` into the same
    cache. They do **not** reuse the item de-animator: that infers a strip from
    `height >= 2*width` plus evenly spaced content blocks, which is right for tall item
    strips and wrong for wide NPCs — a two-frame Blue Slime sheet is 32x52, and a
    one-frame Moon Lord is 573x804. The exact answer is the game's own
    `Main.npcFrameCount`, an `int[697]` at Main-static **+0x0C34**, verified by the fact
    that it divides every sheet height measured (Guide 1456/26, Bunny 280/7, Eye of
    Cthulhu 996/6, Blue Slime 52/2, Moon Lord 804/1).

    The division is floored rather than required to be exact: several sheets carry a few
    rows of padding (Duke Fishron 1298 over 8, Skeletron Prime 940 over 6), and refusing
    those left whole strips on screen.
18. **Some sheets are grids, not strips**, and `npcFrameCount` counts frames rather than
    rows — Queen Slime's 16 frames are 2 columns of 8, Deerclops uses 5 columns, Moon Lord
    a 3x3 — so a vertical crop alone leaves a row of little pictures. `_first_grid_cell`
    takes the top-left cell, and only when the image splits into several **evenly sized**
    blocks separated by fully transparent lines. That evenness test is what stops it
    slicing a single sprite that has a detached piece. Measured across all 697 sheets it
    changes 19 and leaves 678 untouched.
19. **The frame counts cross the privilege boundary through a file.** They live in the
    game's memory, so only the privileged side can read them, while extraction runs
    unprivileged. `Service.compendium` publishes them to
    `~/.cache/terrariabonker/npcframes.json`; the extractor reads them there. If they are
    absent the NPC sheets are **skipped rather than cached whole** — a wrong icon would
    persist until the scope is bumped, a missing one is fixed by the next run — and
    `is_cached()` reports the cache incomplete so that next run happens.
20. **NPC sprites are keyed by type, not netID.** Every coloured slime and every Hornet is
    a separate netID sharing one base type, and the game ships one sheet per type; asking
    by id would request `NPC_-65.xnb`. The cache filenames are `NPC_<type>.png`, kept
    apart from items' `Item_<id>.png` because the two id spaces overlap.
21. The wiki button uses `QProcess.startDetached("xdg-open", …)`, exactly as the Steam launch
    button does, so nothing runs as root and no network call originates in the app.
22. **One dialog per double-click.** `doubleClicked` and `activated` both fire for a
    double-click under this style, and a re-entrancy flag does not help: `exec()` runs a
    nested event loop, so the second signal is delivered *after* the dialog closes and simply
    opens another one. Only `doubleClicked` is connected; Enter is wired separately as a
    `QShortcut` so keyboard use still works. Pinned by a test that emits both signals.
23. **Localization references must resolve.** The extractor's reference resolver only handled
    `CommonItemTooltip.*`; generalising it to any `{$Category.Key}` took unresolved
    references from 19 to 0 in NPC names and 534 to 5 in tooltips (2 to 0 in item names).
    Tests assert the remainder stays at zero for names.
24. **Code-patch tooltips got a crispness pass** in the same round of UI feedback: all 12
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
  and `json.load` executes nothing. **Fixed in v0.25.0**: `proc.give_back_to_user()` hands
  the directory and file to `SUDO_UID`/`SUDO_GID`, but only for paths inside that user's own
  home — without `sudo -E` the cache lands under `/root`, and handing *that* over would widen
  write access inside root's home rather than fix anything.
- **The build key is safe to use as a filename.** It is interpolated into the cache path, and
  both halves are constrained at their source: the version is matched by a digits-and-dots
  regex, the Steam buildid by `(\d+)`, each falling back to `?`. No separator or `..` can
  reach the path.
- **Spawning a boss is destructive**, which is why it is gated twice: a confirmation naming
  it, then a cancellable countdown. A boss arrives at full health and hostile, and a summon
  in the wrong world state may simply be unwinnable.
- **A template copy is not `SetDefaults`.** The game builds an NPC by calling `SetDefaults`
  and then `NewNPC`, which do bookkeeping beyond populating fields — boss-specific statics
  (`NPC.plantBoss`, `golemBoss`), bestiary entries, and net sync. Copying the template gets
  the NPC itself right, which is what a trainer needs, but a boss that reads one of those
  statics could in principle behave oddly. Held back for in-game verification for exactly that
  reason, and **confirmed working**: a boss spawned this way fights normally. The concern is
  recorded rather than removed, because it is the first thing to suspect if some future boss
  does misbehave.
- **The slot's own arrays are kept, and their contents are not reset.** `ai[]` and the rest
  belong to the slot, not the template, so a spawned NPC starts with whatever the previous
  occupant left in them. Zeroing them was considered and not done: the two length-4 float
  arrays could not be identified as `ai` with confidence, and clearing the wrong one is
  worse than a stale AI value the game's own state machine overwrites.
- **Choosing a free slot is not atomic.** `_free_npc_slot` reads, then writes; the game's own
  spawner could claim the same slot in between. The window is microseconds and the loser is
  simply overwritten, so this is accepted rather than guarded.
- **Multiplayer is out of scope.** Nothing here syncs the new NPC to a server; it exists in
  the local game only.
- **Wiki article names are not always the display name.** Most match; some redirect, a few
  differ. Acceptable — the browser handles a redirect, and a miss lands on a search page.
- **6,186 entries in a Qt view** needs the same proxy-model treatment the recipe grid already
  uses; a naive widget-per-entry layout will not do.
- **Tooltip text contains formatting tokens** (`{0}`, colour codes). They are displayed as
  the game stores them unless trivially strippable. Five tooltips still carry an unresolved
  reference after the resolver generalisation; they render as the raw token.
- **"Critter" is a definition, not a flag.** `boss` and `townNPC` are real bytes on the
  template; nothing readable says "critter", so it is defined as an NPC that deals no damage.
  That puts the bunnies and birds (5 life, 0 damage) where a player expects them and misfiles
  the Target Dummy, which is the price of not inventing a flag.
- **The `NPC` kind is a fallback that should stay empty**, and its contents are a bug report.
  It showed 8 entries on first review: one sentinel (`NPCID.NegativeIDCount`) that was never
  an NPC, and seven Moss Hornet variants whose templates the scan was throwing away. Both are
  fixed; if that bucket ever repopulates, something upstream has broken.
- **`Main.npc` is found by a pinned offset with a fallback.** `MAIN_NPC_OFF` is a build
  constant like `MAIN_PLAYER_OFF` and `MAIN_RECIPE_OFF`, but it is validated on use — the
  array must be `maxNPCs + 1` long and its elements must agree on a vtable — and a failure
  falls back to scanning Main's static block for the only array that fits. That is what keeps
  it from becoming another constant that rots silently.
- **Wiki URLs are not percent-encoded.** The scheme and host are a hardcoded prefix and names
  come from the game's own localization, so the result is always a well-formed
  `https://terraria.wiki.gg/` URL handed to `xdg-open` as its own argv element — no shell,
  no injection. Punctuation in a name (apostrophes) is legal in a URL and the wiki resolves
  it.
- **A handful of sheets are not vertical strips at all**, and the grid rule only rescues
  those laid out evenly. The Wyvern segments, the four Pillars and the Flying Dutchman are
  single large sprites and are correctly left whole; Foxparks' 40 uneven row blocks are left
  whole too, which is the conservative failure. Icons render at 40px, so a slightly oversized
  source is cosmetic rather than a wrong picture.
- **Queen Slime's colour cannot be reproduced, and the attempt is shelved.** Its sheet is
  olive with a red core; in game it is pink. Unlike the coloured slimes it carries no tint —
  `NPC.color` is `(0, 0, 0, 0)` — so whatever recolours it lives in its draw code rather than
  in any field this project can read, and matching it would mean inventing a colour. The
  maintainer reviewed the sprite and accepted it as-is. Recorded so the next person does not
  re-derive the dead end; if it is ever worth doing, the lead is `Main.DrawNPCDirect`'s
  special-casing rather than anything on the NPC object.
- **Rollback.** `git revert`. The tab is additive and reads memory except when the user
  asks for something: `give_item` (pre-existing) and `spawn_npc`, which writes only into a
  `Main.npc` slot the game had marked unused. No patches, no code caves, no ledger entries,
  and nothing that outlives the session — a spawned NPC is as permanent as any other NPC.

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
- [x] The wiki button opens the correct article in the default browser — maintainer-confirmed
      in a running session
- [x] **Give** from a compendium entry puts the item in the first empty inventory slot —
      maintainer-confirmed in a running session
- [x] All tests pass headless (269); flake8 clean on changed code; security review recorded
- [x] The template cache is handed back to the invoking user rather than left root-owned
      in their cache directory (`proc.give_back_to_user`)
- [x] README updated; version bumped to 0.25.0 (maintainer confirmed)

### Phase 2 — NPC stats and spawning

- [x] NPC entries show life / damage / defense and a real kind (boss / town NPC / critter /
      monster) rather than the Phase 1 placeholder — all 759 NPCs classified: 22 bosses,
      60 town NPCs, 111 critters, 566 monsters, and **nothing left in the `NPC` fallback**
- [x] The stats come from the ContentSamples templates, not from `Main.npc[]`, so they are
      the game's base values rather than ones the world's difficulty has already scaled
      (a live Blue Slime reads 60 life where its template says 25)
- [x] Every offset used was verified against vanilla stats obtained independently of this
      project: Blue Slime 25/7/2, Zombie 45/14/6, Demon Eye 60/18/2, Eye of Cthulhu
      2800/15/12, King Slime 2000/40/10, Moon Lord 45000 life, critters 5 life / 0 damage
- [x] `NPC.active` identified and confirmed two ways: clear in all 647 templates, and set on
      exactly the live `Main.npc` slots
- [x] **Spawn** places the selected NPC at the configured distance behind the player —
      maintainer-confirmed working in-game
- [x] The spawn writes `active` last, so the game never sees a half-built NPC
- [x] The copy skips every reference field, so a spawned NPC never shares the template's
      arrays
- [x] Spawning a **boss** requires an explicit confirmation naming it, followed by a
      cancellable countdown; cancelling spawns nothing — maintainer-confirmed in-game. The
      template-copy spawn produces a working boss, so the concern that it might miss
      bookkeeping `SetDefaults`/`NewNPC` does (`NPC.plantBoss` and friends) did not
      materialise
- [x] The catalog holds only real NPCs: `NPCID`'s sentinels (`NegativeIDCount`, and the
      `None`/`None2`/`None3` placeholders for unoccupied ids) are dropped at extraction,
      taking `data/npcs.json` from 763 entries to 759
- [x] Templates are not lost to whatever is allocated beside them — the seven Moss Hornet
      variants were, and now carry their stats (Big Hornet Stingy 45/41/4, and so on)
- [x] No build-ledger entry is needed, because nothing is patched: the spawn is a field copy
      into a slot the game had marked unused. The NPC field offsets are recorded against
      build 1.4.5.7+24893155 in `npcs.py`
- [x] All tests pass headless (290); flake8 clean on changed code; security review recorded
- [x] README updated; version bumped to 0.26.0 (maintainer confirmed)

### UI consistency and progress (phase 3 review)

- [x] Every long operation reports through one shared progress bar under the tabs — the
      privileged catalog read, the row build, the recipe grid build and sprite extraction —
      so whichever tab starts the work, it is visible
- [x] The two big grids build in slices with the event loop pumped between them: nearly
      7,000 compendium rows took 1.3 s in one go and froze the window, which meant no bar
      could paint even when one was showing
- [x] The Compendium has a **Re-scan from game** button matching the Recipes tab's
      **Re-extract from game**, backed by `compendium --refresh`, which bypasses the
      per-build cache

### Phase 3 — NPC artwork

- [x] NPC entries show their sprite, decoded from `Content/Images/NPC_*.xnb` into the existing
      cache — 697 sheets, alongside the 6,193 item sprites, in ~24 s
- [x] Sheets are cropped to one frame using the game's own `Main.npcFrameCount` rather than a
      shape heuristic, verified against every sheet height measured
- [x] Grid sheets (Queen Slime 2x8, Deerclops 5 columns, Moon Lord 3x3, the Ogres) are cropped
      to their top-left cell, and the rule provably leaves 678 of 697 sheets untouched
- [x] A variant netID takes its sprite from its base type, not its id
- [x] The 11 NPCs the game tints (`NPC.color` at +0x1A8, e.g. Blue Slime's
      `(0, 80, 255, 100)`) render in their own colours rather than the neutral grey of the
      shared sheet, each as its own cached icon since the tint is per netID
- [x] Queen Slime's crop is a whole cell: its 16 frames are 2 columns of 8, so dividing the
      height by 16 gave half a slime. Columns are counted first and the height divided by the
      rows that implies
- [x] NPC and item cache filenames cannot collide
- [x] If the frame counts are not published yet, NPC sheets are skipped rather than cached
      whole, and the cache reports itself incomplete so the next run finishes the job

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

**Phase 2** gave NPCs the same treatment and then spawning. The spec planned a managed-call
injection of `NPC.SpawnOnPlayer`; recon killed that plan twice over. The method takes six
arguments rather than two (three call sites agree), and its four floats are `ai[0..3]`, not
the spawn offset the spec hoped for — but the real problem is that a managed call has no
trigger here, so it would have meant a stub polling a flag in a per-frame method. The project
already spawns fully-statted *items* by copying a ContentSamples template into a slot, and
every `Main.npc` slot is a real NPC object allocated at world load, so an NPC spawn is that
same trick one level up: copy the template, set the position, write `active` last. No code
injection, no cave, no ledger entry.

Reviewers for phase 2: `Service.spawn_npc` (the write order and why `active` is last),
`npcs.NPC_COPY_SPANS` (which fields must not be copied, and why), and
`CompendiumTab._spawn` / `_tick` (the boss gate).

**Phase 3** put artwork on the NPC rows. The item de-animator could not be reused — it infers
a strip from the sheet's shape, which is right for tall item strips and wrong for wide NPCs —
so the crop uses the game's own `Main.npcFrameCount` instead, an `int[697]` at Main-static
+0x0C34 that divides every sheet height measured. A second, conservative pass handles the
sheets that are grids rather than strips, because the frame count counts frames and not rows;
it changes 19 sheets of 697 and is prevented from slicing single sprites by requiring the
blocks to be evenly sized.

Reviewers for phase 3: `sprites._first_frame` and `_first_grid_cell` (the two rules and what
stops the second one over-reaching), and `Service._publish_npc_frame_counts` (why the counts
travel between processes as a file).

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

### Phase 2

- `tests/test_npc_content.py` (7): one template per netID; the negative variant netIDs
  survive, because ContentSamples is keyed on netID and those are exactly the entries
  `data/npcs.json` names; absurd netIDs rejected; a horde of same-type live NPCs is not
  mistaken for the template table; the real table wins over a smaller one-to-one run of live
  NPCs; the NPC vtable is the one the array elements agree on, and an array of the same
  length whose elements disagree is rejected.
- `tests/test_npc_spawn.py` (6): a non-boss spawns immediately with no countdown; a boss
  declined at the confirmation never spawns; a confirmed boss counts all the way down before
  spawning; cancelling mid-countdown spawns nothing; a second boss request replaces the first
  countdown rather than leaving two running behind one Cancel button; and the Spawn button is
  offered for NPCs only, reading "Spawn boss…" for a boss. Confirmed load-bearing: removing
  the boss check fails four of them.
- `tests/test_compendium_tab.py` gained two: Give is offered for items and withheld from NPCs
  (which must not key on the kind string, now that NPCs report Boss/Monster/…), and the kind
  dropdown widens for kinds added after it is first shown.
- `tests/test_npc_content.py` gained a regression test for the dropped templates: seven
  unique netIDs beside seven objects sharing one key must all survive. Confirmed
  load-bearing by restoring the whole-run rule and watching it fail.

Two defects came out of the maintainer's review of the finished tab, both from the same
symptom — a thinly populated `NPC` category. One was a sentinel constant that was never an
NPC; the other was the scan discarding a whole run of templates because seven default-state
objects had been allocated beside them. See the Technical and Risks sections.

Live, maintainer-verified: spawning works. Two Bunnies and a Zombie were placed into slots
12, 29 and 30 with correct netID, type, life, size and `whoAmI`, the game stayed up, and the
maintainer confirmed spawns arrive in-game from the tab.

### Phase 3

- `tests/test_npc_sprites.py` (15): strips cropped to one frame; a wide two-frame sheet
  cropped where the item rule provably would not; a genuinely single-frame sheet left alone;
  a few rows of padding tolerated; an absurd implied frame ignored; NPC and item cache paths
  cannot collide; frame counts round-trip and a missing file is not an error; a stale scope
  and a cache with no NPCs both re-extract; extraction with no counts writes no NPC icon at
  all; grid sheets cropped to the top-left cell; **and a single sprite with a detached piece
  left untouched**, which is the dangerous direction.
- `tests/test_compendium_tab.py` (+2): an NPC variant takes its sprite from its base type,
  not its netID (mutation-checked), and an item row still uses the item icon.

All of phases 1 and 2 are now maintainer-confirmed in a running session, including the two
buttons held open through phase 2 (Wiki and Give) and the boss gate — a boss spawned from the
template copy behaves correctly, so the `SetDefaults`/`NewNPC` bookkeeping concern recorded
under Risks did not materialise.
