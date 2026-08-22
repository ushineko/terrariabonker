"""Item-icon cache: decode the game's ``Item_<id>.xnb`` sprites into a local PNG cache,
keyed by game version, so the recipe browser can show real icons.

Everything here is unprivileged: it reads the game's ``Content/Images`` from disk and the
running game's ``/proc/<pid>/maps`` (to learn where that is), never ``/proc/mem``. The
cache is disposable and lives under ``~/.cache`` — it is reconstitutable on any machine
from that machine's own game files (see ``extract``), so no sprites are committed.
"""

from __future__ import annotations

import json
import os

import numpy as np

from terrariabonker import names, recipes, xnb
from terrariabonker.version import KNOWN_VERSION

_CACHE_ROOT = os.path.expanduser("~/.cache/terrariabonker/sprites")
# Bump when the cached set changes so stale caches re-extract. "all" = every named item
# (not just recipe-referenced), with animated strips reduced to their first frame.
_SCOPE = "all-v1"
# Kept under ~/.cache (not ~/.config): extraction is unprivileged, and the config dir may
# be root-owned from sudo memory commands, which would make this unwritable.
_PATHS_FILE = os.path.expanduser("~/.cache/terrariabonker/paths.json")


def cache_dir(version: str = KNOWN_VERSION) -> str:
    return os.path.join(_CACHE_ROOT, version)


def icon_path(item_id: int, version: str = KNOWN_VERSION) -> str:
    return os.path.join(cache_dir(version), f"Item_{item_id}.png")


def is_cached(version: str = KNOWN_VERSION) -> bool:
    """True once an extraction for this version AND the current scope has completed. A cache
    from an older scope (e.g. recipe-referenced only) reports False so it re-extracts."""
    try:
        with open(os.path.join(cache_dir(version), ".done")) as f:
            return json.load(f).get("scope") == _SCOPE
    except (OSError, ValueError):
        return False


# --- game content directory -------------------------------------------------
def _load_paths() -> dict:
    try:
        with open(_PATHS_FILE) as f:
            return json.load(f)
    except (OSError, ValueError):
        return {}


def _save_content_dir(path: str) -> None:
    os.makedirs(os.path.dirname(_PATHS_FILE), exist_ok=True)
    d = _load_paths()
    d["content_images"] = path
    with open(_PATHS_FILE, "w") as f:
        json.dump(d, f)


def content_images_dir(mem=None) -> str | None:
    """Resolve the game's ``Content/Images`` directory. Prefers a previously learned path
    (so extraction works with the game closed); otherwise derives it from the running
    game's mapped ``Terraria.exe`` and persists it. Returns None if unknown."""
    persisted = _load_paths().get("content_images")
    if persisted and os.path.isdir(persisted):
        return persisted
    if mem is not None:
        exe = mem.exe_path()
        if exe:
            cand = os.path.join(os.path.dirname(exe), "Content", "Images")
            if os.path.isdir(cand):
                _save_content_dir(cand)
                return cand
    return None


# --- referenced items -------------------------------------------------------
def referenced_item_ids() -> set[int]:
    """Items referenced by recipes (all outputs + all ingredient IDs)."""
    ids: set[int] = set()
    for r in recipes.load().get("recipes", []):
        ids.add(int(r["out"]))
        for t, _ in r.get("ing", []):
            ids.add(int(t))
    return ids


def all_item_ids() -> set[int]:
    """Every known item ID (from the name map). The inventory can hold any item — not just
    craftable ones — so the icon cache covers them all, not only recipe-referenced items."""
    return set(names._NAMES) | referenced_item_ids()


def _deanimate(img):
    """Reduce an animated item's vertical sprite-sheet to its first frame.

    Animated items (Fallen Star, Souls, …) ship as a tall strip of equal-height frames
    separated by transparent rows; a naive decode shows the whole column. Detect a strip
    (height >= 2×width) split into N evenly-spaced content blocks and crop to the first
    frame. Single-frame items — even tall ones like a staff — have one content block and
    are returned unchanged."""
    w, h = img.size
    if h < 2 * w:
        return img
    rows = (np.asarray(img)[..., 3] > 0).any(axis=1)     # True where a row has any pixel
    blocks = []
    start = None
    for y, v in enumerate(rows):
        if v and start is None:
            start = y
        elif not v and start is not None:
            blocks.append((start, y))
            start = None
    if start is not None:
        blocks.append((start, h))
    n = len(blocks)
    if n < 2 or h % n != 0:
        return img
    fh = h // n
    for k, (s, e) in enumerate(blocks):                  # each block within its frame slot?
        if not (k * fh <= s and e <= (k + 1) * fh):
            return img
    return img.crop((0, 0, w, fh))


# --- extraction -------------------------------------------------------------
def extract(mem=None, version: str = KNOWN_VERSION, item_ids=None,
            progress=None, force: bool = False) -> tuple[int, int, int]:
    """Decode the referenced item sprites into the version cache. Idempotent: existing
    PNGs are skipped unless ``force``. ``progress(done, total)`` is called periodically.
    Returns ``(ok, failed, total)``. Raises ``RuntimeError`` if the game files can't be
    found."""
    src = content_images_dir(mem)
    if not src:
        raise RuntimeError(
            "cannot find Terraria's Content/Images — launch the game once so its path "
            "can be learned, then extract.")
    ids = sorted(item_ids if item_ids is not None else all_item_ids())
    out_dir = cache_dir(version)
    os.makedirs(out_dir, exist_ok=True)
    # A stale cache (older scope) must be rebuilt, not skipped — its PNGs may be wrong
    # (missing items, un-cropped animations). Treat a scope mismatch like ``force``.
    refresh = force or not is_cached(version)
    ok = failed = 0
    total = len(ids)
    for n, i in enumerate(ids):
        dst = icon_path(i, version)
        if not refresh and os.path.exists(dst):
            ok += 1
        else:
            try:
                img = _deanimate(xnb.read_item_texture(os.path.join(src, f"Item_{i}.xnb")))
                img.save(dst)
                ok += 1
            except Exception:                        # any malformed/unsupported sprite:
                failed += 1                          # skip it, never abort the batch
        if progress and (n % 100 == 0):
            progress(n + 1, total)
    with open(os.path.join(out_dir, ".done"), "w") as f:
        json.dump({"version": version, "scope": _SCOPE,
                   "ok": ok, "failed": failed, "total": total}, f)
    if progress:
        progress(total, total)
    return ok, failed, total
