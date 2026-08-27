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
                  tile_boost=None, defense=None, prefix=None,
                  expect_type=None) -> list[str]:
    argv = ["set-item", str(slot), str(item_type)]
    if expect_type is not None:
        argv += ["--expect-type", str(expect_type)]
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


def compendium_argv(refresh: bool = False) -> list[str]:
    return ["compendium", "--json"] + (["--refresh"] if refresh else [])


def parse_compendium(raw: str) -> dict | None:
    """Normalize the catalog feed; None when the command failed or produced nothing."""
    try:
        data = json.loads(raw.strip().splitlines()[-1])
    except (ValueError, IndexError):
        return None
    return data if isinstance(data, dict) and "items" in data else None


def give_argv(item_type: int, stack: int) -> list[str]:
    return ["give", str(item_type), "--stack", str(stack)]


def build_check_argv() -> list[str]:
    return ["build-check", "--json"]


def accept_build_argv(decision: str, failed=()) -> list[str]:
    argv = ["accept-build", decision, "--json"]
    if failed:
        argv += ["--failed", *sorted(failed)]
    return argv


def parse_build_check(raw: str) -> dict | None:
    """The build report; None when the command failed or produced nothing."""
    try:
        data = json.loads(raw.strip().splitlines()[-1])
    except (ValueError, IndexError):
        return None
    return data if isinstance(data, dict) and "build" in data else None


def spawn_npc_argv(net_id: int, distance: int) -> list[str]:
    return ["spawn-npc", str(net_id), "--distance", str(distance)]


def fast_mining_argv() -> list[str]:
    return ["fast-mining"]


def long_reach_argv(tiles: int) -> list[str]:
    return ["long-reach", "--tiles", str(tiles)]


def potions_argv(min_stack: int = 1) -> list[str]:
    """One renewal round. Deliberately not ``--watch``: the worker must not block, so the
    GUI drives the cadence from its own timer, as it does for vein watching."""
    return ["potions", "--json", "--min-stack", str(min_stack)]


def fishing_argv(keep: int = 30, kit: bool = True) -> list[str]:
    """One round: hand out the kit if asked, then top bait up. Never ``--watch`` — the
    worker must not block, so the GUI drives the cadence from its own timer."""
    argv = ["fishing", "--json", "--keep", str(keep)]
    if not kit:
        argv.append("--no-kit")
    return argv


def fishing_buffs_argv(power: bool, sonar: bool, crate: bool) -> list[str]:
    """One round of holding the fishing potion effects up. Never ``--watch``: the worker
    must not block, so the GUI owns the cadence, as it does for bait and vein watching."""
    argv = ["fishing-buffs", "--json"]
    for on, flag in ((power, "--power"), (sonar, "--sonar"), (crate, "--crate")):
        if on:
            argv.append(flag)
    return argv


def catch_argv(recast: bool = False) -> list[str]:
    """One slice of auto-catch. Never the blocking ``catch`` loop, for the same reason as
    the bait round: the worker must not block, so the GUI owns the cadence."""
    argv = ["catch-tick", "--json"]
    if recast:
        argv.append("--recast")
    return argv


def catch_stop_argv() -> list[str]:
    return ["catch-stop", "--json"]


def fishing_power_argv(power: int) -> list[str]:
    return ["fishing", "--json", "--no-kit", "--power", str(power)]


def fishing_restore_argv() -> list[str]:
    return ["fishing", "--json", "--no-kit", "--restore"]


def patch_status_argv() -> list[str]:
    return ["patch", "status", "--json"]


def parse_patch_status(raw: str) -> dict | None:
    """Normalize ``patch status --json`` into ``{"on": {name: bool},
    "values": {name: number}}``. Accepts the legacy flat ``{name: bool}`` shape
    (older CLI) by wrapping it with empty values."""
    try:
        data = json.loads(raw.strip().splitlines()[-1])
    except (ValueError, IndexError):
        return None
    if isinstance(data, dict) and "on" in data:
        return {"on": dict(data.get("on") or {}),
                "values": dict(data.get("values") or {})}
    return {"on": dict(data), "values": {}}


def patch_set_argv(cheat: str, on: bool, value: float | None = None) -> list[str]:
    if not on:
        return ["patch", "disable", cheat]
    argv = ["patch", "enable", cheat]
    if value is not None:
        argv += ["--value", str(value)]
    return argv


def restore_argv() -> list[str]:
    return ["restore", "--json"]


def parse_restore(raw: str) -> dict | None:
    try:
        return json.loads(raw.strip().splitlines()[-1])
    except (ValueError, IndexError):
        return None


def extract_recipes_argv() -> list[str]:
    return ["extract-recipes"]


def extract_sprites_argv(force: bool = False) -> list[str]:
    return ["extract-sprites"] + (["--force"] if force else [])


def freeze_argv(godmode: bool, mana: bool) -> list[str]:
    argv = ["freeze"]
    if godmode:
        argv.append("--godmode")
    if mana:
        argv.append("--mana")
    return argv


# Every CLI subcommand this client emits. The parity test asserts each one is a
# real subcommand of the CLI parser (and that --json reads are supported).
# Every argv builder here, with the subcommand it is expected to emit and a sample call.
# The expected command is written down rather than read back off the sample: deriving it
# from argv[0] would only prove argv[0] equals itself, so a builder quietly switching to
# another valid command would pass. The parity test walks this: a builder that
# is not represented fails the suite, so a new command cannot be added on the GUI side
# without proving it parses on the CLI side.
#
# It was a hand-written set of command names until 2026-08-26, and it had gone stale --
# catch-tick, catch-stop, fishing-buffs, build-check, accept-build, restore and
# extract-sprites were all missing, so the parity test silently stopped covering two
# releases' worth of new commands while still reading like coverage.
SAMPLE_ARGVS: list[tuple[str, str, list[str]]] = [
    ("status_argv", "status", status_argv()),
    ("inventory_argv", "inventory", inventory_argv()),
    ("set_hp_argv", "set-hp", set_hp_argv("max")),
    ("set_mana_argv", "set-mana", set_mana_argv(20)),
    ("set_max_hp_argv", "set-max-hp", set_max_hp_argv(500)),
    ("set_max_mana_argv", "set-max-mana", set_max_mana_argv(200)),
    ("set_stack_argv", "set-stack", set_stack_argv(40, 999)),
    ("set_item_argv", "set-item",
     set_item_argv(0, 3507, stack=1, damage=200, auto_reuse=1, use_time=8)),
    ("set_item_argv", "set-item",
     set_item_argv(10, 3507, use_anim=8, pick=200, tile_boost=30)),
    ("set_item_argv", "set-item", set_item_argv(20, 285, defense=5, prefix=25)),  # Warding
    ("set_item_argv", "set-item", set_item_argv(10, 0)),               # clear a slot
    ("give_argv", "give", give_argv(2, 999)),
    ("spawn_npc_argv", "spawn-npc", spawn_npc_argv(46, 25)),
    ("compendium_argv", "compendium", compendium_argv()),
    ("compendium_argv", "compendium", compendium_argv(refresh=True)),
    ("build_check_argv", "build-check", build_check_argv()),
    ("accept_build_argv", "accept-build", accept_build_argv("accepted")),
    ("accept_build_argv", "accept-build",
     accept_build_argv("degraded", failed=("mining",))),
    ("fast_mining_argv", "fast-mining", fast_mining_argv()),
    ("long_reach_argv", "long-reach", long_reach_argv(25)),
    ("freeze_argv", "freeze", freeze_argv(True, True)),
    ("patch_status_argv", "patch", patch_status_argv()),
    ("patch_set_argv", "patch", patch_set_argv("mining", True, value=0.2)),
    ("patch_set_argv", "patch", patch_set_argv("reach", False)),
    ("patch_set_argv", "patch", patch_set_argv("auto_use", True)),
    ("potions_argv", "potions", potions_argv()),
    ("potions_argv", "potions", potions_argv(30)),
    ("fishing_argv", "fishing", fishing_argv()),
    ("fishing_argv", "fishing", fishing_argv(50, kit=False)),
    ("fishing_power_argv", "fishing", fishing_power_argv(255)),
    ("fishing_restore_argv", "fishing", fishing_restore_argv()),
    ("fishing_buffs_argv", "fishing-buffs", fishing_buffs_argv(True, True, True)),
    ("fishing_buffs_argv", "fishing-buffs", fishing_buffs_argv(True, False, False)),
    ("catch_argv", "catch-tick", catch_argv()),
    ("catch_argv", "catch-tick", catch_argv(recast=True)),
    ("catch_stop_argv", "catch-stop", catch_stop_argv()),
    ("restore_argv", "restore", restore_argv()),
    ("extract_recipes_argv", "extract-recipes", extract_recipes_argv()),
    ("extract_sprites_argv", "extract-sprites", extract_sprites_argv()),
    ("extract_sprites_argv", "extract-sprites", extract_sprites_argv(force=True)),
]

#: The CLI subcommands this module can emit. Derived, never hand-maintained.
COMMANDS: set[str] = {cmd for _name, cmd, _argv in SAMPLE_ARGVS}


def replies(raw: str) -> list[dict]:
    """Every JSON object the worker sent back, in order.

    The worker answers with a line of JSON, sometimes preceded by human-readable output,
    so a reply is parsed by walking the lines and keeping the ones that decode. Six tick
    handlers in the panel each had their own copy of this loop -- the contract this module
    exists to own, restated six times where it could drift six ways.
    """
    # Only lines starting with "{" are considered, so anything that decodes is an object;
    # a JSON array or bare string on its own line is skipped before parsing rather than
    # filtered afterwards. (An isinstance guard here was unreachable for that reason.)
    out = []
    for line in raw.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            got = json.loads(line)
        except ValueError:
            continue
        out.append(got)
    return out


def error_in(raw: str) -> str | None:
    """The worker's error message, or None.

    The privileged side reports failure as an ``[ERROR] ...`` line (see `cli._serve_reply`),
    which is the only contract there is -- so it is decoded here rather than string-matched
    at four call sites, one of which was written against a `{"error": ...}` shape that the
    worker never sends.
    """
    for line in raw.splitlines():
        line = line.strip()
        if line.startswith("[ERROR]"):
            return line[len("[ERROR]"):].strip() or "the worker reported an error"
    return None


def build_banner(status: dict | None, known_build: str) -> str:
    """One line describing how the running build stands, or "" when all is well.

    Cheat availability belongs in the UI, not in a log line repeated on every retry: a
    cheat can be unavailable (its AOB does not resolve here) or merely unverified (it
    resolves, but was never confirmed on this exact build). Returns HTML for the banner.
    """
    if not status:
        return ""
    detail = status.get("detail") or {}
    if not detail:
        return ""
    build = status.get("build") or "?"
    broken = {n: d.get("reason", "") for n, d in detail.items() if not d.get("available")}
    unverified = [n for n, d in detail.items() if d.get("available") and not d.get("verified")]
    if not broken and not unverified:
        return ""
    parts = []
    if broken:
        listed = "; ".join(f"<b>{n}</b> — {r}" for n, r in sorted(broken.items()))
        parts.append(f"{len(detail) - len(broken)} of {len(detail)} cheats resolve on this "
                     f"build. Unavailable: {listed}.")
    if unverified:
        parts.append(f"Build <code>{build}</code> is not the build these AOBs were verified "
                     f"on (<code>{known_build}</code>): {len(unverified)} cheat(s) resolve "
                     "but are unproven here.")
    return " ".join(parts)


def restore_summary(report: dict | None, _unused=None) -> list[str]:
    """Plain-language lines for what auto-restore could not finish, or [] when it did.

    Cheats and item edits fail for unrelated reasons and must not be reported as one lump:
    a cheat is missing because its anchor does not resolve on this build, while an item
    edit simply has nothing to apply to.

    An item you are not carrying is **not** a failure. That distinction is the whole point
    of spec 038 — the old wording ("no longer hold the item they were saved for; left
    alone rather than overwritten") read like a warning, and was produced mostly by edits
    that never needed restoring in the first place.
    """
    if not report:
        return []
    out = []
    pending = list(report.get("pending") or [])
    if pending:
        out.append(f"[auto-restore] gave up on {len(pending)} cheat(s) after retrying: "
                   f"{', '.join(sorted(pending))} — see the notice above for why")
    skipped = list(report.get("skipped") or [])
    cheats = [s.split(":", 1)[1] for s in skipped if s.startswith("cheat:")]
    if cheats:
        out.append(f"[auto-restore] {len(cheats)} cheat(s) refused: {', '.join(sorted(cheats))}")
    absent = list(report.get("absent") or [])
    if absent:
        from terrariabonker import names
        labels = ", ".join(sorted(names.label(int(t)) for t in absent))
        out.append(f"[auto-restore] {len(absent)} saved item edit(s) waiting for an item "
                   f"you are not carrying: {labels}")
    return out
