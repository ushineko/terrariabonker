#!/usr/bin/env python3
"""Register (or remove) a KWin rule that remembers the panel's position.

Why the compositor and not the app: under KWin/Wayland ``QWidget.move()`` is a silent
no-op and ``pos()`` returns the value you asked for rather than where the window is
(measured: asked (700,400), Qt reported (700,400), KWin had it at (1116,1762)). An app
cannot save a position it is not allowed to read, so placement is left to KWin, which
already has a "Remember" rule type for exactly this. The panel still owns its own size.

Writes go through ``kwriteconfig6`` so the format is whatever KDE itself considers
canonical. Note it rewrites the whole file: sections are reordered and keys sorted, the
same normalisation any KDE settings dialog performs. Verified lossless here — 13 sections
in, 13 out, no key or value changed. Idempotent: re-running updates the existing rule
instead of adding another, and ``--remove`` takes it back out cleanly.

Skips quietly when the KDE config tools are absent (non-KDE desktop), so the installer can
call it unconditionally.
"""

from __future__ import annotations

import argparse
import shutil
import subprocess
import sys

FILE = "kwinrulesrc"
WMCLASS = "terrariabonker"
DESCRIPTION = "terrariabonker Remember Position"

# rule types in kwinrulesrc: 2 = Force, 4 = Remember (the idiom used by the existing
# "Always On Top" rules that keep their own position/screen).
REMEMBER = "4"

# Matching on wmclass alone also catches the app's DIALOGS, and "remember position" then
# pins each one to the panel's stored spot instead of letting KWin centre it on its parent.
# Measured: with the rule on, a dialog opened at exactly the parent's corner; with it off,
# perfectly centred. Restricting by window type (types=1, NET::NormalMask) did NOT help, so
# the discriminator is the title: only the panel's own title carries the version, while
# dialogs are named after the item, "About terrariabonker", and so on.
TITLE = "terrariabonker v"
SUBSTRING_MATCH = "2"           # 0 unimportant, 1 exact, 2 substring, 3 regex

KEYS = {
    "Description": DESCRIPTION,
    "wmclass": WMCLASS,
    "wmclassmatch": "1",
    "title": TITLE,
    "titlematch": SUBSTRING_MATCH,
    "positionrule": REMEMBER,
    "screenrule": REMEMBER,
}


def _read(group: str, key: str) -> str:
    out = subprocess.run(["kreadconfig6", "--file", FILE, "--group", group, "--key", key],
                         capture_output=True, text=True)
    return out.stdout.strip()


def _write(group: str, key: str, value: str) -> None:
    subprocess.run(["kwriteconfig6", "--file", FILE, "--group", group, "--key", key, value],
                   check=True)


def _delete(group: str, key: str) -> None:
    subprocess.run(["kwriteconfig6", "--file", FILE, "--group", group, "--key", key,
                    "--delete"], check=False)


def _rules() -> list[str]:
    raw = _read("General", "rules")
    return [r for r in raw.split(",") if r]


def _find_existing(rules: list[str]) -> str | None:
    for r in rules:
        if _read(r, "wmclass") == WMCLASS:
            return r
    return None


def _reconfigure() -> None:
    """Ask KWin to reload its rules so this takes effect without logging out."""
    for argv in (["qdbus6", "org.kde.KWin", "/KWin", "reconfigure"],
                 ["qdbus", "org.kde.KWin", "/KWin", "reconfigure"],
                 ["gdbus", "call", "--session", "--dest", "org.kde.KWin",
                  "--object-path", "/KWin", "--method", "org.kde.KWin.reconfigure"]):
        if shutil.which(argv[0]) and subprocess.run(argv, capture_output=True).returncode == 0:
            return


def install() -> int:
    rules = _rules()
    group = _find_existing(rules)
    if group is None:
        numeric = [int(r) for r in rules if r.isdigit()]
        group = str(max(numeric, default=0) + 1)
        rules.append(group)
        _write("General", "rules", ",".join(rules))
        _write("General", "count", str(len(rules)))
        print(f"[kwin] added rule [{group}] — KWin will remember the panel's position")
    else:
        print(f"[kwin] rule [{group}] already present — refreshed")
    for key, value in KEYS.items():
        _write(group, key, value)
    _reconfigure()
    return 0


def remove() -> int:
    rules = _rules()
    group = _find_existing(rules)
    if group is None:
        print("[kwin] no terrariabonker rule to remove")
        return 0
    for key in KEYS:
        _delete(group, key)
    # KWin also stores what it remembered (position/screen) on the same group, plus any
    # keys an older version of this rule wrote.
    for key in ("position", "screen", "size", "types"):
        _delete(group, key)
    rest = [r for r in rules if r != group]
    _write("General", "rules", ",".join(rest))
    _write("General", "count", str(len(rest)))
    _reconfigure()
    print(f"[kwin] removed rule [{group}]")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--remove", action="store_true", help="remove the rule instead")
    args = ap.parse_args()
    if not (shutil.which("kwriteconfig6") and shutil.which("kreadconfig6")):
        print("[kwin] KDE config tools not found — skipping the window-position rule")
        return 0
    return remove() if args.remove else install()


if __name__ == "__main__":
    sys.exit(main())
