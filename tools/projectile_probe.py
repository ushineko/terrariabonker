#!/usr/bin/env python3
"""Enforce field values on live projectiles, and report what the game did with them.

Recon for the projectile-editor idea (see `docs/item-fields.md`). Run it yourself while
playing rather than on someone else's cue -- every attempt to co-ordinate "fire now" with a
probe window failed, because the probe's output only appears once it has finished.

    sudo python3 tools/projectile_probe.py --type 837 --set tileCollide=1
    sudo python3 tools/projectile_probe.py --type 837 --set scale=2.5 --set penetrate=-1
    sudo python3 tools/projectile_probe.py --watch          # just report what is flying

Values are ENFORCED on every pass, not written once: a fast weapon reuses projectile slots
between polls, so "patch each new slot" misses nearly everything -- 60 seconds of sustained
fire once produced three detections.

With `--ab`, even-numbered slots are patched and odd ones are left alone, so a type that
collides anyway cannot be mistaken for the edit working. The verdict column asks the tile
map whether the projectile is standing inside a solid tile, which is a fact rather than an
impression of what happened on screen.

Offsets are from `Projectile.SetDefaults`' own declared values (see docs/item-fields.md);
`position` is Entity's, shared by every subclass.
"""

from __future__ import annotations

import argparse
import collections
import os
import struct
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from terrariabonker import locate, proc, tiles                     # noqa: E402
from terrariabonker import projectiles as P                        # noqa: E402

ACTIVE, POSX, PTYPE = 0x03C, 0x00C, 0x094

#: name -> (offset, kind). Solved against the game's own SetDefaults, except where noted.
FIELDS = {
    "scale": (0x08C, "f32"),
    "alpha": (0x098, "i32"),
    "aiStyle": (0x0B0, "i32"),
    "timeLeft": (0x0B4, "i32"),
    "friendly": (0x0D0, "i32"),
    "penetrate": (0x0D4, "i32"),
    "maxPenetrate": (0x0DC, "i32"),
    "tileCollide": (0x100, "i32"),
    "extraUpdates": (0x104, "i32"),
    "width": (0x034, "i32"),
    "height": (0x038, "i32"),
}


def parse_set(pairs):
    out = {}
    for p in pairs:
        name, _, value = p.partition("=")
        if name not in FIELDS:
            sys.exit(f"unknown field {name!r}; known: {', '.join(sorted(FIELDS))}")
        out[name] = float(value) if FIELDS[name][1] == "f32" else int(value)
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--type", type=int, help="projectile type; omit for every type")
    ap.add_argument("--set", action="append", default=[], metavar="FIELD=VALUE")
    ap.add_argument("--ab", action="store_true",
                    help="patch even slots only, leaving odd ones as a control")
    ap.add_argument("--seconds", type=float, default=120.0)
    ap.add_argument("--watch", action="store_true", help="report only, write nothing")
    args = ap.parse_args()

    wanted = {} if args.watch else parse_set(args.set)
    mem = proc.Mem(proc.find_pid())
    base = locate.main_static_base(mem)
    if base is None:
        sys.exit("could not find Main's statics -- is a world loaded?")
    arr = P.projectile_array(mem, base)
    if not arr:
        sys.exit("could not find Main.projectile")
    tm = tiles.TileMap(mem, base)

    print(f"watching for {args.seconds:.0f}s. Fire whenever -- nothing here needs timing.")
    if wanted:
        print(f"enforcing {wanted} on {'every other slot' if args.ab else 'every projectile'}"
              f"{'' if args.type is None else f' of type {args.type}'}")
    print()

    stat = collections.defaultdict(lambda: [0, 0])
    t0 = time.time()
    while time.time() - t0 < args.seconds:
        for slot, obj in enumerate(struct.unpack("<1001I", mem.read(arr + 0x10, 4004))):
            if not obj or mem.read(obj + ACTIVE, 1) != b"\x01":
                continue
            ptype = mem.read_i32(obj + PTYPE)
            if args.type is not None and ptype != args.type:
                continue
            patched = (not args.ab) or (slot % 2 == 0)
            if wanted and patched:
                for name, value in wanted.items():
                    off, kind = FIELDS[name]
                    if kind == "f32":
                        mem.write(obj + off, struct.pack("<f", value))
                    else:
                        mem.write_i32(obj + off, int(value))
            x, y = struct.unpack("<ff", mem.read(obj + POSX, 8))
            if not (0 < x < 1e7 and 0 < y < 1e7):
                continue
            solid = tm.solid_type_at(int(x // 16), int(y // 16)) is not None
            stat[(ptype, patched)][0 if solid else 1] += 1
        time.sleep(1 / 120)

    if not stat:
        print("nothing was flying. Fire something while this runs.")
        return 0
    print(f"{'type':>6} {'patched':>8} {'in blocks':>10} {'in open':>9} {'% in blocks':>12}")
    for (ptype, patched), (ins, outs) in sorted(stat.items()):
        total = ins + outs
        print(f"{ptype:>6} {('yes' if patched else 'control'):>8} {ins:>10} {outs:>9} "
              f"{100 * ins / total:>11.1f}%")
    print("\nA projectile that respects tileCollide is essentially never inside a solid tile.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
