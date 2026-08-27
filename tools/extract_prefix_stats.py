#!/usr/bin/env python3
"""Regenerate ``terrariabonker/data/prefix_stats.json`` from the game's own IL.

A modifier's bonuses are not a display value: ``Item.Prefix`` multiplies the item's fields
by these numbers and stores the results. The table has 82 entries and nine possible stats
each, which is exactly the sort of thing that gets transcribed with one digit wrong and
then believed -- so it is read out of ``Item::TryGetPrefixStatMultipliersForItem`` instead.

That method is a flat ``if (prefix == N) { out_a = x; out_b = y; }`` chain, which is why a
line-by-line parse is enough and no real IL interpretation is needed.

The out-parameters are mapped to fields by reading how ``Item::Prefix`` consumes them:

    arg2 damage   arg3 knockBack   arg4 useTime/useAnimation/reuseDelay   arg5 scale
    arg6 shootSpeed   arg7 mana    arg8 crit(+)   arg9 bonusTagDamage(+)  arg10 armorPen(+)

Usage:
    python3 tools/extract_prefix_stats.py [path/to/Terraria.exe] > data/prefix_stats.json

Requires ``tools/ilrecon`` (dotnet) to dump the IL.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys

DEFAULT_EXE = ("/mnt/Data3/SteamLibrary/steamapps/common/Terraria/Terraria.exe")
ILRECON = os.path.join(os.path.dirname(os.path.abspath(__file__)), "ilrecon")
METHOD = "Terraria.Item::TryGetPrefixStatMultipliersForItem"

#: Out-parameter -> the item field it scales. Multiplicative unless listed in ADDITIVE.
ARGS = {
    "ldarg.2": "damage",
    "ldarg.3": "knockback",
    "ldarg.s 4": "usetime",        # useAnimation, useTime and reuseDelay share this one
    "ldarg.s 5": "scale",
    "ldarg.s 6": "shootspeed",
    "ldarg.s 7": "mana",
    "ldarg.s 8": "crit",
    "ldarg.s 9": "tagdamage",
    "ldarg.s 10": "armorpen",
}
ADDITIVE = {"crit", "tagdamage", "armorpen"}


def dump_il(exe: str) -> str:
    out = subprocess.run(["dotnet", "run", "--", exe, "il", METHOD],
                         cwd=ILRECON, capture_output=True, text=True, check=True)
    return out.stdout


def _literal(op: str) -> float | None:
    """The value an ``ldc.*`` op pushes, or None if it is not one."""
    if op.startswith("ldc.r4 ") or op.startswith("ldc.i4 ") or op.startswith("ldc.i4.s "):
        try:
            return float(op.rsplit(" ", 1)[1])
        except ValueError:
            return None
    m = re.fullmatch(r"ldc\.i4\.(\d)", op)
    return float(m.group(1)) if m else None


def parse(il: str) -> dict[int, dict[str, float]]:
    ops = [ln.split(": ", 1)[1].strip() for ln in il.splitlines() if ": " in ln]
    table: dict[int, dict[str, float]] = {}
    current: int | None = None
    pending: str | None = None
    for i, op in enumerate(ops):
        # `ldarg.1 ; ldc.i4 N ; bne.un` is the test that opens one prefix's block.
        if op.startswith("ldarg.1") and i + 2 < len(ops):
            n = _literal(ops[i + 1])
            if n is not None and ops[i + 2].startswith("bne"):
                current = int(n)
                table.setdefault(current, {})
                continue
        if current is None:
            continue
        if op in ARGS:
            pending = ARGS[op]
        elif pending is not None:
            value = _literal(op)
            if value is not None:
                # A multiplier of 1 (or a bonus of 0) is the default and carries no meaning.
                if value != (0.0 if pending in ADDITIVE else 1.0):
                    table[current][pending] = value
            pending = None
    return {k: v for k, v in table.items() if v}


def main() -> int:
    exe = sys.argv[1] if len(sys.argv) > 1 else DEFAULT_EXE
    table = parse(dump_il(exe))
    if len(table) < 50:
        print(f"only {len(table)} prefixes parsed — the IL shape has probably changed",
              file=sys.stderr)
        return 1
    json.dump({str(k): v for k, v in sorted(table.items())}, sys.stdout,
              indent=1, sort_keys=True)
    print()
    return 0


if __name__ == "__main__":
    sys.exit(main())
