# Crafting-window hotkey — findings (SHELVED)

**Goal:** an in-game keybind to toggle the pop-out "Crafting Window" (natively you must
open the inventory and click the grid icon; its tooltip is "Crafting Window — Right
Click to toggle style"). The intended delivery is a **code-cave hook inside Terraria**
that reads a key (edge-triggered) and triggers the toggle — self-contained, no external
hotkey infra (works because the game has focus). See the reach/pickup work for the
code-cave + wildcard-AOB machinery this would reuse.

**Status: shelved.** Unlike reach/pickup, there is no reference CT entry and the toggle
resisted the fast techniques. This note is so a future attempt starts ahead.

## What we found (1.4.5.7)

- `Main.craftingUI : Terraria.UI.CraftingUI` (static, `Main`+0x234). But `CraftingUI` is
  a **static drawing helper class**, not an instance UI — it has only 2 static fields
  (`availableRecipeY`, `_lastFilter`) and no `Show`/`Hide`/`Toggle`/visibility bool. Its
  methods are draw helpers: `DrawRecipesGrid/List`, **`DrawGridToggle`**,
  `DrawCraftFromNearbyChestsToggle`, and `get_CraftingWindowTextKey` /
  `get_CraftingWindowTextTipKey` (the tooltip we saw).
- The toggle button + its click handling live in **`CraftingUI.DrawGridToggle`** (it
  owns the "Crafting Window" tooltip). But the toggle is **not** a plain `mov [flag]` in
  that method — it routes through helper `call`s to nearby CraftingUI methods
  (this session: ~0x29F06DA0 / 0x29F06DE0). So this is the "call a routine / manipulate
  an object" case, not a tidy flag flip.
- Other `Main` crafting fields (offsets, static): `craftingHide` bool (0x52C),
  `craftingAlpha` float (0x538), `nearbyCraftingMouseOver` (0x762),
  `GridToggleMouseOverCrafting` (0x763), `InGuideCraftMenu` (0xB29).
- **Red herring:** `Main._windowMover : Terraria.Graphics.WindowStateController` (0xD60)
  is the **OS window / multi-monitor** controller (`IsVisibleOnAnyScreen`,
  `TryMovingToScreen`, `ScreenDeviceName`), unrelated to the crafting UI.
- **Value-diff was inconclusive:** the absolute statics `DrawGridToggle` reads
  (0x0E1F1AB8 compared to 1 — looked like a window mode; 0x028730D4 a pointer;
  0x05BDBAB8 a gate) did **not** change when the window was toggled on. They are
  per-frame / transient render state, not the persistent toggle. (Those addresses are
  ASLR'd per launch — valid only for that session's pid.)

## Why the quick paths failed

No reference table naming the method (reach/pickup had one); no field whose name
matches a persistent "crafting window visible" flag; and the button's read-statics are
transient, so diffing them across a click showed nothing. The persistent state is
hidden inside an object the click handler calls into.

## Next steps for a future attempt

1. **Pin the toggle with a write-breakpoint.** Set a CE hardware BP on the state and
   click the icon to catch the exact writing instruction, then trace back to the
   variable/object. (Interactive — needs clicks on cue.) Alternatively, trace
   `DrawGridToggle`'s click branch through its helper calls (0x29F06DA0 / 0x29F06DE0
   this session) to find the toggle.
2. **Confirm it shows standalone.** Once the state is found, verify flipping it actually
   shows the window (and whether it needs the inventory open).
3. **Build the input hook** (the real prize, reusable): a code-cave hook in a per-frame
   method that reads `Main.keyState` for a chosen key, edge-detects it (own "was-down"
   byte in the cave), and triggers the toggle. If the toggle is a method, calling it
   from asm brings the mono calling-convention + JIT-trampoline caveat (a lazily-JIT'd
   method's entry only exists after first invocation — force-compile or ensure it has
   been called once). Generalize into a "bind key → do X in-game" capability; future QoL
   binds reuse it.

## Probes

- `poc_main_craft.lua` — enumerate `Main` crafting/window fields.
- `poc_craftingui.lua` — enumerate `Terraria.UI.CraftingUI` fields + methods.
- `poc_gridtoggle.lua` — disassemble `CraftingUI.DrawGridToggle` (the toggle button).
