"""Unprivileged panel state: window size, Effects switches, projectile overrides.

Kept under ``~/.cache`` alongside the sprite cache, and for the same reason recorded
there: the config directory can be root-owned from sudo memory commands, so the
unprivileged panel cannot write into it. Ownership follows who writes — this file is
written only by the GUI, so it belongs to the user.

**Why not `profile.json`.** The desired-cheat profile lives under `~/.config` and is
written by the privileged side; the config directory can be root-owned from sudo memory
commands, so the unprivileged panel cannot write into it. The Effects switches are driven
by the panel's own timers, so their state is the panel's to keep, and it belongs beside the
window size where ownership follows who writes.

Position is the compositor's job: under KWin/Wayland
``QWidget.move()`` is a silent no-op and ``pos()`` reports the value you asked for rather
than the truth (measured: asked (700,400), Qt reported (700,400), KWin had placed the
window at (1116,1762)). Guessing from those numbers would save nonsense, so the installer
registers a KWin "remember position" rule instead and lets KWin own placement.
"""

from __future__ import annotations

import json
import os

#: Where this lives by design. Kept separate from ``_PATH`` because tests redirect the
#: latter to a tmp file, and "it belongs in ~/.cache, not the root-ownable ~/.config" is a
#: decision worth asserting on independently of wherever a given process is pointed.
_DEFAULT_PATH = os.path.expanduser("~/.cache/terrariabonker/window.json")
_PATH = _DEFAULT_PATH

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


def _read() -> dict:
    try:
        with open(_PATH) as f:
            d = json.load(f)
        return d if isinstance(d, dict) else {}
    except (OSError, ValueError):
        return {}


def _write(d: dict) -> None:
    """Best effort: never let a settings write stop the panel from closing."""
    try:
        os.makedirs(os.path.dirname(_PATH), exist_ok=True)
        tmp = _PATH + ".tmp"
        with open(tmp, "w") as f:
            json.dump(d, f)
        os.replace(tmp, _PATH)
    except OSError:
        pass


def save_size(width: int, height: int) -> None:
    """Record the window size, keeping whatever else is in the file.

    Merged rather than written fresh: this used to dump `{"w", "h"}` over the whole file,
    so anything else stored here would be dropped every time the panel closed -- which is
    the one moment it is guaranteed to happen.
    """
    d = _read()
    d.update(w=int(width), h=int(height))
    _write(d)


def load_effects() -> dict:
    """Which Effects switches were on, and the numbers beside them. ``{}`` when unset."""
    got = _read().get("effects")
    return got if isinstance(got, dict) else {}


def save_effects(state: dict) -> None:
    """Record the Effects panel, keeping the window size."""
    d = _read()
    d["effects"] = {k: v for k, v in state.items() if isinstance(v, (bool, int))}
    _write(d)


def load_projectiles() -> dict:
    """Saved projectile overrides as ``{projectile type: {field: value}}``. ``{}`` unset.

    Keys come back from JSON as strings and are converted here, because everything
    downstream keys on an int projectile type and a silently-stringy key would match
    nothing while looking perfectly correct in the file.
    """
    got = _read().get("projectiles")
    if not isinstance(got, dict):
        return {}
    out: dict[int, dict] = {}
    for ptype, fields in got.items():
        try:
            key = int(ptype)
        except (TypeError, ValueError):
            continue
        if isinstance(fields, dict):
            clean = {k: v for k, v in fields.items() if isinstance(v, (int, float))}
            if clean:
                out[key] = clean
    return out


def save_projectiles(state: dict) -> None:
    """Record projectile overrides, keeping the window size and Effects."""
    d = _read()
    d["projectiles"] = {str(int(t)): {k: v for k, v in f.items()
                                      if isinstance(v, (int, float))}
                        for t, f in state.items() if f}
    _write(d)
