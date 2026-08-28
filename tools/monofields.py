#!/usr/bin/env python3
"""Ask the mono runtime for field offsets by name, instead of inferring them.

Every offset in this project used to be *derived* -- from declaration order, from a
value signature in ``SetDefaults``, from watching a number change in game. That works
until it quietly does not: ``Projectile.active`` was read at ``0x03C`` for eight
releases, which is really ``Entity.wet``, and nothing caught it because a fishing bobber
floats in water and is therefore always wet. Every test agreed with the wrong number.

The runtime knows the answer. ``MonoClassField`` on 32-bit is::

    struct MonoClassField { MonoType *type; const char *name; MonoClass *parent; int offset; }

so finding a field-name string and then a pointer to it puts the offset 8 bytes further
on. Deliberately, nothing here walks ``MonoVTable`` or ``MonoClass`` -- those layouts
shift between mono builds, and avoiding them is what makes this survive an update.

    sudo python3 tools/monofields.py --verify              # check our constants (start here)
    sudo python3 tools/monofields.py active tileCollide    # what does the runtime say?
    sudo python3 tools/monofields.py --dump 0x02852ee8     # every field of one class

Read-only. It never writes to the game.
"""

from __future__ import annotations

import argparse
import os
import struct
import sys

import numpy as np

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from terrariabonker import proc                                   # noqa: E402

#: What we believe, grouped by the class that DECLARES the field. Inherited fields live
#: on the base class and do not appear in the subclass's own table, so `position` is
#: checked against Entity rather than Projectile. Keep this in step with the modules.
EXPECTED = {
    "Entity": {
        "whoAmI": 0x008, "position": 0x00C, "velocity": 0x014, "oldPosition": 0x01C,
        "direction": 0x030, "width": 0x034, "height": 0x038, "wet": 0x03C,
    },
    "Projectile": {
        "ai": 0x044, "localAI": 0x048, "active": 0x078, "bobber": 0x088,
        "scale": 0x08C, "type": 0x094, "alpha": 0x098, "aiStyle": 0x0B0,
        "timeLeft": 0x0B4, "damage": 0x0BC, "hostile": 0x0C8, "knockBack": 0x0CC,
        "friendly": 0x0D0, "penetrate": 0x0D4, "maxPenetrate": 0x0DC,
        "tileCollide": 0x100, "extraUpdates": 0x104,
    },
    "Item": {
        "useAnimation": 0x080, "useTime": 0x084, "pick": 0x090, "damage": 0x0AC,
        "knockBack": 0x0B0, "healLife": 0x0B4, "healMana": 0x0B8, "scale": 0x0CC,
        "shootSpeed": 0x100, "mana": 0x11C, "crit": 0x150, "prefix": 0x15C,
    },
    "Player": {"statLife": 0x738, "itemAnimation": 0x0BCC},
}

#: Fields the project knows about but deliberately does NOT write. Verified all the same,
#: so that "unverified" never silently becomes "forgotten".
NOT_WRITTEN = {
    "Item": {"armorPenetration": 0x154, "bonusTagDamage": 0x158, "reuseDelay": 0x164},
}

MAX_SANE_OFFSET = 0x4000       # a field past this is a false positive, not a field


def readable_regions(pid: int) -> list[tuple[int, int]]:
    """Readable non-device regions.

    ``proc.Mem.regions()`` is writable-only, which is right for finding game objects and
    wrong here: field-name strings live in the read-only mapped assembly image.
    """
    out = []
    with open(f"/proc/{pid}/maps") as f:
        for line in f:
            parts = line.split()
            if "r" not in parts[1]:
                continue
            path = parts[5] if len(parts) > 5 else ""
            if path.startswith("/dev/") or path in ("[vvar]", "[vsyscall]"):
                continue
            a, b = parts[0].split("-")
            out.append((int(a, 16), int(b, 16)))
    return out


def cstr(mem, addr: int, cap: int = 64) -> str | None:
    """A NUL-terminated printable ASCII string at ``addr``, or None."""
    raw = mem.read(addr, cap)
    if not raw:
        return None
    end = raw.find(b"\x00")
    body = raw[: end if end != -1 else cap]
    if not body or not all(32 <= c < 127 for c in body):
        return None
    return body.decode("ascii")


def find_strings(mem, regions, names) -> dict[int, str]:
    """Address -> name, for every place one of ``names`` appears NUL-terminated."""
    found: dict[int, str] = {}
    wanted = [(n, n.encode() + b"\x00") for n in names]
    for start, end in regions:
        buf = mem.read(start, end - start)
        if not buf:
            continue
        for name, pat in wanted:
            i = buf.find(pat)
            while i != -1:
                found[start + i] = name
                i = buf.find(pat, i + 1)
    return found


def find_records(mem, regions, straddrs: dict[int, str]) -> list[tuple[int, str, int]]:
    """``(klass, name, offset)`` for every MonoClassField naming one of these strings.

    A pointer to the name string IS the record's ``name`` slot, so the record starts one
    word earlier and the offset sits two words after the pointer.
    """
    if not straddrs:
        return []
    want = np.array(sorted(straddrs), dtype=np.uint32)
    out = []
    for start, end in regions:
        buf = mem.read(start, end - start)
        n = len(buf) // 4
        if n < 4:
            continue
        arr = np.frombuffer(buf[: n * 4], dtype=np.uint32)
        for i in np.where(np.isin(arr, want))[0].tolist():
            p = start + i * 4
            if p - 4 < start or p + 12 > end:
                continue
            rec = mem.read(p - 4, 16)
            if len(rec) < 16:
                continue
            _, nameptr, klass, off = struct.unpack("<4I", rec)
            name = straddrs.get(nameptr)
            # `offset` is from the object start and already counts the mono header.
            if name and 0 < klass < 0xFFFFFFFF and off <= MAX_SANE_OFFSET:
                out.append((klass, name, off))
    return out


def dump_class(mem, regions, klass: int) -> list[tuple[int, str]]:
    """Every named field of ``klass`` as ``(offset, name)``, offset-ordered.

    Static fields share the table and their offsets index static storage instead, so a
    cluster of them at low offsets is expected rather than a contradiction.
    """
    rows, seen = [], set()
    for start, end in regions:
        buf = mem.read(start, end - start)
        n = len(buf) // 4
        if n < 4:
            continue
        arr = np.frombuffer(buf[: n * 4], dtype=np.uint32)
        for i in np.where(arr == klass)[0].tolist():
            rec = start + i * 4 - 8            # parent is the third word
            if rec < start:
                continue
            raw = mem.read(rec, 16)
            if len(raw) < 16:
                continue
            _, nameptr, _, off = struct.unpack("<4I", raw)
            if off > MAX_SANE_OFFSET:
                continue
            name = cstr(mem, nameptr)
            if name and name not in seen:
                seen.add(name)
                rows.append((off, name))
    return sorted(rows)


def resolve_classes(records, expected):
    """Pick each class's MonoClass address: the one declaring the most expected fields."""
    resolved = {}
    for cls, fields in expected.items():
        score: dict[int, int] = {}
        for klass, name, off in records:
            if name in fields and fields[name] == off:
                score[klass] = score.get(klass, 0) + 1
        if score:
            resolved[cls] = max(score, key=lambda k: score[k])
    return resolved


def verify(mem, regions) -> int:
    """Check every constant we believe against the runtime. Non-zero exit on mismatch."""
    groups = {c: dict(f) for c, f in EXPECTED.items()}
    for cls, fields in NOT_WRITTEN.items():
        groups.setdefault(cls, {}).update(fields)
    names = {n for f in groups.values() for n in f}
    records = find_records(mem, regions, find_strings(mem, regions, names))
    classes = resolve_classes(records, groups)

    bad = 0
    for cls, fields in groups.items():
        klass = classes.get(cls)
        if klass is None:
            print(f"{cls}: NOT FOUND -- no class declares these fields")
            bad += len(fields)
            continue
        actual = {name: off for k, name, off in records if k == klass}
        print(f"\n{cls}  (MonoClass {klass:#010x})")
        for name, want in sorted(fields.items(), key=lambda kv: kv[1]):
            got = actual.get(name)
            note = "  [not written]" if name in NOT_WRITTEN.get(cls, {}) else ""
            if got == want:
                print(f"  ok       {name:<22} {want:#06x}{note}")
            elif got is None:
                print(f"  MISSING  {name:<22} {want:#06x} not declared here{note}")
                bad += 1
            else:
                print(f"  WRONG    {name:<22} we say {want:#06x}, runtime says {got:#06x}")
                bad += 1
    print(f"\n{bad} mismatch(es)" if bad else "\nall offsets agree with the runtime")
    return 1 if bad else 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("names", nargs="*", help="field names to look up")
    ap.add_argument("--verify", action="store_true",
                    help="check this project's offsets against the runtime")
    ap.add_argument("--dump", metavar="KLASS", help="dump every field of a MonoClass")
    args = ap.parse_args()

    pid = proc.find_pid()
    mem = proc.Mem(pid)
    regions = readable_regions(pid)

    if args.verify:
        return verify(mem, regions)

    if args.dump:
        klass = int(args.dump, 0)
        rows = dump_class(mem, regions, klass)
        print(f"class {klass:#010x}: {len(rows)} named fields\n")
        for off, name in rows:
            print(f"  {off:#06x}  {off:>6}  {name}")
        return 0

    if not args.names:
        ap.error("give some field names, or --verify / --dump")
    records = find_records(mem, regions, find_strings(mem, regions, set(args.names)))
    if not records:
        print("no field records found -- is the game running with a world loaded?")
        return 1
    for name in args.names:
        hits = sorted({(k, o) for k, n, o in records if n == name})
        print(f"\n{name}:")
        for klass, off in hits:
            print(f"  MonoClass {klass:#010x}  offset {off:#06x} ({off})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
