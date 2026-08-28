# Spec 048: Auto-selling — whitelisted items sold into the piggy bank

**Status**: COMPLETE — shipped in v0.41.0. Verified in the running game on
1.4.5.7+24893155; 720 tests pass, security review clean. One caveat is recorded against the
last criterion: the crash-hardening is backed by extended live use without recurrence
rather than by a targeted stress test.

> **Note**: This work has no associated issue tracker ticket (personal utility).

Item influx from the other cheats outruns what a player can carry. Auto-sell turns a
whitelist of item types into coins continuously, with no shop UI: the player picks the
items in the panel, toggles the cheat on, and whitelisted items leave the inventory as
they arrive. Proceeds land in the Piggy Bank.

## Context — what the game actually does

Established with `tools/ilrecon` against `Terraria.exe` on 1.4.5.7; no live probing yet.

**Selling does not involve the NPC.** `Player::SellItem(Item, stack)` takes no NPC
argument and checks none. The shopkeeper gate lives entirely in the UI path
(`Main.OpenShop` → `Main.npcShop` → `ItemSlot.HandleShopSlot`). So the "a shop NPC must be
present" rule below is one this cheat imposes deliberately, not one the sell path enforces.

**The price is a small formula.** `GetItemExpectedPrice` reads
`Item.GetStoreValue()` (= `shopCustomPrice ?? Item.value`), then `SellItem` divides by 5,
floors to a minimum of 1, and multiplies by the stack. Two modifiers ride on top —
`Player.discountAvailable` (×0.8) and `currentShoppingSettings.PriceAdjustment` (NPC
happiness) — plus a buy-back bonus from `Main.shopSellbackHelper`. **None of the three are
implemented here**; see "Pricing" below.

**Which NPCs accept a sale is an extractable list, and this cheat does not use it.**
`GameContent.NPCInteractions::Initialize` registers one `OpenShop(npcType, shopIndex)` per
shop NPC (17, 19, 20, 38, 54, 107, 108, 124, 142, 160, 178, 207, 208, 209, 227 twice,
228, 229, 353, 368, 453, 550, 588, 633, 663 on this build; Guide, Nurse, Angler, Old Man
and the Tax Collector are absent). Recorded here because it was measured and may serve a
later feature. **No NPC gate ships**: requiring one breaks an established character taken
to a fresh world, which has no town NPCs yet and is exactly when the influx is worst.

**Coins are four item types with a carry rule.** `Player::DoCoins` reads 71 copper,
72 silver, 73 gold, 74 platinum: a stack that reaches 100 is re-typed to `type + 1` with
stack 1 and merged into an existing stack of that type. Replicating this is a few lines.

**The bank containers are ordinary chests.** `Player.bank`..`bank4` (Piggy Bank, Safe,
Defender's Forge, Void Vault) are `Chest` objects whose `item` array holds
`Chest.DefaultMaxItems` = 40 slots. They exist in memory whether or not the player owns the
matching container item, so ownership is a thing to check rather than something the memory
layout enforces.

**The bank's contents live on the character, not in the world.** A placed Piggy Bank tile
(`TileID.PiggyBank` = 29, already in `data/tiles.json`) is a portal to `Player.bank`, not
a store of its own; so is a Money Trough (`Main::TryInteractingWithMoneyTrough`, projectile
and item 3213), and both open the same container — `Player.chest == -2`. Coins deposited
while the player is in a world with no way to open the bank are therefore not *lost*, but
they are unreachable until the player opens one, which is the outcome to avoid.

## Design

No code patch, no stub, no arena slot. Everything here is reading and writing object
fields, so this is shaped like the existing polling cheats (`catch_tick`, `potion_tick`,
`bait_tick`): a `sell_tick` on `Service`, a `QTimer` in the panel, and a blocking `watch`
loop for the CLI, sharing one implementation.

`SellItem` itself is **not** called. Calling a mono method from an injected stub is the
riskiest operation in this codebase, and the entire transaction here is "clear a slot,
credit some coins" — arithmetic we can do correctly in Python and cover headlessly.

**Pricing.** Flat `max(1, value // 5) * stack`. The Discount Card and NPC happiness are
deliberately excluded: `PriceAdjustment` lives in `Player.currentShoppingSettings`, which
is only populated while a shop is open, so out of shop it holds stale data and would need
its own measurement before it could be trusted. The player is told the rate is the plain
shop rate without modifiers.

**No NPC gate.** Selling does not depend on any NPC being alive or nearby. The rule was
considered and dropped: an established character moved to a fresh world has no town NPCs,
and that is precisely the situation the cheat is for.

**Destination, and the reachability test that guards it.** Coins go to the Piggy Bank
(`bank`) only when the player can actually open it *in the current world*, which is true
in either of two cases:

1. they carry a Piggy Bank (item 87) or a Money Trough (item 3213) — either can be
   deployed anywhere, so the bank is reachable on demand; or
2. a Piggy Bank tile (type 29) is placed somewhere in the current world.

Case 1 is read from the inventory the tick already reads. Case 2 needs the world's tiles
and is answered **once per world load and cached**, never per tick — see Risks for the
cost, which is not yet measured. When neither holds, coins go to the player's inventory,
as does any overflow when the Piggy Bank has no room. The Safe is not used as a fallback:
it is a separate container with the same reachability problem and no advantage over the
inventory. Every sale reports where its coins went.

**A whitelisted item worth nothing is taken anyway.** `sell_price` returns 0 for it and no
coins are credited, but the item still goes. For a junk drop "sell it" and "bin it" are the
same request, and refusing would leave the one thing the player most wants gone sitting in
the bag. This makes the sell list a delete list for valueless items, which is why the README
says so plainly. The favorite override still applies and does not depend on the item being
worth anything.

**Whitelist, built from the panel's own inventory grid.** There is no separate item
picker: the player right-clicks a slot in the Inventory tab and toggles "auto-sell this
item type" for whatever occupies it. Left-click keeps opening the item editor, which is
what the cells already do. The whitelist keys on the **item type**, not the slot, so it
keeps applying as new stacks arrive. Whitelisted types are marked in the grid so the
player can see at a glance what will vanish.

**The list is shown in the Effects panel as well, and that is where items come off it.**
Adding from the grid is natural; removing from the grid is impossible, because a
whitelisted item is sold on the next tick and never stays in a slot long enough to
right-click a second time. Without a second view the whitelist is add-only and the only
way out is the CLI, which is not an answer for a panel feature. The Auto-sell box lists
every whitelisted type with its icon; double-clicking one removes it, whether or not the
player owns one.

The set is held in the pid-independent profile (`profile.json`), so it survives a restart
and participates in auto-restore like the other desired-config state. Toggling the cheat
on and off does not clear it.

**Favorited items are never sold.** A favorited stack is skipped regardless of the
whitelist. `ITEM_FAVORITED` (0x70) is already derived and in use. This matches vanilla,
which refuses to sell or trash a favorited item, and it gives the player a per-stack
override on top of a per-type whitelist: whitelist the ore, favorite the one stack being
saved for something. Note the flag means the opposite here to what it means in the passive
potions cheat, where a favorite is the opt-*in*; both follow the player's intent for their
own feature, but the panel text should be explicit so the two do not read as inconsistent.

## Recon results (measured 2026-08-28, game running, read-only)

Every offset below was asked of the mono runtime with `tools/monofields.py` and then
**checked against live data**, because a dump only proves the runtime agrees with itself.
`sudo python3 tools/monofields.py --verify` reported all existing constants correct in the
same session, so the instrument was known good.

| Offset | Field | How it was confirmed |
| --- | --- | --- |
| `0x124` | `Item.value` | Copper Coin 5, Silver 500, Gold 50000, Platinum 5000000 |
| `0x0E0` | `Player.bank` | `bank`..`bank4` at 0x0E0/E4/E8/EC; each `Chest.item` is 40 long |
| `0x008` | `Chest.item` | as above, on all four banks |

The same dump re-confirmed `favorited` 0x70, `type` 0x6C and `stack` 0x88 against the
constants `inventory.py` already pins — a free cross-check on the instrument.

**The coin values prove the price formula end to end.** `value // 5` yields exactly 1
copper, 1 silver, 1 gold and 1 platinum for the four coin types. `Item.value` is held at
five times the item's copper worth, which is why `SellItem` divides by 5; the division is
the sell rate, not a rounding artefact.

**Prefixes scale `Item.value`, so reading the live item prices modifiers for free.** Three
Slime Staffs in one inventory read 100000, 189062 and 58522. This was not anticipated:
pricing from the *type's* template would have mispriced every modified item. The tick reads
the field off the live item and must keep doing so.

### The placed-piggy-bank scan is cheap, contrary to the spec's earlier risk

The naive scan — one read per tile — measured **13.2 s** extrapolated across this 4200x1200
world, which is what the risk section predicted and would have killed the check.

It is not necessary. Tile objects are **pool-allocated with a fixed 24-byte stride and are
contiguous down a column**: a 1200-tile column holds 1197 deltas of exactly 24 and just
**3 contiguous runs**, with no null entries. Reading each run in one go and striding the
type field (`+0x08`, `ushort`) out of it scans the whole world in **0.15 s / ~16k reads**,
against 5,040,000 tiles — an 88x improvement, and it finds the same tiles the slow scan
did. A largest-size world is 4x the tiles, so ~0.6 s.

Confirmed against ground truth: the scan located Piggy Bank tiles at (2114, 311) and
(2115, 311) — two tiles, which is right, since a Piggy Bank is 2x1 furniture, not 2x2 —
with the player at (2135, 253). Both the slow and the fast scan agree on that pair.

**The contiguity is an allocator observation, not a guaranteed layout.** The implementation
verifies the deltas as it goes (as the probe does) and falls back to per-tile reads for any
run that does not hold, so a different allocation pattern costs speed and never correctness.

## Acceptance criteria

- [x] `Item.value` (0x124), `Player.bank` (0x0E0) and `Chest.item` (0x08) are declared
      once each in the owning module and pinned as literals in a test whose docstring
      records the derivation above. *(`selling.py`; `test_selling.py`
      `test_offsets_are_the_measured_ones`.)*
- [x] Pricing reads `value` from the **live item**, not from the type's template, so a
      modified item prices correctly. *(`test_two_items_of_one_type_can_price_differently`,
      using the three real Slime Staff values.)*
- [x] Sell price is `max(1, value // 5) * stack`, covered headlessly for a normal item, a
      `value` of 0..4 (floors to 1 per unit) and a full stack. *(Four coin types round-trip
      to their own face value, which is what pinned the formula.)*
- [x] Selling does not depend on any NPC. *(No NPC is read anywhere on the path; the whole
      of `test_auto_sell.py` runs against an image with no `Main.npc` planted at all, which
      is the strongest form this can take -- there is no NPC code to disable.)*
- [x] A tick sells every whitelisted stack and leaves every non-whitelisted stack
      untouched, verified slot by slot. *(`test_leaves_everything_not_whitelisted_alone`.)*
- [x] A whitelisted item worth 0 is taken and pays nothing; no coins are written anywhere
      for it. *(Mutation M19, skipping worthless items, kills a test.)* **Not yet verified
      in game** -- there were no zero-value items in the inventory when this was written.
- [x] A **favorited** stack of a whitelisted type is never sold, including when another
      stack of the same type is sold in the same round.
      including when it is worth nothing. *(`test_never_sells_a_favorited_stack`,
      `test_a_favorited_worthless_item_is_still_protected`,
      `test_favorite_protects_one_stack_while_the_rest_of_the_type_sells`; mutation M5,
      ignoring the flag, kills both.)*
- [x] Favorite protection does not depend on the order the two were set in. *Reduced from
      the original wording after implementation: the flag is read off the live item inside
      the round, so the two orderings are the same code path and a second test would assert
      the same execution twice. Recorded rather than quietly dropped.*
- [x] Coins land in the Piggy Bank, merged and carried the way `DoCoins` does it.
      *(`test_coins_merge_into_an_existing_stack_and_carry_at_100`; `coin_stacks` is
      value-conserving across eight magnitudes.)*
- [x] Coins go to the **inventory** when the bank cannot be reached in this world.
      *(All four states covered: carrying a Piggy Bank, carrying a Money Trough, one placed
      in the world, and neither. Mutation M9 kills the carried check.)*
- [x] The placed-tile answer is computed once per world load, not per tick, and recomputed
      when the world changes. *(Mutations M10 and M11 -- never caching, and never
      invalidating -- each kill a test.)*
- [x] The world scan bulk-reads contiguous runs and falls back to per-tile reads when a
      run cannot be read in one go. *(The first version of this test was WEAK -- a mutant
      deleting the fallback survived it, because relocating one tile leaves a one-tile run
      the fast path still reads. Rewritten around a short bulk read; the mutant now dies.)*
- [x] A full Piggy Bank overflows to the inventory; a full inventory sells nothing and
      reports it. **No path destroys an item without crediting its coins.** *(Coins are
      credited before the item is taken; mutation M4, reversing that order, fails three
      tests.)*
- [x] The whitelist persists in `profile.json` and is independent of the cheat's on/off
      switch. *(`test_the_whitelist_survives_a_restart`.)*
- [x] The whitelist is edited from the Inventory tab: right-clicking a slot toggles
      auto-sell for the type in it, left-click still opens the item editor, and a
      whitelisted type is marked with a dashed border. Right-clicking an empty slot does
      nothing. *(`test_sell_panel.py`; the grid follows the worker's reply, not the click,
      so a write that did not land cannot leave the cell lying.)*
- [x] An item can be taken off the list **without owning one** — the case the grid cannot
      serve, since a whitelisted item is sold before it can be right-clicked again.
      *(`test_an_item_can_be_removed_without_ever_appearing_in_the_inventory`; mutations
      M21b and M22 each kill a test.)*
- [x] The CLI lists, adds to and removes from the same whitelist through the same service
      methods. *(`sell`, `sell-tick`, `sell-list`; the view-parity test forced the new argv
      builders to be registered, which is exactly what it is for.)*
- [x] Turning the cheat off stops all selling immediately; there is nothing to disarm,
      because nothing is armed -- no stub, no patch, no arena slot.
- [x] Every new test is mutation-checked. *(Eleven mutants, M1-M11. One survivor, M1,
      was a weak test and is recorded above; it was rewritten and the mutant now dies.)*
- [x] Coins are promoted the way ``DoCoins`` does it: 100 of a denomination becomes 1 of
      the next one up, merged into an existing stack where there is one, and platinum is
      left alone. *(Added AFTER the first live run — see below. Mutations M12 and M13 each
      kill a test.)*
- [x] **Re-opened after the crash, and closed on weaker evidence than it was opened on.**
      The three fixes were exercised by extended live use after the restart -- whitelisting
      from the grid, rounds selling, the panel rebuilt twice -- with no recurrence. That is
      absence of a recurrence, not a targeted stress test: a chest opened and closed
      repeatedly with auto-sell running was never run deliberately. Recorded this way on
      purpose, because "it did not crash again" is the evidence that was actually
      collected.
- [x] Verified in the running game before the crash-hardening: whitelisted items sell, the
      coins are in the piggy bank, and the total matches. *(Slime Staff
      (1309), four across three slots including two prefixed ones. Bank 13,738,337 ->
      13,827,853 copper, **+89,516 exactly as reported**; all three slots left empty and no
      Slime Staff remaining in the inventory.)*

### The crash, and three defects it exposed

The game died at 14:18:38 while the maintainer opened a chest with auto-sell running. There
is **no proof of cause**: the managed crash log has no entry for that day and `dmesg` has no
segfault line, which is what a native write into reclaimed memory looks like, but is equally
what several other things look like. What follows is what auditing the code found, not a
diagnosis of the crash.

**1. Writes went through addresses read earlier.** `_credit_coins` and `_normalize_coins`
read every slot in a container, then wrote back to the addresses that read returned -- with
dozens of syscalls in between, twice a second. `AGENTS.md` states the rule this breaks:
*locate by identity on every access, never by a cached address; mono's GC moves objects, and
a stale pointer writes into whatever now lives there.* Opening a chest is exactly the sort
of allocation that moves objects. Every write now goes through `Container.write`, which
re-resolves the slot and refuses when the type or stack there is not what the caller
believed -- an unexpected pre-value aborts the round instead of being overwritten.

**2. Coins could be written outside the slots the game uses for them.** The round treated
`Player.inventory` as 59 uniform slots. `SellItem`'s own IL bounds its coin loop at slot 53,
and slot 58 is not a normal slot -- the panel's grid hides it. Selling now walks 0..57 and
coins go only to 0..53.

**3. The round wrote into containers while their UI was open.** A guard was added on
`Player.chest` (0x0A8C, -1 none, -2 piggy bank) and then **removed at the maintainer's
direction** after they found what it cost: Terraria keeps the piggy bank *open* while the
player is in range and closes it when they walk away, so the guard meant nothing sold while
standing at the bank -- which is exactly where loot gets sorted. The guard was also
over-scoped: it blocked writing to *any* container when the race concerns only the one the
game is drawing.

The offset was validated live before the guard came out (reads -1 with nothing open), so it
is recorded here for whoever wants it. The race remains unmeasured; defect 1 is the likelier
cause of the crash and is fixed on its own merits.

### What the first live run found

The sale itself was right first time -- 89,516 copper reported, 89,516 gained, the prefixed
staffs priced individually (values 100000 / 189062 / 58522 giving 20000 x2 / 37812 / 11704).

It left one state the game would never leave. Merging 17 silver onto an existing stack of
83 stopped at 100, where ``DoCoins`` promotes a full stack to 1 of the next denomination.
The bank was *worth* the right amount, so every value-conservation test passed and the
divergence was visible only by reading the coins back and noticing "100 silver" is not a
thing Terraria writes. ``_normalize_coins`` now does what ``DoCoins`` does, and was verified
against that very stack in the live bank: silver 100 -> gone, gold 81 -> 82, total
13,827,853 before and after, **delta 0**.

Worth recording as a class of bug rather than a bug: a credit can be arithmetically correct
and still leave a shape the game does not produce, and no amount of "is the total right"
testing will find it.

## Risks & Assumptions

- **Coins are created, not transferred.** The NPC pays nothing; this mints the coins into
  the bank. That is what every other cheat here does (items are minted too), but it means a
  bug inflates the save rather than merely misplacing an item. The value-conservation test
  above is the guard on the sell side; there is no guard on the mint side by design.
- **Irreversible to the save.** A sold item is gone from that world's player file once it
  saves. The panel must make the whitelist unambiguous before it can run, and the README
  must say the effect is permanent. Favorite protection is the player's per-stack undo for
  a whitelist entry that was too broad, so it is a correctness requirement, not a nicety.
- **Stale-address hazard.** Bank chests and their item arrays are GC-movable like
  everything else, so each tick re-locates by identity; an unexpected pre-value aborts the
  tick rather than being written through.
- **Rollback**: a single feature commit, revertible on its own. The cheat ships switched
  off with an empty whitelist, so a revert changes nothing for a player who never enabled it.
- **The world scan was the open feasibility question and is now closed** — 0.15 s measured,
  see Recon results. The `Player.chest == -2` fallback that was held in reserve is not
  needed and is not implemented.
- **The scan's speed rests on an allocator behaviour.** If a future build allocates tiles
  differently the scan degrades toward the 13.2 s naive cost rather than breaking. That is
  survivable for a once-per-world-load check, but it is a reason to keep the check off the
  tick path permanently, not merely for now.
- **Assumption to confirm live**: writing coins into `bank` while the Piggy Bank UI is
  *open* may race the game's own slot handling. If it does, the tick skips while the
  container is open. Untested — this is called out, not claimed either way.

## Alternatives Considered

- Calling `Player::SellItem` from a stub: exact prices and coin handling for free, rejected
  because it is the highest-crash-risk mechanism available and the formula it hides is four
  lines of arithmetic.
- Requiring a shop NPC in the world: rejected because it breaks a character taken to a
  fresh world.
- Falling back to the Safe before the inventory: rejected because the Safe has the same
  reachability problem as the Piggy Bank and offers nothing the inventory does not.
- Crediting `PriceAdjustment` and the Discount Card: rejected for now because
  `currentShoppingSettings` is only meaningful inside an open shop and would need its own
  measurement first.
