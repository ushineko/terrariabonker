"""Command line front end for terrariabonker.

A thin adapter over ``terrariabonker.service``: every subcommand elevates (once,
via sudo), builds a Service, and calls one operation, then formats the result as
text or ``--json``. All game logic lives in the service so the CLI and GUI cannot
drift; ``--json`` is the contract the GUI's ``gui.client`` consumes.
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import asdict

from terrariabonker.proc import elevate
from terrariabonker.service import Service, ServiceError
from terrariabonker.trainer import Freezer


def _svc(guard: bool = False, force: bool = False) -> Service:
    elevate()                                # no-op if already root; re-execs otherwise
    svc = Service.connect()
    if guard:
        svc.require_compatible(force)
    return svc


def _print_snapshot(svc: Service, show_all: bool = False) -> None:
    snap = svc.snapshot()
    p = snap.player
    print(f"Terraria PID {snap.pid} - {snap.copies} player copy(ies) - "
          f"build {snap.version} ({snap.compat_level})")
    if p:
        print(f"  {p.name!r}: HP {p.hp}/{p.max_hp}  Mana {p.mana}/{p.max_mana}")
    for s in snap.inventory:
        if s.type == 0 and not show_all:
            continue
        dmg = f" dmg={s.damage}" if s.damage > 0 else ""
        auto = " auto" if s.auto_reuse else ""
        tool = f" useTime={s.use_time} pick={s.pick}" if s.pick > 0 else ""
        print(f"  slot {s.slot:2d}: type={s.type:<5} stack={s.stack}{dmg}{auto}{tool}")


def cmd_status(args) -> int:
    svc = _svc()
    if args.json:
        snap = svc.snapshot(with_inventory=False)
        p = snap.player
        print(json.dumps({
            "pid": snap.pid, "version": snap.version, "compat_level": snap.compat_level,
            "copies": snap.copies,
            "name": p.name if p else None, "hp": p.hp if p else None,
            "max_hp": p.max_hp if p else None, "mana": p.mana if p else None,
            "max_mana": p.max_mana if p else None,
        }))
        return 0
    _print_snapshot(svc)
    return 0


def cmd_version(args) -> int:
    svc = _svc()
    snap = svc.snapshot(with_inventory=False)
    print(f"detected version : {snap.version}")
    print(f"detected buildid : {snap.buildid}")
    print(f"compatibility    : {snap.compat_level} - {snap.compat_msg}")
    return 0 if snap.compat_level in ("exact", "hotfix") else 2


def cmd_inventory(args) -> int:
    svc = _svc()
    slots = svc.inventory()
    if args.json:
        print(json.dumps([asdict(s) for s in slots]))
        return 0
    print(f"{len(slots)} slots")
    for s in slots:
        if s.type == 0 and not args.all:
            continue
        dmg = f" dmg={s.damage}" if s.damage > 0 else ""
        auto = " auto" if s.auto_reuse else ""
        tool = f" useTime={s.use_time} pick={s.pick}" if s.pick > 0 else ""
        print(f"  slot {s.slot:2d}: type={s.type:<5} stack={s.stack}{dmg}{auto}{tool}")
    return 0


def cmd_set_hp(args) -> int:
    svc = _svc(guard=True, force=args.force)
    svc.set_hp(args.value)
    print(f"[OK] set HP to {args.value}")
    return 0


def cmd_set_max_hp(args) -> int:
    svc = _svc(guard=True, force=args.force)
    svc.set_max_hp(int(args.value))
    print(f"[OK] set max HP to {args.value}")
    return 0


def cmd_set_mana(args) -> int:
    svc = _svc(guard=True, force=args.force)
    svc.set_mana(args.value)
    print(f"[OK] set mana to {args.value}")
    return 0


def cmd_set_max_mana(args) -> int:
    svc = _svc(guard=True, force=args.force)
    svc.set_max_mana(int(args.value))
    print(f"[OK] set max mana to {args.value}")
    return 0


def cmd_set_stack(args) -> int:
    svc = _svc(guard=True, force=args.force)
    svc.set_stack(args.slot, args.value)
    print(f"[OK] set slot {args.slot} stack to {args.value}")
    return 0


def cmd_set_item(args) -> int:
    svc = _svc(guard=True, force=args.force)
    svc.set_item(args.slot, args.type, stack=args.stack, damage=args.damage,
                 auto_reuse=args.auto_reuse, use_time=args.use_time,
                 use_anim=args.use_anim, pick=args.pick, tile_boost=args.tile_boost)
    print(f"[OK] set slot {args.slot} to type {args.type}")
    return 0


def cmd_give(args) -> int:
    svc = _svc(guard=True, force=args.force)
    slot = svc.give_item(args.type, args.stack)
    print(f"[OK] gave type {args.type} x{args.stack} into slot {slot}")
    return 0


def cmd_fast_mining(args) -> int:
    svc = _svc(guard=True, force=args.force)
    hit = svc.fast_mining(args.use_time, args.use_anim, args.pick)
    print(f"[OK] sped up pickaxe slots {hit}" if hit
          else "[note] no pickaxe found (pick power > 0)")
    return 0


def cmd_long_reach(args) -> int:
    svc = _svc(guard=True, force=args.force)
    hit = svc.long_reach(args.tiles)
    print(f"[OK] +{args.tiles} placement reach on {len(hit)} item(s). "
          "Base reach (tileRangeX/Y) is frame-reset and needs the CE table.")
    return 0


def cmd_freeze(args) -> int:
    svc = _svc(guard=True, force=args.force)
    if not (args.godmode or args.mana):
        sys.exit("[ERROR] pick at least one of --godmode / --mana")
    fr = Freezer(svc.mem, godmode=args.godmode, infinite_mana=args.mana, hz=args.hz)
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
    try:
        from terrariabonker.gui.main_window import run
    except ImportError as e:
        print(f"[ERROR] GUI unavailable (is PyQt6 installed?): {e}", file=sys.stderr)
        return 1
    return run()


def cmd_read(args) -> int:
    print(_svc().mem.read_i32(int(args.addr, 16)))
    return 0


def cmd_write(args) -> int:
    ok = _svc(guard=True, force=args.force).mem.write_i32(int(args.addr, 16), int(args.value))
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
    p.add_argument("--json", action="store_true", help="machine-readable output")
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
    p.add_argument("--json", action="store_true", help="machine-readable output")
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

    p = sub.add_parser("give", help="give an item into the first empty inventory slot")
    p.add_argument("type", type=int, help="ItemID")
    p.add_argument("--stack", type=int, default=1)
    force_flag(p)
    p.set_defaults(func=cmd_give)

    p = sub.add_parser("fast-mining", help="speed up every pickaxe (persistent item edit)")
    p.add_argument("--use-time", type=int, default=8, help="ticks per hit (default 8)")
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
    except ServiceError as e:
        print(f"[ERROR] {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
