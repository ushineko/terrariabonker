"""Command line front end for terrariabonker.

A thin adapter over ``terrariabonker.service``: every subcommand elevates (once,
via sudo), builds a Service, and calls one operation, then formats the result as
text or ``--json``. All game logic lives in the service so the CLI and GUI cannot
drift; ``--json`` is the contract the GUI's ``gui.client`` consumes.
"""

from __future__ import annotations

import argparse
import contextlib
import io
import json
import os
import sys
from dataclasses import asdict

from terrariabonker.patcher import PATCH_CATALOG, PatchError
from terrariabonker.proc import elevate
from terrariabonker.service import Service, ServiceError
from terrariabonker.trainer import Freezer


# Set by ``serve`` to a Service that keeps its locate caches warm across requests.
# One-shot CLI runs leave it None and behave exactly as before.
_WARM: Service | None = None


def _svc(guard: bool = False, force: bool = False) -> Service:
    if _WARM is not None:
        if guard:
            _WARM.require_compatible(force)
        return _WARM
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
    from terrariabonker import profile
    svc = _svc(guard=True, force=args.force)
    kwargs = dict(stack=args.stack, damage=args.damage, auto_reuse=args.auto_reuse,
                  use_time=args.use_time, use_anim=args.use_anim, pick=args.pick,
                  tile_boost=args.tile_boost, defense=args.defense, prefix=args.prefix)
    svc.set_item(args.slot, args.type, expect_type=args.expect_type, **kwargs)
    # Record the edit in the cross-session profile so auto-restore can re-apply it (Terraria
    # only saves type/stack/prefix, regenerating the rest from the type on load).
    if args.type:
        profile.set_item(args.slot, {"type": args.type,
                                     **{k: v for k, v in kwargs.items() if v is not None}})
    else:
        profile.clear_item(args.slot)
    print(f"[OK] set slot {args.slot} to type {args.type}")
    return 0


# Subcommands the long-lived worker will run. Everything else is refused: the GUI
# itself, the blocking freeze loop, the raw memory debug pokes, and the slow
# unprivileged disk work the GUI already runs without sudo.
SERVE_OPS = frozenset({
    "status", "version", "inventory", "inv", "set-hp", "set-max-hp", "set-mana",
    "set-max-mana", "set-stack", "set-item", "give", "patch", "restore",
    "fast-mining", "long-reach",
})


def _serve_reply(rid, ok: bool, out: str) -> None:
    """One response per line on stdout; the stream is the protocol, so flush it."""
    print(json.dumps({"id": rid, "ok": ok, "out": out}), flush=True)


def _serve_once(parser, argv: list[str]) -> tuple[bool, str]:
    """Run one already-allowlisted argv against the warm Service, capturing its
    output the way the GUI's merged-channels subprocess would see it."""
    buf = io.StringIO()
    try:
        with contextlib.redirect_stdout(buf), contextlib.redirect_stderr(buf):
            try:
                args = parser.parse_args(argv)
            except SystemExit:                      # argparse rejected the argv
                return False, (buf.getvalue() or "[ERROR] bad arguments")
            if not getattr(args, "func", None):
                return False, "[ERROR] no such command"
            rc = args.func(args)
        return rc == 0, buf.getvalue()
    except ServiceError as e:
        return False, buf.getvalue() + f"[ERROR] {e}"
    except Exception as e:                          # never let one request kill the worker
        return False, buf.getvalue() + f"[ERROR] {type(e).__name__}: {e}"


def cmd_serve(args) -> int:
    """Long-lived privileged worker for the GUI.

    Reads one JSON request per line on stdin -- ``{"id": N, "argv": [...]}`` -- runs the
    argv through this same parser against a Service whose locate caches stay warm, and
    writes one JSON response per line: ``{"id": N, "ok": bool, "out": str}``.

    The point is cost: locating the player is ~99% of a read (a full memory scan), so a
    one-shot CLI run pays ~2.7 s while a warm request costs ~2.5 ms. That is what makes
    the GUI's 1 Hz inventory sync affordable.

    It exits on stdin EOF, so it cannot outlive the GUI that started it.
    """
    global _WARM
    elevate()                                # no-op when the GUI already started us under sudo
    parser = build_parser()
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        rid = None
        try:
            req = json.loads(line)
            rid = req.get("id")
            argv = req["argv"]
            if not isinstance(argv, list) or not all(isinstance(a, str) for a in argv):
                raise ValueError("argv must be a list of strings")
        except (ValueError, KeyError, TypeError, AttributeError) as e:
            _serve_reply(rid, False, f"[ERROR] malformed request: {e}")
            continue
        if not argv or argv[0] not in SERVE_OPS:
            _serve_reply(rid, False,
                         f"[ERROR] {argv[0] if argv else '(empty)'} is not served here")
            continue
        # Drop the warm Service when the game went away, so the next request reconnects
        # to whatever is running now (game restart => new pid).
        if _WARM is not None and not os.path.exists(f"/proc/{_WARM.mem.pid}"):
            _WARM = None
        if _WARM is None:
            try:
                _WARM = Service.connect()
            except ServiceError as e:
                _serve_reply(rid, False, f"[ERROR] {e}")
                continue
        ok, out = _serve_once(parser, argv)
        _serve_reply(rid, ok, out)
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


def cmd_patch(args) -> int:
    svc = _svc(guard=True, force=args.force)
    p = svc.patcher()
    try:
        if args.action == "status":
            st = p.status()
            vals = p.values()
            if args.json:
                print(json.dumps({"on": st, "values": vals}))
                return 0
            for name, on in st.items():
                v = f"  = {vals[name]}" if name in vals else ""
                print(f"  [{'x' if on else ' '}] {name:<11} {PATCH_CATALOG[name].label}{v}")
        elif args.action in ("enable", "on"):
            p.enable(args.cheat, value=args.value)
            shown = f" (value {args.value})" if args.value is not None else ""
            print(f"[OK] enabled {args.cheat}{shown}")
        elif args.action in ("disable", "off"):
            p.disable(args.cheat)
            print(f"[OK] disabled {args.cheat}")
    except PatchError as e:
        print(f"[ERROR] {e}", file=sys.stderr)
        return 1
    return 0


def cmd_extract_recipes(args) -> int:
    from terrariabonker import recipes as R
    svc = _svc(guard=True, force=args.force)
    try:
        data = R.extract(svc.mem)
    except R.RecipeError as e:
        print(f"[ERROR] {e}", file=sys.stderr)
        return 1
    R.save(data)
    print(f"[OK] extracted {len(data['recipes'])} recipes, "
          f"{len(data['stations'])} station names -> {R._DATA}")
    return 0


def cmd_restore(args) -> int:
    """Re-apply the saved profile (desired cheats + item edits) to the running game."""
    svc = _svc(guard=True, force=args.force)
    rep = svc.restore()
    if args.json:
        print(json.dumps(rep))
        return 0
    print(f"[OK] restored cheats={rep['cheats']} items={rep['items']} "
          f"pending={rep['pending']} skipped={rep['skipped']}")
    return 0


def cmd_extract_sprites(args) -> int:
    """Decode item icons from the game's Content/Images into the local cache. Unprivileged
    (disk read + /proc/<pid>/maps only) — no sudo, so the cache lands in the user's home."""
    from terrariabonker import sprites
    from terrariabonker.proc import Mem, find_pid

    mem = None
    try:
        mem = Mem(find_pid())                        # only to learn the content path
    except Exception:
        mem = None                                   # game closed: rely on persisted path

    def prog(done, total):
        print(f"{done}/{total}", flush=True)

    try:
        ok, failed, total = sprites.extract(mem=mem, force=args.force, progress=prog)
    except RuntimeError as e:
        print(f"[ERROR] {e}", file=sys.stderr)
        return 1
    print(f"[OK] icons: {ok} of {total} ({failed} skipped/failed) -> {sprites.cache_dir()}")
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

    p = sub.add_parser("extract-recipes",
                       help="read Main.recipe[] from the running game -> data/recipes.json")
    force_flag(p)
    p.set_defaults(func=cmd_extract_recipes)

    p = sub.add_parser("restore",
                       help="re-apply the saved profile (cheats + item edits) to the game")
    force_flag(p)
    p.add_argument("--json", action="store_true", help="machine-readable report")
    p.set_defaults(func=cmd_restore)

    p = sub.add_parser("serve",
                       help="long-lived JSON worker for the GUI (stdin/stdout protocol)")
    p.set_defaults(func=cmd_serve)

    p = sub.add_parser("gui", help="launch the graphical control panel")
    p.set_defaults(func=cmd_gui)

    p = sub.add_parser("extract-sprites",
                       help="decode item icons from the game into a local cache")
    p.add_argument("--force", action="store_true", help="re-decode even if cached")
    p.set_defaults(func=cmd_extract_sprites)

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
    p.add_argument("--defense", type=int, default=None, help="defense the item grants")
    p.add_argument("--prefix", type=int, default=None,
                   help="modifier tier byte (0 none; e.g. Legendary/Warding/Menacing)")
    p.add_argument("--expect-type", type=int, default=None,
                   help="ItemID the slot is believed to hold; refuse the write if it "
                        "changed in-game (guards against editing a stale snapshot)")
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

    p = sub.add_parser("patch", help="code-patch cheats (mining/reach/placement)")
    p.add_argument("action", choices=["status", "enable", "disable", "on", "off"])
    p.add_argument("cheat", nargs="?", choices=list(PATCH_CATALOG))
    p.add_argument("--value", type=float, default=None,
                   help="override the enabled value (mining pickSpeed, reach tiles)")
    p.add_argument("--json", action="store_true", help="machine-readable status")
    force_flag(p)
    p.set_defaults(func=cmd_patch)

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
