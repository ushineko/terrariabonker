"""Version gating: refuse to run stale offsets on the wrong Terraria build.

The field offsets in ``player`` / ``locate`` were derived against one exact build.
Terraria patches frequently, and running these writes against a build whose
layout has shifted risks corrupting the wrong fields. The locator already fails
safe (a moved layout matches nothing), but this adds a coarse, explicit gate:

- major/minor/patch change (1.4.5 -> anything else): almost certainly incompatible.
- hotfix-only change (1.4.5.7 -> 1.4.5.8) or a Steam buildid drift with the same
  version string: unproven but plausibly fine - warn and continue.

Version is read from the running game (a mono String "v1.4.5.7"); the Steam
buildid is read from the app manifest beside the installed game.
"""

from __future__ import annotations

import os
import re

KNOWN_VERSION = "1.4.5.7"       # the build these offsets were derived against
KNOWN_BUILDID = "24825745"      # Steam buildid of that build (extra fingerprint)

# UTF-16LE "vX.Y.Z[.W]" as the menu/version string appears in memory.
_VER_RE = re.compile(rb"v\x00((?:\d\x00)+(?:\.\x00(?:\d\x00)+)+)")


def detect_version(mem) -> str | None:
    """Return the game's version string (e.g. "1.4.5.7") scanned from memory."""
    counts: dict[str, int] = {}
    for start, end in mem.regions():
        buf = mem.read(start, end - start)
        if not buf:
            continue
        for m in _VER_RE.finditer(buf):
            s = m.group(1).replace(b"\x00", b"").decode("ascii", "ignore")
            if s.count(".") >= 2:            # want X.Y.Z or X.Y.Z.W, not "v1.0"
                counts[s] = counts.get(s, 0) + 1
    if not counts:
        return None
    return max(counts, key=counts.get)


def read_buildid(exe_path: str | None) -> str | None:
    """Read the Steam buildid from ``appmanifest_105600.acf`` next to the game."""
    if not exe_path:
        return None
    # .../steamapps/common/Terraria/Terraria.exe -> .../steamapps/appmanifest_105600.acf
    steamapps = exe_path
    for _ in range(3):
        steamapps = os.path.dirname(steamapps)
    manifest = os.path.join(steamapps, "appmanifest_105600.acf")
    try:
        with open(manifest) as f:
            m = re.search(r'"buildid"\s+"(\d+)"', f.read())
            return m.group(1) if m else None
    except OSError:
        return None


def _triple(v: str) -> tuple[int, ...]:
    return tuple(int(x) for x in v.split(".")[:3])


def compatibility(found_version: str | None, found_buildid: str | None):
    """Classify a build against the known-good one.

    Returns ``(level, message)`` where level is one of:
    ``"exact"``, ``"hotfix"``, ``"incompatible"``, ``"unknown"``.
    """
    if found_version is None:
        return "unknown", "could not read the game version from memory"
    if found_version == KNOWN_VERSION:
        if found_buildid and found_buildid != KNOWN_BUILDID:
            return "hotfix", (f"version {found_version} matches but Steam buildid "
                              f"{found_buildid} != known {KNOWN_BUILDID}; likely a "
                              "rebuild, offsets probably fine but unverified")
        return "exact", f"Terraria {found_version} (matches known-good build)"
    if _triple(found_version) == _triple(KNOWN_VERSION):
        return "hotfix", (f"hotfix {found_version} vs known {KNOWN_VERSION}: offsets "
                          "MIGHT still be valid but are unproven on this build")
    return "incompatible", (f"Terraria {found_version} differs from {KNOWN_VERSION} in "
                            "major/minor/patch; the offsets are almost certainly wrong")


def guard(mem, force: bool = False) -> None:
    """Print the version status and abort mutating work on an incompatible build.

    Read-only commands should not call this; it is for anything that writes.
    """
    import sys

    version = detect_version(mem)
    buildid = read_buildid(mem.exe_path())
    level, msg = compatibility(version, buildid)
    if level == "exact":
        print(f"[version] {msg}")
        return
    if level == "incompatible" and not force:
        sys.exit(f"[ABORT] {msg}.\n"
                 f"        Re-derive offsets (see docs/discovery.md) or pass --force "
                 f"to override (may corrupt player data).")
    banner = "[version] WARNING" if force else "[version] WARNING"
    print(f"{banner}: {msg}.")
    if level == "incompatible":
        print("[version] --force given; proceeding anyway. The locator still "
              "validates each match, so a wrong layout should find no player.")
