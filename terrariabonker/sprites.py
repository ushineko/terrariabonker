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

from terrariabonker import recipes, xnb
from terrariabonker.version import KNOWN_VERSION

_CACHE_ROOT = os.path.expanduser("~/.cache/terrariabonker/sprites")
# Kept under ~/.cache (not ~/.config): extraction is unprivileged, and the config dir may
# be root-owned from sudo memory commands, which would make this unwritable.
_PATHS_FILE = os.path.expanduser("~/.cache/terrariabonker/paths.json")


def cache_dir(version: str = KNOWN_VERSION) -> str:
    return os.path.join(_CACHE_ROOT, version)


def icon_path(item_id: int, version: str = KNOWN_VERSION) -> str:
    return os.path.join(cache_dir(version), f"Item_{item_id}.png")


def is_cached(version: str = KNOWN_VERSION) -> bool:
    """True once an extraction for this version has completed (marker file present)."""
    return os.path.exists(os.path.join(cache_dir(version), ".done"))


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
    """Every item the browser needs an icon for: all recipe outputs and all ingredient
    item IDs (so both the grid and the ingredient popup can render)."""
    ids: set[int] = set()
    for r in recipes.load().get("recipes", []):
        ids.add(int(r["out"]))
        for t, _ in r.get("ing", []):
            ids.add(int(t))
    return ids


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
    ids = sorted(item_ids if item_ids is not None else referenced_item_ids())
    out_dir = cache_dir(version)
    os.makedirs(out_dir, exist_ok=True)
    ok = failed = 0
    total = len(ids)
    for n, i in enumerate(ids):
        dst = icon_path(i, version)
        if not force and os.path.exists(dst):
            ok += 1
        else:
            try:
                img = xnb.read_item_texture(os.path.join(src, f"Item_{i}.xnb"))
                img.save(dst)
                ok += 1
            except Exception:                        # any malformed/unsupported sprite:
                failed += 1                          # skip it, never abort the batch
        if progress and (n % 100 == 0):
            progress(n + 1, total)
    with open(os.path.join(out_dir, ".done"), "w") as f:
        json.dump({"version": version, "ok": ok, "failed": failed, "total": total}, f)
    if progress:
        progress(total, total)
    return ok, failed, total
