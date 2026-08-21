# terrariabonker

A from-scratch live-memory trainer and item editor for **Terraria 1.4.5.7**
(Steam appid 105600), the Windows build running under Proton (wine-mono). It
finds the player in memory with no hardcoded address, then reads, edits and
freezes player state and inventory items by reading and writing
`/proc/<pid>/mem`. A PyQt6 control panel drives the same operations as the CLI.

No Cheat Engine required at runtime: discovery, value edits, and the *code
patches* all run from Python over `/proc`. Cheat Engine is used only off to the
side, to re-derive patch offsets after a game update — see
[Two kinds of cheat](#two-kinds-of-cheat-value-edits-and-code-patches).

*This edits your own single-player game in memory. It writes nothing to disk and
holds no state; stopping it ends every effect.*

## Table of Contents

- [What it can do](#what-it-can-do)
- [Two kinds of cheat](#two-kinds-of-cheat-value-edits-and-code-patches)
- [Requirements](#requirements)
- [Installation](#installation)
- [Usage (CLI)](#usage-cli)
- [Usage (GUI)](#usage-gui)
- [Version safety](#version-safety)
- [How it works](#how-it-works)
- [Project layout](#project-layout)
- [Testing](#testing)
- [Safety](#safety)

## What it can do

- **Godmode** and **infinite mana** — high-frequency freezes that hold HP/mana.
- **Stat edits** — set current and permanent-max HP and mana.
- **Grid item editor** — an inventory grid that mirrors the in-game layout
  (Hotbar / Inventory / Coins / Ammo). Click a slot to open an editor dialog for
  item **type** (ItemID), **stack**, **damage**, **auto-reuse** (auto-attack),
  **use-speed**, **use-animation**, **pickaxe power**, and **placement reach**;
  click an empty slot to place a fully-statted item, or clear a slot. Accessories
  carried in the inventory edit through the same path.
- **Fast mining** — sets every pickaxe to Picksaw-tier speed and power in one click.
- **Long reach** — extends placement distance on all items.
- **Give items by name** — the editor dialog's item field autocompletes over all
  6,195 item names, extracted from the game's own `Terraria.exe` for the exact
  1.4.5.7 build.
- **Code-patch cheats** — global mining speed, item-independent placement reach, and
  fast placement: things a value-write can't hold, applied by patching the game's JIT
  code through `/proc` (derived with Cheat Engine, but no CE needed at runtime).

## Two kinds of cheat: value edits and code patches

Terraria recomputes some player fields every frame in `ResetEffects` and reads them
within that same frame, so a plain `/proc` value-write loses the race (the same
reason a single lethal hit can kill through a health freeze). Those need a **code
patch** — remove the reset (or force a constant at the read site) so the value holds.

Crucially, applying a patch is *also* just a `/proc` byte-write, so this trainer does
both — no Cheat Engine at runtime:

| Value edits (persistent fields) | Code patches (frame-reset fields) |
| :--- | :--- |
| HP, mana, godmode-by-freeze, max stats | **Global mining speed** (`pickSpeed`) |
| Item stack / type / damage / auto-reuse | **Placement reach** (`blockRange`, item-independent) |
| Per-item use-speed (`Item.useTime`), pick power | **Fast placement** (`ApplyItemTime` timing) |
| Placement distance per item (`Item.tileBoost`) | *(planned: true damage-immunity, pickup range)* |

The code-patch sites were **derived with Cheat Engine's mono dissector** (see
`ce/README.md`), but the trainer locates them by AOB and patches them itself. CE is
only needed to re-derive the patterns after a game update.

## Requirements

- Terraria launched as the **Windows build under Proton** (not the native Linux
  build). Force Proton in Steam under Properties, Compatibility.
- Python 3.10+ (system Python; `/usr/bin/python3`, not conda)
- `numpy` and `PyQt6` (Arch/CachyOS: `python-numpy python-pyqt6`)
- `sudo`. Game memory access needs root (`kernel.yama.ptrace_scope=1`). The CLI
  re-execs under sudo; the GUI stays unprivileged and shells each action out
  through sudo. Passwordless sudo makes both seamless.

## Installation

```bash
cd ~/git/ag-scripts/terrariabonker
./install.sh
```

Installs a `terrariabonker` symlink into `~/.local/bin` and a desktop entry that
launches the GUI. Nothing is copied; the entry points at this checkout, so
`git pull` updates it. Remove with `./uninstall.sh`.

## Usage (CLI)

```bash
terrariabonker status              # find the player, show HP/mana
terrariabonker version             # detected build and compatibility

terrariabonker godmode             # pin HP to max; Ctrl-C to stop
terrariabonker freeze --godmode --mana
terrariabonker set-hp max          # heal to full
terrariabonker set-max-hp 500

terrariabonker inventory           # list your inventory
terrariabonker set-stack 40 9999   # set a slot's quantity
terrariabonker set-item 0 3507 --damage 200 --auto-reuse 1   # edit a slot
terrariabonker fast-mining         # speed + power on all pickaxes
terrariabonker long-reach --tiles 25

terrariabonker patch status                 # code-patch cheats: on/off
terrariabonker patch enable mining --value 0.15   # global mining speed (pickSpeed; lower = faster)
terrariabonker patch enable reach --value 30      # placement reach (extra tiles)
terrariabonker patch disable fast_place           # fast placement (ApplyItemTime)
```

`--value` overrides the enabled value; omit it to use the default (mining `0.2`,
reach `20`). `fast_place` carries no value.

Every memory-touching command elevates through sudo first and is gated on the
game build (see below).

## Usage (GUI)

Launch from the application menu (**terrariabonker**) or `terrariabonker gui`.

- **Launch Terraria** — a header button starts the game through Steam when it
  isn't already running. It runs unprivileged (Steam refuses to run as root), so
  unlike the memory actions it does not go through sudo.
- **Trainer** tab — godmode / infinite-mana toggles, heal / refill, max HP/mana,
  fast-mining, long-reach, and a **Code patches** section: checkbox toggles for
  the frame-reset cheats (global mining speed, placement reach, fast placement)
  with tunable value spinboxes for mining and reach. No Cheat Engine at runtime;
  a game restart clears them.
- **Inventory** tab — a grid mirroring the in-game inventory (Hotbar / Inventory /
  Coins / Ammo). Each cell shows an abbreviated name and stack, is tinted by the
  item's **rarity** (Terraria's own tooltip colours), and has a full-detail tooltip.
  Click a filled cell to edit it in a dialog, or an empty cell to place a
  fully-statted item (item field autocompletes over all item names); the dialog can
  also clear a slot.

The window runs unprivileged and runs each action as a short `sudo` CLI call, so
Qt never runs as root.

## Version safety

The offsets are specific to one game build. `version.py` reads the running game's
version string and Steam buildid and compares them to the known-good build:

| Situation | Behaviour |
| :--- | :--- |
| Exact match (`1.4.5.7`, buildid `24825745`) | proceeds |
| Hotfix only (`1.4.5.8`) or buildid drift | warns, proceeds |
| Major/minor/patch differs (`1.4.6`, `1.5.x`) | refuses without `--force` |

The locator also fails safe: a shifted layout matches nothing rather than writing
to a wrong address. After an update, see [docs/discovery.md](docs/discovery.md).

## How it works

The player is located by scanning writable memory for the six-int32 life/mana
block, validated by Terraria invariants and a real character-name string.
wine-mono does not move objects, so a located address stays valid for the world's
lifetime. The inventory is reached structurally (`Player.inventory[]`, a 59-slot
`Item[]`), because value-scanning a stack count only finds downstream caches. The
full derivation, and how to rebuild the offsets after a game update, is in
**[docs/discovery.md](docs/discovery.md)**.

## Project layout

```
terrariabonker/
├── terrariabonker.py           thin entry point
├── terrariabonker/
│   ├── proc.py                 /proc read/write, PID detect, sudo self-elevation
│   ├── locate.py               from-scratch player locator (signature + name anchor)
│   ├── player.py               player stat offsets and a read/write handle
│   ├── inventory.py            inventory array + Item field editor
│   ├── trainer.py              the freeze engine (godmode, infinite mana)
│   ├── patcher.py              code-patch cheats (AOB resolve + patch via /proc)
│   ├── service.py              view-neutral core shared by CLI and GUI
│   ├── version.py              build detection and the compatibility gate
│   ├── names.py                ItemID -> name lookup for the item browser
│   ├── data/items.json         ItemID name map (extracted from Terraria.exe)
├── tools/extract_item_names/   dotnet tool that regenerates items.json from the exe
│   ├── cli.py                  argparse front end
│   └── gui/
│       ├── main_window.py      PyQt6 control panel
│       ├── client.py           CLI-argv builders + output parsers (CLI/GUI parity)
│       ├── invgrid.py          Qt-free grid layout / label / tooltip helpers
│       └── item_dialog.py      modal per-item editor
├── docs/discovery.md           how the offsets were derived and how to rebuild them
├── ce/                         Cheat Engine spike: how the code-patch sites were found
├── specs/                      feature specs
└── tests/                      unittest suite (headless, no game, no root)
```

## Testing

```bash
QT_QPA_PLATFORM=offscreen /usr/bin/python3 -m pytest tests/ -q
```

Tests run against an in-memory fake process, so they need neither the game nor
root and never touch real game memory.

## Safety

- The tool writes nothing to disk and keeps no state. Stopping it ends every
  effect; edited values are ordinary game values the game keeps managing.
- Godmode is a freeze, not a code patch. A single hit larger than current HP can
  still register before the next rewrite; raise max HP for headroom.
- **`Item.consumable`** is deliberately not exposed for editing: setting it on a
  stack-of-one item makes the game eat it on use. If an item is damaged by an
  edit, the game keeps a pristine template of every item in memory
  (`ContentSamples`) that can be cloned back — see docs/discovery.md.
- Terraria is single-player here. Edited characters and worlds are yours.
- The version gate exists because running stale offsets on a changed build could
  write into the wrong fields. Do not `--force` past an incompatible build unless
  you have re-derived the offsets.
