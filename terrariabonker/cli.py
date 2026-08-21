"""Command line front end for terrariabonker.

Every command that touches game memory first re-execs under sudo (ptrace_scope=1
blocks non-root ``/proc/<pid>/mem`` access). Discovery and editing both run here;
the freeze-based cheats (godmode, infinite mana) hold until Ctrl-C.
"""

from __future__ import annotations

import argparse
import sys

from terrariabonker import version as ver
from terrariabonker.inventory import INVENTORY_SLOTS, Inventory
from terrariabonker.locate import find_players, pick_live
from terrariabonker.player import Player
from terrariabonker.proc import Mem, ProcError, elevate, find_pid
from terrariabonker.trainer import Freezer


def _connect(guard: bool = False, force: bool = False) -> Mem:
    elevate()                       # no-op if already root; re-execs otherwise
    mem = Mem(find_pid())
    if guard:
        ver.guard(mem, force=force)     # aborts on an incompatible game build
    return mem


def _players(mem: Mem):
    blocks = find_players(mem)
    if not blocks:
        sys.exit("[ERROR] no player found. Load into a world first.")
    return blocks


def cmd_status(args) -> int:
    mem = _connect()
    blocks = _players(mem)
    live = pick_live(mem, blocks)
    if getattr(args, "json", False):
        import json
        best = live or (max(blocks, key=lambda b: b.stat_life) if blocks else None)
        print(json.dumps({
            "pid": mem.pid,
            "version": ver.detect_version(mem),
            "name": best.name if best else None,
            "hp": best.stat_life if best else None,
            "max_hp": best.stat_life_max if best else None,
            "mana": best.stat_mana if best else None,
            "max_mana": best.stat_mana_max if best else None,
            "copies": len(blocks),
        }))
        return 0
    print(f"Terraria PID {mem.pid} - {len(blocks)} player copy(ies) found\n")
    for b in blocks:
        tag = "  <== live" if live and b.life_addr == live.life_addr else ""
        print(f"  0x{b.life_addr:08x}  {b.name!r}")
        print(f"      HP  {b.stat_life}/{b.stat_life_max} (perm {b.stat_life_max2})"
              f"   Mana {b.stat_mana}/{b.stat_mana_max} (perm {b.stat_mana_max2}){tag}")
    if live is None:
        print("\n[note] could not single out the live copy (game paused or idle at "
              "full HP); edits/freezes apply to all copies, which is safe.")
    return 0


def _targets(mem: Mem):
    """All player copies as Player handles - writes apply to every copy."""
    return [Player(mem, b.life_addr) for b in _players(mem)]


def cmd_version(args) -> int:
    mem = _connect()
    version = ver.detect_version(mem)
    buildid = ver.read_buildid(mem.exe_path())
    level, msg = ver.compatibility(version, buildid)
    print(f"detected version : {version}")
    print(f"detected buildid : {buildid}")
    print(f"known-good       : {ver.KNOWN_VERSION} (buildid {ver.KNOWN_BUILDID})")
    print(f"compatibility    : {level} - {msg}")
    return 0 if level in ("exact", "hotfix") else 2


def cmd_set_hp(args) -> int:
    mem = _connect(guard=True, force=args.force)
    for p in _targets(mem):
        p.set_life(p.stat_life_max if args.value == "max" else int(args.value))
    print(f"[OK] set HP to {args.value}")
    return cmd_status(args)


def cmd_set_max_hp(args) -> int:
    mem = _connect(guard=True, force=args.force)
    for p in _targets(mem):
        p.set_max_life(int(args.value))
    print(f"[OK] set max HP to {args.value}")
    return cmd_status(args)


def cmd_set_mana(args) -> int:
    mem = _connect(guard=True, force=args.force)
    for p in _targets(mem):
        p.set_mana(p.stat_mana_max if args.value == "max" else int(args.value))
    print(f"[OK] set mana to {args.value}")
    return cmd_status(args)


def cmd_set_max_mana(args) -> int:
    mem = _connect(guard=True, force=args.force)
    for p in _targets(mem):
        p.set_max_mana(int(args.value))
    print(f"[OK] set max mana to {args.value}")
    return cmd_status(args)


def _live_inventory(mem: Mem):
    """The live player's inventory.

    Prefer the activity-sampled live copy; when that is inconclusive (paused),
    fall back to the copy with the richest inventory, since the snapshot copies
    hold only the stale starting items.
    """
    blocks = _players(mem)
    live = pick_live(mem, blocks)
    if live is None:
        live = max(blocks, key=lambda b: Inventory(mem, b.life_addr).nonempty_count())
    return Inventory(mem, live.life_addr)


def _print_inventory(mem: Mem, show_all: bool = False) -> int:
    inv = _live_inventory(mem)
    arr = inv.array_addr()
    if arr is None:
        sys.exit("[ERROR] could not resolve the inventory array")
    print(f"inventory array 0x{arr:08x} ({INVENTORY_SLOTS} slots)")
    for s in inv.slots():
        if s.empty and not show_all:
            continue
        dmg = f" dmg={s.damage}" if s.damage > 0 else ""
        auto = " auto" if s.auto_reuse else ""
        tool = f" useTime={s.use_time} pick={s.pick}" if s.pick > 0 else ""
        print(f"  slot {s.index:2d}: type={s.type:<5} stack={s.stack}{dmg}{auto}{tool}")
    return 0


def cmd_inventory(args) -> int:
    mem = _connect()
    if getattr(args, "json", False):
        import json
        inv = _live_inventory(mem)
        print(json.dumps([
            {"slot": s.index, "type": s.type, "stack": s.stack, "damage": s.damage,
             "auto_reuse": s.auto_reuse, "use_time": s.use_time, "pick": s.pick,
             "tile_boost": s.tile_boost}
            for s in inv.slots()
        ]))
        return 0
    return _print_inventory(mem, show_all=args.all)


def cmd_set_stack(args) -> int:
    mem = _connect(guard=True, force=args.force)
    _live_inventory(mem).set_stack(args.slot, args.value)
    print(f"[OK] set slot {args.slot} stack to {args.value}")
    return _print_inventory(mem, show_all=True)


def cmd_set_item(args) -> int:
    mem = _connect(guard=True, force=args.force)
    inv = _live_inventory(mem)
    inv.set_type(args.slot, args.type)
    if args.stack is not None:
        inv.set_stack(args.slot, args.stack)
    if args.damage is not None:
        inv.set_damage(args.slot, args.damage)
    if args.auto_reuse is not None:
        inv.set_auto_reuse(args.slot, bool(args.auto_reuse))
    if args.use_time is not None:
        inv.set_use_speed(args.slot, args.use_time, args.use_anim)
    if args.pick is not None:
        inv.set_pick(args.slot, args.pick)
    if args.tile_boost is not None:
        inv.set_tile_boost(args.slot, args.tile_boost)
    print(f"[OK] set slot {args.slot} to type {args.type}")
    return _print_inventory(mem, show_all=True)


def cmd_fast_mining(args) -> int:
    mem = _connect(guard=True, force=args.force)
    hit = _live_inventory(mem).make_fast_mining(args.use_time, args.use_anim, args.pick)
    if not hit:
        print("[note] no pickaxe found in the inventory (pick power > 0)")
    else:
        print(f"[OK] sped up pickaxe slots {hit} "
              f"(useTime={args.use_time}, useAnim={args.use_anim}, pick={args.pick})")
    return _print_inventory(mem, show_all=False)


def cmd_long_reach(args) -> int:
    mem = _connect(guard=True, force=args.force)
    hit = _live_inventory(mem).long_reach(args.tiles)
    print(f"[OK] +{args.tiles} placement reach on {len(hit)} item(s). "
          "Base reach (tileRangeX/Y) is frame-reset and needs the CE table.")
    return 0


def cmd_freeze(args) -> int:
    mem = _connect(guard=True, force=args.force)
    if not (args.godmode or args.mana):
        sys.exit("[ERROR] pick at least one of --godmode / --mana")
    fr = Freezer(mem, godmode=args.godmode, infinite_mana=args.mana, hz=args.hz)
    levers = ", ".join(x for x, on in
                       [("godmode", args.godmode), ("infinite-mana", args.mana)] if on)

    def announce(players):
        print(f"[OK] freezing [{levers}] on {len(players)} copy(ies) at {args.hz} Hz. "
              f"{'Runs for %gs.' % args.seconds if args.seconds else 'Ctrl-C to stop.'}")

    try:
        fr.run(seconds=args.seconds, on_start=announce)
    except KeyboardInterrupt:
        pass
    print(f"\n[done] {fr.saves} restores applied.")
    return 0


def cmd_gui(args) -> int:
    """Launch the GUI. It runs unprivileged and shells actions out via sudo."""
    try:
        from terrariabonker.gui.main_window import run
    except ImportError as e:
        print(f"[ERROR] GUI unavailable (is PyQt6 installed?): {e}", file=sys.stderr)
        return 1
    return run()


def cmd_read(args) -> int:
    mem = _connect()
    print(mem.read_i32(int(args.addr, 16)))
    return 0


def cmd_write(args) -> int:
    mem = _connect(guard=True, force=args.force)
    ok = mem.write_i32(int(args.addr, 16), int(args.value))
    print("[OK]" if ok else "[FAIL]", "wrote", args.value, "to", args.addr)
    return 0 if ok else 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="terrariabonker",
        description="From-scratch /proc memory trainer for Terraria (Proton/wine-mono).",
    )
    sub = parser.add_subparsers(dest="command")

    def force_flag(sp):
        sp.add_argument("--force", action="store_true",
                        help="run even if the game build is not the known-good one")

    p = sub.add_parser("status", help="find the player and show HP/mana")
    p.add_argument("--json", action="store_true", help="machine-readable output (for the GUI)")
    p.set_defaults(func=cmd_status)

    p = sub.add_parser("gui", help="launch the graphical control panel")
    p.set_defaults(func=cmd_gui)

    p = sub.add_parser("version", help="report the game version and compatibility")
    p.set_defaults(func=cmd_version)

    p = sub.add_parser("set-hp", help="set current HP (a number or 'max')")
    p.add_argument("value")
    force_flag(p)
    p.set_defaults(func=cmd_set_hp)

    p = sub.add_parser("set-max-hp", help="set permanent max HP")
    p.add_argument("value")
    force_flag(p)
    p.set_defaults(func=cmd_set_max_hp)

    p = sub.add_parser("set-mana", help="set current mana (a number or 'max')")
    p.add_argument("value")
    force_flag(p)
    p.set_defaults(func=cmd_set_mana)

    p = sub.add_parser("set-max-mana", help="set permanent max mana")
    p.add_argument("value")
    force_flag(p)
    p.set_defaults(func=cmd_set_max_mana)

    p = sub.add_parser("inventory", aliases=["inv"], help="list inventory slots")
    p.add_argument("--all", action="store_true", help="include empty slots")
    p.add_argument("--json", action="store_true", help="machine-readable output (for the GUI)")
    p.set_defaults(func=cmd_inventory)

    p = sub.add_parser("set-stack", help="set an inventory slot's stack count")
    p.add_argument("slot", type=int)
    p.add_argument("value", type=int)
    force_flag(p)
    p.set_defaults(func=cmd_set_stack)

    p = sub.add_parser("set-item",
                       help="edit a slot: type, stack, damage, auto-reuse, use-speed, pick, tile-boost")
    p.add_argument("slot", type=int)
    p.add_argument("type", type=int, help="ItemID (e.g. dirt=2)")
    p.add_argument("--stack", type=int, default=None)
    p.add_argument("--damage", type=int, default=None)
    p.add_argument("--auto-reuse", type=int, choices=[0, 1], default=None,
                   help="1 = auto-swing while held, 0 = off")
    p.add_argument("--use-time", type=int, default=None, help="ticks per use (lower=faster)")
    p.add_argument("--use-anim", type=int, default=None, help="swing animation frames")
    p.add_argument("--pick", type=int, default=None, help="pickaxe power (percent)")
    p.add_argument("--tile-boost", type=int, default=None, help="extra placement reach (tiles)")
    force_flag(p)
    p.set_defaults(func=cmd_set_item)

    p = sub.add_parser("fast-mining", help="speed up every pickaxe (persistent item edit)")
    p.add_argument("--use-time", type=int, default=8, help="ticks per hit (default 8, Picksaw-tier)")
    p.add_argument("--use-anim", type=int, default=13, help="swing frames (default 13)")
    p.add_argument("--pick", type=int, default=200, help="pickaxe power percent (default 200)")
    force_flag(p)
    p.set_defaults(func=cmd_fast_mining)

    p = sub.add_parser("long-reach", help="extend placement reach on all items (Item.tileBoost)")
    p.add_argument("--tiles", type=int, default=20, help="extra tiles of reach (default 20)")
    force_flag(p)
    p.set_defaults(func=cmd_long_reach)

    p = sub.add_parser("freeze", help="hold values against the game (godmode etc.)")
    p.add_argument("--godmode", action="store_true", help="pin HP to max")
    p.add_argument("--mana", action="store_true", help="pin mana to max")
    p.add_argument("--hz", type=int, default=200, help="rewrite frequency (default 200)")
    p.add_argument("--seconds", type=float, default=None, help="stop after N seconds")
    force_flag(p)
    p.set_defaults(func=cmd_freeze)

    p = sub.add_parser("godmode", help="shorthand for: freeze --godmode")
    p.add_argument("--hz", type=int, default=200)
    p.add_argument("--seconds", type=float, default=None)
    force_flag(p)
    p.set_defaults(func=cmd_freeze, godmode=True, mana=False)

    p = sub.add_parser("read", help="read an int32 at a hex address (debug)")
    p.add_argument("addr")
    p.set_defaults(func=cmd_read)

    p = sub.add_parser("write", help="write an int32 at a hex address (debug)")
    p.add_argument("addr")
    p.add_argument("value")
    force_flag(p)
    p.set_defaults(func=cmd_write)
    return parser


def main() -> int:
    if len(sys.argv) == 1:
        # Bare launch (menu / double-click) opens the GUI; fall back to status text.
        try:
            import PyQt6.QtWidgets  # noqa: F401
            sys.argv.append("gui")
        except ImportError:
            sys.argv.append("status")
    parser = build_parser()
    args = parser.parse_args()
    if not getattr(args, "func", None):
        parser.print_help()
        return 1
    try:
        return args.func(args)
    except ProcError as e:
        print(f"[ERROR] {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
