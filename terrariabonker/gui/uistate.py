"""Unprivileged panel state: the window size.

Kept under ``~/.cache`` alongside the sprite cache, and for the same reason recorded
there: the config directory can be root-owned from sudo memory commands, so the
unprivileged panel cannot write into it. Ownership follows who writes — this file is
written only by the GUI, so it belongs to the user.

Only the SIZE is stored. Position is the compositor's job: under KWin/Wayland
``QWidget.move()`` is a silent no-op and ``pos()`` reports the value you asked for rather
than the truth (measured: asked (700,400), Qt reported (700,400), KWin had placed the
window at (1116,1762)). Guessing from those numbers would save nonsense, so the installer
registers a KWin "remember position" rule instead and lets KWin own placement.
"""

from __future__ import annotations

import json
import os

_PATH = os.path.expanduser("~/.cache/terrariabonker/window.json")

_MIN, _MAX = 200, 10000          # ignore absurd sizes (corrupt file, monitor unplugged)


def load_size() -> tuple[int, int] | None:
    """Saved (width, height), or None when unset or implausible."""
    try:
        with open(_PATH) as f:
            d = json.load(f)
        w, h = int(d["w"]), int(d["h"])
    except (OSError, ValueError, KeyError, TypeError):
        return None
    if _MIN <= w <= _MAX and _MIN <= h <= _MAX:
        return w, h
    return None


def save_size(width: int, height: int) -> None:
    """Best effort: never let a settings write stop the panel from closing."""
    try:
        os.makedirs(os.path.dirname(_PATH), exist_ok=True)
        tmp = _PATH + ".tmp"
        with open(tmp, "w") as f:
            json.dump({"w": int(width), "h": int(height)}, f)
        os.replace(tmp, _PATH)
    except OSError:
        pass
