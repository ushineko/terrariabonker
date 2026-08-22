"""The one place that knows the CLI subprocess contract the GUI depends on.

The GUI cannot call ``service`` in-process (it is unprivileged; memory access
needs root), so it reaches the same operations across a sudo subprocess boundary.
This module is the single definition of that boundary: each function returns the
CLI argv for an operation, and the parsers decode the ``--json`` replies. It is
deliberately toolkit-free (no PyQt) and imports nothing from the shell, so
``tests/test_view_parity.py`` can check every command it emits against the real
CLI parser — turning a contract drift (like a missing ``--json`` flag) into a
test failure instead of a runtime gap.
"""

from __future__ import annotations

import json

# --- read operations: (argv, parser) ----------------------------------------

def status_argv() -> list[str]:
    return ["status", "--json"]


def parse_status(raw: str) -> dict | None:
    try:
        return json.loads(raw.strip().splitlines()[-1])
    except (ValueError, IndexError):
        return None


def inventory_argv() -> list[str]:
    return ["inventory", "--all", "--json"]


def parse_inventory(raw: str) -> list[dict] | None:
    try:
        return json.loads(raw.strip().splitlines()[-1])
    except (ValueError, IndexError):
        return None


# --- mutation operations: argv builders -------------------------------------

def set_hp_argv(value) -> list[str]:
    return ["set-hp", str(value)]


def set_mana_argv(value) -> list[str]:
    return ["set-mana", str(value)]


def set_max_hp_argv(value: int) -> list[str]:
    return ["set-max-hp", str(value)]


def set_max_mana_argv(value: int) -> list[str]:
    return ["set-max-mana", str(value)]


def set_stack_argv(slot: int, value: int) -> list[str]:
    return ["set-stack", str(slot), str(value)]


def set_item_argv(slot: int, item_type: int, *, stack=None, damage=None,
                  auto_reuse=None, use_time=None, use_anim=None, pick=None,
                  tile_boost=None, defense=None, prefix=None) -> list[str]:
    argv = ["set-item", str(slot), str(item_type)]
    if stack is not None:
        argv += ["--stack", str(stack)]
    if damage is not None:
        argv += ["--damage", str(damage)]
    if auto_reuse is not None:
        argv += ["--auto-reuse", str(auto_reuse)]
    if use_time is not None:
        argv += ["--use-time", str(use_time)]
    if use_anim is not None:
        argv += ["--use-anim", str(use_anim)]
    if pick is not None:
        argv += ["--pick", str(pick)]
    if tile_boost is not None:
        argv += ["--tile-boost", str(tile_boost)]
    if defense is not None:
        argv += ["--defense", str(defense)]
    if prefix is not None:
        argv += ["--prefix", str(prefix)]
    return argv


def give_argv(item_type: int, stack: int) -> list[str]:
    return ["give", str(item_type), "--stack", str(stack)]


def fast_mining_argv() -> list[str]:
    return ["fast-mining"]


def long_reach_argv(tiles: int) -> list[str]:
    return ["long-reach", "--tiles", str(tiles)]


def patch_status_argv() -> list[str]:
    return ["patch", "status", "--json"]


def parse_patch_status(raw: str) -> dict | None:
    try:
        return json.loads(raw.strip().splitlines()[-1])
    except (ValueError, IndexError):
        return None


def patch_set_argv(cheat: str, on: bool, value: float | None = None) -> list[str]:
    if not on:
        return ["patch", "disable", cheat]
    argv = ["patch", "enable", cheat]
    if value is not None:
        argv += ["--value", str(value)]
    return argv


def extract_recipes_argv() -> list[str]:
    return ["extract-recipes"]


def freeze_argv(godmode: bool, mana: bool) -> list[str]:
    argv = ["freeze"]
    if godmode:
        argv.append("--godmode")
    if mana:
        argv.append("--mana")
    return argv


# Every CLI subcommand this client emits. The parity test asserts each one is a
# real subcommand of the CLI parser (and that --json reads are supported).
COMMANDS: set[str] = {
    "status", "inventory", "set-hp", "set-mana", "set-max-hp", "set-max-mana",
    "set-stack", "set-item", "give", "fast-mining", "long-reach", "freeze", "patch",
    "extract-recipes",
}

# argv samples exercised by the parity test to prove they parse cleanly.
SAMPLE_ARGVS: list[list[str]] = [
    status_argv(), inventory_argv(),
    set_hp_argv("max"), set_mana_argv(20), set_max_hp_argv(500), set_max_mana_argv(200),
    set_stack_argv(40, 999),
    set_item_argv(0, 3507, stack=1, damage=200, auto_reuse=1, use_time=8),
    set_item_argv(10, 3507, use_anim=8, pick=200, tile_boost=30),
    set_item_argv(20, 285, defense=5, prefix=25),   # accessory: defense + Warding
    set_item_argv(10, 0),                       # clear a slot
    give_argv(2, 999), fast_mining_argv(), long_reach_argv(25),
    freeze_argv(True, True),
    patch_status_argv(), patch_set_argv("mining", True, value=0.2),
    patch_set_argv("reach", False), patch_set_argv("fast_place", True),
    patch_set_argv("tool_reach", True, value=40), patch_set_argv("pickup", True, value=50),
    patch_set_argv("spawn_rate", True, value=40),
    extract_recipes_argv(),
]
