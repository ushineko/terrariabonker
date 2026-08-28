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

from terrariabonker import names
from terrariabonker.patcher import PATCH_CATALOG, PatchError
from terrariabonker import version as ver
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
            "buildid": snap.buildid, "build": ver.build_key(snap.version, snap.buildid),
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
    # Record the edit so auto-restore can re-apply it. Terraria itself saves type, stack
    # and prefix and regenerates the rest from the type on load, so only the regenerated
    # fields are worth keeping (spec 038).
    if args.type:
        svc.record_item_edit(args.type, kwargs)
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
    "fast-mining", "long-reach", "compendium", "spawn-npc", "build-check",
    "accept-build", "vein", "extract", "extract-tick", "extract-stop",
    "potions", "fishing", "fishing-buffs", "catch", "catch-tick", "catch-stop",
    "projectile-tick", "projectile-stop", "projectile-of",
    "sell", "sell-tick", "sell-list",
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


def cmd_compendium(args) -> int:
    """The full item/NPC catalog. Always --json: it is a GUI data feed, not a listing."""
    svc = _svc()
    cat = svc.compendium(refresh=getattr(args, "refresh", False))
    print(json.dumps(cat))
    return 0


def cmd_vein(args) -> int:
    """Dry run: what a vein miner would take from one tile. Reads only, writes nothing."""
    svc = _svc()
    if args.x is None or args.y is None:
        x, y = svc.player_tile()
        print(f"[vein] no coordinates given; using the player's tile ({x}, {y})")
    else:
        x, y = args.x, args.y
    got = svc.vein_at(x, y, gems=args.gems, limit=args.limit,
                      diagonal=not args.orthogonal)
    if args.json:
        print(json.dumps(got))
        return 0
    if not got["whitelisted"]:
        print(f"[vein] tile ({x}, {y}) is id {got['type']} — not on the whitelist; "
              "nothing would be mined")
        return 0
    print(f"[vein] {got['name']} (id {got['type']}) at ({x}, {y}): "
          f"{got['count']} tiles would be mined"
          + ("  [CAPPED — the vein is larger]" if got["capped"] else ""))
    pts = [tuple(p) for p in got["tiles"]]
    xs = [p[0] for p in pts]
    ys = [p[1] for p in pts]
    print(f"       bounding box x {min(xs)}..{max(xs)}, y {min(ys)}..{max(ys)}")
    if args.map:
        seen = set(pts)
        tm = svc.tilemap()
        for yy in range(min(ys) - 1, max(ys) + 2):
            row = "".join("#" if (xx, yy) in seen else ("." if tm.type_at(xx, yy) else " ")
                          for xx in range(min(xs) - 1, max(xs) + 2))
            print("       " + row)
    return 0


def _extract_line(got) -> str:
    at = got["at"]
    med = got.get("median_wait")
    return (f"[extract] {got['mined']} of {got['queued']} tiles mined at "
            f"({at[0]}, {at[1]})"
            + (f", median {med:.2f}s/tile" if med is not None else "")
            + (f" — {got['reason']}" if got["reason"] else ""))


def _bait_line(r: dict) -> str:
    if not r["topped"]:
        return "[fishing] bait ok" if r["baits"] else "[fishing] no bait carried"
    return "[fishing] topped up " + ", ".join(
        "slot %d %d->%d" % (t["slot"], t["was"], t["now"]) for t in r["topped"])


def cmd_fishing(args) -> int:
    """Hand out a rod and bait, and keep the bait from running out."""
    svc = _svc(guard=True, force=args.force)
    out = {}
    if args.restore:
        out["restore"] = svc.restore_fishing_power()
        if not args.json:
            done = out["restore"]["restored"]
            print("[fishing] " + (", ".join(
                "rod in slot %d back to power %d" % (d["slot"], d["power"])
                for d in done) if done else "no rod power to put back"))
        if args.json:
            print(json.dumps(out))
        return 0
    if args.power:
        out["power"] = svc.set_fishing_power(args.power)
        if not args.json:
            ch = out["power"]["changed"]
            print("[fishing] " + (", ".join(
                "slot %d %d -> %d" % (c["slot"], c["was"], c["now"]) for c in ch)
                if ch else "rods already at power %d" % args.power))
    if not args.no_kit:
        out["kit"] = svc.fishing_kit()
        if not args.json:
            gave = out["kit"]["gave"]
            print("[fishing] " + ("gave " + ", ".join(
                "%s (slot %d)" % (k, v["slot"]) for k, v in gave.items())
                if gave else "you already have a rod and bait"))
    if args.watch:
        print("[fishing] watching — your bait will not run out. Ctrl-C to stop.")
        try:
            got = svc.watch_bait(keep=args.keep, interval=args.interval,
                                 rounds=args.rounds,
                                 on_event=lambda r: print(_bait_line(r), flush=True))
        except KeyboardInterrupt:
            print("\n[fishing] stopped")
            return 0
        out["watch"] = got
        if not args.json:
            print(f"[fishing] {got['refills']} refill(s) over {got['rounds']} rounds")
    else:
        out["bait"] = svc.bait_tick(keep=args.keep)
        if not args.json:
            print(_bait_line(out["bait"]))
    if args.json:
        print(json.dumps(out))
    return 0


def _potion_line(r: dict) -> str:
    bits = []
    for kind in ("added", "renewed", "kept"):
        if r[kind]:
            bits.append("%s %s" % (kind, ",".join(str(e["buff"]) for e in r[kind])))
    if r["full"]:
        bits.append("NO FREE BUFF SLOT for %s"
                    % ",".join(str(e["buff"]) for e in r["full"]))
    return "[potions] " + ("; ".join(bits) if bits else "nothing favorited")


def cmd_potions(args) -> int:
    """Keep favorited potions' buffs topped up while they sit in the inventory."""
    svc = _svc(guard=True, force=args.force)
    if args.watch:
        print("[potions] watching — favorite a potion (alt-click) to switch it on, "
              "unfavorite or drop it to let it lapse. Ctrl-C to stop.")
        try:
            got = svc.watch_potions(min_stack=args.min_stack, ticks=args.ticks,
                                    interval=args.interval, rounds=args.rounds,
                                    on_event=lambda r: print(_potion_line(r), flush=True))
        except KeyboardInterrupt:
            print("\n[potions] stopped — your buffs will lapse on their own")
            return 0
        if args.json:
            print(json.dumps(got))
        else:
            print(f"[potions] {got['applied']} applied over {got['rounds']} rounds")
        return 0
    got = svc.potion_tick(min_stack=args.min_stack, ticks=args.ticks)
    if args.json:
        print(json.dumps(got))
        return 0
    print(_potion_line(got))
    return 0


def _sell_line(got: dict) -> str:
    if got.get("error"):
        return f"[sell] {got['error']}"
    if not got["sold"]:
        held = len(got["skipped"])
        return f"[sell] nothing to sell{f' ({held} skipped)' if held else ''}"
    what = ", ".join(f"{e['stack']}x {e['name']}" for e in got["sold"])
    where = {"bank": "the piggy bank", "inventory": "your inventory",
             "bank+inventory": "the piggy bank and your inventory"}[got["destination"]]
    return f"[sell] {what} -> {_coins(got['copper'])} into {where}"


def _coins(copper: int) -> str:
    """Copper as the game would write it: 1 platinum 23 gold 45 silver 67 copper."""
    from terrariabonker import selling as S
    names_ = {71: "copper", 72: "silver", 73: "gold", 74: "platinum"}
    parts = [f"{n} {names_[t]}" for t, n in S.coin_stacks(copper)]
    return " ".join(parts) if parts else "0 copper"


def cmd_sell(args) -> int:
    """Sell whitelisted items. THIS PERMANENTLY REMOVES THEM."""
    from terrariabonker import profile

    svc = _svc(guard=True, force=args.force)
    if args.add or args.remove:
        for t in args.add or []:
            profile.set_sell_whitelist(t, True)
        for t in args.remove or []:
            profile.set_sell_whitelist(t, False)
    if args.list:
        wl = sorted(profile.sell_whitelist())
        if args.json:
            print(json.dumps({"whitelist": wl,
                              "names": {str(t): names.label(t) for t in wl}}))
        else:
            print("[sell] whitelist: "
                  + (", ".join(f"{names.label(t)} ({t})" for t in wl) or "empty"))
        return 0
    if args.watch:
        print("[sell] watching — whitelisted items are sold as they arrive. Ctrl-C stops.")
        try:
            got = svc.watch_selling(interval=args.interval, rounds=args.rounds,
                                    on_event=lambda r: print(_sell_line(r), flush=True))
        except KeyboardInterrupt:
            print("\n[sell] stopped")
            return 0
        if args.json:
            print(json.dumps(got))
        else:
            print(f"[sell] {_coins(got['copper'])} over {got['rounds']} rounds")
        return 0
    got = svc.sell_tick(dry_run=args.dry_run)
    if args.json:
        print(json.dumps(got))
        return 0
    print(_sell_line(got))
    return 0


def cmd_extract(args) -> int:
    """Mine the vein at a tile. THIS WRITES TO THE WORLD."""
    svc = _svc(guard=True, force=args.force)
    if args.watch:
        print("[extract] watching — break one ore by hand and the rest of its vein "
              "goes with it. Ctrl-C to stop.")
        try:
            got = svc.watch_veins(gems=args.gems, limit=args.limit,
                                  timeout=args.timeout, rounds=args.rounds,
                                  on_event=lambda e: print(_extract_line(e), flush=True))
        except KeyboardInterrupt:
            print("\n[extract] stopped")
            return 0
        if args.json:
            print(json.dumps(got))
        else:
            print(f"[extract] {got['mined']} tiles over {len(got['events'])} vein(s)")
        return 0
    if args.x is None or args.y is None:
        x, y = svc.player_tile()
    else:
        x, y = args.x, args.y
    got = svc.extract_vein(x, y, gems=args.gems, limit=args.limit,
                           timeout=args.timeout)
    if args.json:
        print(json.dumps(got))
        return 0
    print(_extract_line(got))
    return 0


def cmd_sell_tick(args) -> int:
    """One auto-sell round, for the panel timer."""
    got = _svc(guard=True, force=args.force).sell_tick(dry_run=args.dry_run)
    print(json.dumps(got) if args.json else _sell_line(got))
    return 0


def cmd_sell_list(args) -> int:
    """Read or edit the whitelist without touching the game."""
    from terrariabonker import profile

    for t in args.add or []:
        profile.set_sell_whitelist(t, True)
    for t in args.remove or []:
        profile.set_sell_whitelist(t, False)
    wl = sorted(profile.sell_whitelist())
    if args.json:
        print(json.dumps({"whitelist": wl,
                          "names": {str(t): names.label(t) for t in wl}}))
    else:
        print("[sell] whitelist: "
              + (", ".join(f"{names.label(t)} ({t})" for t in wl) or "empty"))
    return 0


def cmd_extract_tick(args) -> int:
    """One slice of vein watching, for a caller that cannot block (the GUI).

    The blocking `extract --watch` owns its loop; the GUI drives this from a timer so the
    Qt event loop keeps running. State lives in the long-lived `serve` worker, which is
    what makes it cheap: the ore map persists between ticks instead of being rebuilt.
    """
    svc = _svc(guard=True, force=args.force)
    got = svc.watch_tick(gems=args.gems, limit=args.limit, timeout=args.timeout,
                         budget=args.budget)
    if args.json:
        print(json.dumps(got))
        return 0
    for e in got["events"]:
        print(_extract_line(e))
    return 0


def cmd_extract_stop(args) -> int:
    """Drop the watcher and disarm; the cheat was switched off."""
    svc = _svc(guard=True, force=args.force)
    print(json.dumps(svc.watch_stop()) if args.json else "[extract] watcher stopped")
    return 0


def _buff_line(r: dict) -> str:
    held = ", ".join(f"{d['name']} ({d['what']})" for d in r["held"])
    deferred = ", ".join(d["name"] for d in r["deferred"])
    parts = []
    if held:
        parts.append("holding " + held)
    if deferred:
        parts.append(f"left {deferred} alone — a potion is already running it")
    return "[fishing] " + ("; ".join(parts) if parts else "nothing to hold")


def cmd_fishing_buffs(args) -> int:
    """Hold the fishing potions' effects up without the potions."""
    svc = _svc(guard=True, force=args.force)
    if not (args.power or args.sonar or args.crate):
        print("[fishing] pick at least one of --power, --sonar, --crate")
        return 1
    if args.watch:
        print("[fishing] holding the fishing potion effects up. Ctrl-C to stop.")
        try:
            got = svc.watch_fishing_buffs(power=args.power, sonar=args.sonar,
                                          crate=args.crate, interval=args.interval,
                                          rounds=args.rounds,
                                          on_event=lambda r: print(_buff_line(r), flush=True))
        except KeyboardInterrupt:
            print("\n[fishing] stopped — the effects lapse in a couple of seconds")
            return 0
        print(json.dumps(got) if args.json else f"[fishing] {got['rounds']} rounds")
        return 0
    got = svc.fishing_buff_tick(power=args.power, sonar=args.sonar, crate=args.crate)
    print(json.dumps(got) if args.json else _buff_line(got))
    return 0


def _catch_line(e: dict) -> str:
    if e["what"] == "cast":
        return ("[catch] cast the line" if e.get("confirmed")
                else "[catch] tried to cast and no line went out")
    catch = e.get("catch", 0)
    from terrariabonker import names
    what = names.name(catch) if catch > 0 else f"NPC {-catch}"
    return f"[catch] reeled in {what}"


def cmd_catch(args) -> int:
    """Take every fish that bites. Blocking; Ctrl-C stops it."""
    svc = _svc(guard=True, force=args.force)
    print("[catch] watching for bites. Ctrl-C to stop."
          + ("" if args.recast else " (pass --recast to cast for you too)"))
    try:
        got = svc.watch_catch(recast=args.recast, rounds=args.rounds,
                              on_event=lambda e: print(_catch_line(e), flush=True))
    except KeyboardInterrupt:
        print("\n[catch] stopped")
        return 0
    print(json.dumps(got) if args.json
          else f"[catch] {got['caught']} fish over {got['rounds']} rounds"
               + (f", {got['cast']} casts" if got.get("cast") else "")
               + (f", {got['cast_missed']} that did not go out"
                  if got.get("cast_missed") else ""))
    return 0


def cmd_catch_tick(args) -> int:
    """One slice of auto-catch, for a caller that cannot block (the GUI)."""
    svc = _svc(guard=True, force=args.force)
    got = svc.catch_tick(recast=args.recast, budget=args.budget)
    if args.json:
        print(json.dumps(got))
        return 0
    for e in got["events"]:
        print(_catch_line(e))
    return 0


def cmd_catch_stop(args) -> int:
    """Drop the located projectile array; the cheat was switched off."""
    svc = _svc(guard=True, force=args.force)
    print(json.dumps(svc.catch_stop()) if args.json else "[catch] stopped")
    return 0


def _parse_overrides(pairs) -> dict:
    """``["837:tileCollide=0"]`` -> ``{837: {"tileCollide": 0}}``.

    Unknown field names are rejected here rather than dropped silently: a typo in a
    profile would otherwise look exactly like a field that does nothing, which is a
    diagnosis this project has already paid for once.
    """
    from terrariabonker.projectile_edit import FIELDS

    out: dict[int, dict] = {}
    for raw in pairs or ():
        head, _, value = raw.partition("=")
        ptype, _, name = head.partition(":")
        if not value or not name:
            raise SystemExit(f"bad --set {raw!r}; expected TYPE:FIELD=VALUE")
        if name not in FIELDS:
            raise SystemExit(f"unknown field {name!r}; known: {', '.join(sorted(FIELDS))}")
        try:
            num = float(value) if FIELDS[name].kind == "f32" else int(value)
        except ValueError:
            raise SystemExit(f"bad value for {name}: {value!r}")
        out.setdefault(int(ptype), {})[name] = num
    return out


def cmd_projectile_tick(args) -> int:
    """One slice of projectile editing, for a caller that cannot block (the GUI)."""
    svc = _svc(guard=True, force=args.force)
    got = svc.projectile_tick(_parse_overrides(args.set), budget=args.budget)
    if args.json:
        print(json.dumps(got))
        return 0
    types = ", ".join(f"{t}x{n}" for t, n in sorted(got["types"].items()))
    print(f"[projectile] {got['patched']} field writes over {got['sweeps']} sweeps"
          + (f" ({types})" if types else " (nothing in flight)"))
    return 0


def cmd_projectile_stop(args) -> int:
    """Forget per-projectile state; the cheat was switched off."""
    svc = _svc(guard=True, force=args.force)
    print(json.dumps(svc.projectile_stop()) if args.json else "[projectile] stopped")
    return 0


def cmd_projectile_of(args) -> int:
    """Which projectile does this item fire?"""
    svc = _svc(guard=True, force=args.force)
    got = svc.projectile_of(args.item)
    if args.json:
        print(json.dumps(got))
        return 0
    print(f"item {got['item']} shoots projectile {got['shoot']}" if got["shoot"]
          else f"item {got['item']} fires no projectile")
    return 0


def cmd_build_check(args) -> int:
    """What build is running, is it recognised, and do the cheats resolve on it."""
    svc = _svc()
    got = svc.build_check()
    if args.json:
        print(json.dumps(got))
        return 0
    print(f"build {got['build']} ({got['level']})")
    # The runtime is reported because the patches match code its JIT emitted: a Proton
    # update can break a cheat with the game untouched, and this is the only thing on
    # screen that would show it changed.
    print(f"  runtime: {got.get('runtime') or 'unknown'}")
    print(f"  recognised: {got['recognised']}  known-good: {got['known']}"
          f"  decision: {got['decision']}")
    for name, r in sorted(got["cheats"].items()):
        mark = "ok " if r["resolved"] else "NO "
        extra = f"  {r['reason']}" if r["reason"] else ""
        print(f"  {mark} {name:16} sites={r['sites']}{extra}")
    return 0


def cmd_accept_build(args) -> int:
    """Record this machine's decision about the running build (spec 036)."""
    svc = _svc()
    got = svc.accept_build(args.decision, args.failed or ())
    print(json.dumps(got) if args.json else
          f"recorded {got['build']} as {got['decision']}")
    return 0


def cmd_spawn_npc(args) -> int:
    svc = _svc(guard=True, force=args.force)
    got = svc.spawn_npc(args.id, args.distance)
    if args.json:
        print(json.dumps(got))
    else:
        print(f"spawned {got['name']} (#{got['id']}) in slot {got['slot']} "
              f"at tile ({got['x']:.0f}, {got['y']:.0f}), "
              f"{got['tiles_away']} tiles from you")
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
            # The panel polls this every couple of seconds, which is the one moment we can
            # count on the game being focused and running. Allocate then, so toggling a
            # cheat later — which requires clicking away from the game, pausing it — does
            # not have to.
            svc.ensure_arena()
            build = svc.build_key()
            detail = p.details(build)
            st = {name: d["on"] for name, d in detail.items()}
            vals = p.values()
            if args.json:
                print(json.dumps({"on": st, "values": vals, "detail": detail,
                                  "build": build,
                                  "build_verified": all(d["verified"] for d in detail.values())}))
                return 0
            print(f"  build {build}")
            for name, on in st.items():
                v = f"  = {vals[name]}" if name in vals else ""
                d = detail[name]
                note = "" if d["available"] else f"   UNAVAILABLE: {d['reason']}"
                if d["available"] and not d["verified"]:
                    note = "   (AOB unverified on this build)"
                print(f"  [{'x' if on else ' '}] {name:<11} "
                      f"{PATCH_CATALOG[name].label}{v}{note}")
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

    p = sub.add_parser("compendium",
                       help="dump the full item/NPC catalog as JSON (for the GUI tab)")
    p.add_argument("--json", action="store_true", help="machine-readable (always on)")
    p.add_argument("--refresh", action="store_true",
                   help="rescan the game instead of using the per-build cache")
    p.set_defaults(func=cmd_compendium)

    p = sub.add_parser("vein",
                       help="dry run: what a vein miner would take from a tile (reads only)")
    p.add_argument("x", type=int, nargs="?", help="tile x (default: the player's tile)")
    p.add_argument("y", type=int, nargs="?", help="tile y")
    p.add_argument("--gems", action="store_true", help="include gems as well as ores")
    p.add_argument("--limit", type=int, help="stop after this many tiles")
    p.add_argument("--orthogonal", action="store_true",
                   help="only up/down/left/right, not diagonals")
    p.add_argument("--map", action="store_true", help="draw the vein")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.set_defaults(func=cmd_vein)

    p = sub.add_parser("fishing",
                       help="a rod and bait if you have none, and bait that lasts")
    p.add_argument("--keep", type=int, default=30,
                   help="top any bait stack below this back up to it (default 30)")
    p.add_argument("--no-kit", action="store_true", dest="no_kit",
                   help="do not hand out a rod or bait, only keep bait topped up")
    p.add_argument("--watch", action="store_true", help="keep topping bait up")
    p.add_argument("--interval", type=float, default=1.0,
                   help="seconds between rounds with --watch")
    p.add_argument("--rounds", type=int,
                   help="with --watch, stop after this many rounds")
    p.add_argument("--power", type=int,
                   help="raise every rod you carry to this fishing power (1-255); the "
                        "original is recorded and put back by --restore")
    p.add_argument("--restore", action="store_true",
                   help="put every rod back to the power it had, and forget the record")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_fishing)

    p = sub.add_parser("extract",
                       help="mine the vein at a tile (WRITES to the world)")
    p.add_argument("x", type=int, nargs="?", help="tile x (default: the player's tile)")
    p.add_argument("y", type=int, nargs="?", help="tile y")
    p.add_argument("--gems", action="store_true", help="include gems")
    p.add_argument("--limit", type=int, help="stop after this many tiles")
    p.add_argument("--timeout", type=float, default=20.0,
                   help="seconds to wait for each tile before giving up")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.add_argument("--watch", action="store_true",
                   help="keep watching: break one ore by hand and its vein goes with it")
    p.add_argument("--rounds", type=int,
                   help="with --watch, stop after this many polls (default: forever)")
    p.set_defaults(func=cmd_extract)

    p = sub.add_parser("potions",
                       help="favorited potions grant their buff from the inventory")
    p.add_argument("--min-stack", type=int, default=1, dest="min_stack",
                   help="only potions with at least this many in the stack (default 1)")
    p.add_argument("--ticks", type=int, default=None,
                   help="buff time written per round, in frames (60 = 1s)")
    p.add_argument("--interval", type=float, default=0.25,
                   help="seconds between rounds; must be well under --ticks")
    p.add_argument("--watch", action="store_true",
                   help="keep them topped up (otherwise a single round)")
    p.add_argument("--rounds", type=int,
                   help="with --watch, stop after this many rounds (default: forever)")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_potions)

    p = sub.add_parser("sell",
                       help="sell whitelisted items for coins (PERMANENT)")
    p.add_argument("--add", type=int, nargs="+", metavar="TYPE",
                   help="add item type(s) to the whitelist")
    p.add_argument("--remove", type=int, nargs="+", metavar="TYPE",
                   help="remove item type(s) from the whitelist")
    p.add_argument("--list", action="store_true", help="show the whitelist and stop")
    p.add_argument("--dry-run", action="store_true", dest="dry_run",
                   help="report what would sell, and sell nothing")
    p.add_argument("--watch", action="store_true",
                   help="keep selling as items arrive (otherwise a single round)")
    p.add_argument("--interval", type=float, default=0.5,
                   help="seconds between rounds when watching")
    p.add_argument("--rounds", type=int,
                   help="with --watch, stop after this many rounds (default: forever)")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_sell)

    p = sub.add_parser("sell-tick", help="one auto-sell round (GUI; PERMANENT)")
    p.add_argument("--dry-run", action="store_true", dest="dry_run")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_sell_tick)

    p = sub.add_parser("sell-list", help="the auto-sell whitelist (GUI)")
    p.add_argument("--add", type=int, nargs="+", metavar="TYPE")
    p.add_argument("--remove", type=int, nargs="+", metavar="TYPE")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_sell_list)

    p = sub.add_parser("extract-tick",
                       help="one slice of vein watching (GUI; WRITES to the world)")
    p.add_argument("--gems", action="store_true", help="include gems")
    p.add_argument("--limit", type=int, help="stop after this many tiles")
    p.add_argument("--timeout", type=float, default=8.0,
                   help="seconds to wait for a batch to break")
    p.add_argument("--budget", type=float, default=0.08,
                   help="seconds to spend watching in this call")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_extract_tick)

    p = sub.add_parser("extract-stop", help="drop the vein watcher and disarm")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_extract_stop)

    p = sub.add_parser("fishing-buffs",
                       help="fishing potion effects without the potions")
    p.add_argument("--power", action="store_true", help="Fishing Potion (fishing power +15)")
    p.add_argument("--sonar", action="store_true", help="Sonar Potion (see what is biting)")
    p.add_argument("--crate", action="store_true", help="Crate Potion (more crates)")
    p.add_argument("--watch", action="store_true",
                   help="keep them up (otherwise a single round)")
    p.add_argument("--interval", type=float, default=1.0, help="seconds between rounds")
    p.add_argument("--rounds", type=int, help="with --watch, stop after this many rounds")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_fishing_buffs)

    p = sub.add_parser("catch",
                       help="reel in every bite for you (needs the auto-use cheat on)")
    p.add_argument("--recast", action="store_true",
                   help="cast again when the water is empty, once you have cast once")
    p.add_argument("--rounds", type=int, help="stop after this many rounds")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_catch)

    p = sub.add_parser("catch-tick", help="one slice of auto-catch (GUI)")
    p.add_argument("--recast", action="store_true", help="cast when the water is empty")
    p.add_argument("--budget", type=float, default=0.30,
                   help="seconds to spend watching in this call")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_catch_tick)

    p = sub.add_parser("catch-stop", help="drop the auto-catch watcher")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_catch_stop)

    p = sub.add_parser("projectile-tick", help="one slice of projectile editing (GUI)")
    p.add_argument("--set", action="append", default=[], metavar="TYPE:FIELD=VALUE",
                   help="e.g. 837:tileCollide=0 (repeatable)")
    p.add_argument("--budget", type=float, default=0.25,
                   help="seconds to spend enforcing in this call")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_projectile_tick)

    p = sub.add_parser("projectile-stop", help="drop the projectile editor state")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_projectile_stop)

    p = sub.add_parser("projectile-of", help="which projectile an item fires")
    p.add_argument("item", type=int, help="item type")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true", help="run on an unverified build")
    p.set_defaults(func=cmd_projectile_of)

    p = sub.add_parser("build-check",
                       help="report the running build and whether the cheats resolve on it")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.set_defaults(func=cmd_build_check)

    p = sub.add_parser("accept-build",
                       help="record this machine's decision about the running build")
    p.add_argument("decision", choices=["accepted", "degraded"])
    p.add_argument("--failed", nargs="*", default=[],
                   help="cheats that did not resolve (recorded so they stay disabled)")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.set_defaults(func=cmd_accept_build)

    p = sub.add_parser("spawn-npc", help="spawn an NPC beside the player")
    p.add_argument("id", type=int, help="NPCID (netID; negatives are the variants)")
    p.add_argument("--distance", type=int, default=25,
                   help="tiles behind the player to place it (default 25)")
    p.add_argument("--json", action="store_true", help="machine-readable")
    p.add_argument("--force", action="store_true",
                   help="run even when the build is not the verified one")
    p.set_defaults(func=cmd_spawn_npc)

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
