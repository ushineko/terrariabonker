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
# Bump when the cached set changes so stale caches re-extract. all-v1 = every named item
# with animated strips reduced to their first frame; all-v2 adds tile-sheet icons for
# sprite-less placeable items (trapped chests); all-v3 adds NPC sheets cropped with the
# game's own Main.npcFrameCount; all-v4 also crops the grid sheets (Queen Slime,
# Deerclops, Moon Lord, the Ogres) to their top-left cell.
_SCOPE = "all-v4"
# A frame shorter than this means the count is not describing this sheet.
_MIN_FRAME_PX = 4
# How unequal grid blocks may be before we stop believing it is a grid.
_GRID_EVENNESS = 1.35
# How much of the neutral sheet survives before the tint is added on top.
_TINT_KEEP = 0.45
# Kept under ~/.cache (not ~/.config): extraction is unprivileged, and the config dir may
# be root-owned from sudo memory commands, which would make this unwritable.
_PATHS_FILE = os.path.expanduser("~/.cache/terrariabonker/paths.json")


def cache_dir(version: str = KNOWN_VERSION) -> str:
    return os.path.join(_CACHE_ROOT, version)


def icon_path(item_id: int, version: str = KNOWN_VERSION) -> str:
    return os.path.join(cache_dir(version), f"Item_{item_id}.png")


def npc_icon_path(npc_type: int, version: str = KNOWN_VERSION) -> str:
    """NPC sprites share the cache but not the namespace — ids collide with items'."""
    return os.path.join(cache_dir(version), f"NPC_{npc_type}.png")


# Written by the privileged side (Service.compendium) because it comes out of the game's
# memory; read here, where extraction runs unprivileged.
_NPC_FRAMES_FILE = os.path.expanduser("~/.cache/terrariabonker/npcframes.json")


def npc_tinted_icon_path(net_id: int, version: str = KNOWN_VERSION) -> str:
    """A tinted variant's own icon.

    The sheet is per *type* but the tint is per *netID* — every coloured slime shares one
    neutral sheet and differs only by ``NPC.color`` — so a tinted variant cannot reuse the
    type's icon and gets its own file.
    """
    return os.path.join(cache_dir(version), f"NPCt_{net_id}.png")


def save_npc_draw_data(frames: dict, tints: dict) -> None:
    """Persist what the extractor needs from the game's memory. Best effort.

    ``frames`` is ``{type: frame count}``; ``tints`` is
    ``{net_id: {"type": t, "color": [r, g, b, a]}}`` for the netIDs the game tints.
    """
    try:
        os.makedirs(os.path.dirname(_NPC_FRAMES_FILE), exist_ok=True)
        tmp = _NPC_FRAMES_FILE + ".tmp"
        with open(tmp, "w") as f:
            json.dump({"frames": {str(k): int(v) for k, v in frames.items()},
                       "tints": {str(k): v for k, v in tints.items()}}, f)
        os.replace(tmp, _NPC_FRAMES_FILE)
    except (OSError, TypeError, ValueError):
        pass


def load_npc_draw_data() -> tuple[dict[int, int], dict[int, dict]]:
    try:
        with open(_NPC_FRAMES_FILE) as f:
            blob = json.load(f)
        frames = {int(k): int(v) for k, v in blob.get("frames", {}).items()}
        tints = {int(k): v for k, v in blob.get("tints", {}).items()}
        return frames, tints
    except (OSError, ValueError, AttributeError):
        return {}, {}


def load_npc_frame_counts() -> dict[int, int]:
    return load_npc_draw_data()[0]


def _tinted(img, color):
    """Paint a neutral sheet with the game's tint.

    Terraria attenuates the lit texture and *adds* ``NPC.color`` on top, which is why a
    Blue Slime's grey gel comes out blue. Reproduced closely enough for a 40px icon:
    multiplying instead comes out far too dark to read at that size.
    """
    from PIL import Image

    a = np.asarray(img.convert("RGBA")).astype("float32")
    a[:, :, :3] = a[:, :, :3] * _TINT_KEEP + np.array(color[:3], dtype="float32")
    return Image.fromarray(a.clip(0, 255).astype("uint8"), "RGBA")


def is_cached(version: str = KNOWN_VERSION) -> bool:
    """True once an extraction for this version AND the current scope has completed. A cache
    from an older scope (e.g. recipe-referenced only) reports False so it re-extracts.

    An extraction that ran before the NPC frame counts were published also reports False:
    it skipped the NPC sheets, so the cache is real but incomplete, and the next run — by
    which time the catalog fetch has published the counts — finishes the job.
    """
    try:
        with open(os.path.join(cache_dir(version), ".done")) as f:
            done = json.load(f)
    except (OSError, ValueError):
        return False
    return done.get("scope") == _SCOPE and done.get("npcs", 0) > 0


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


def _tile_icon(src: str, tile: int, style: int, sheet_cache: dict):
    """Render a 32x32 icon for a placeable item that has no ``Item_<id>.xnb`` (chests) by
    compositing the four 16x16 tiles of its 2x2 chest from ``Tiles_<tile>.xnb`` at the
    given ``placeStyle``. Returns a PIL image, or None if the sheet/style is unavailable.

    Tiles are 16px with 2px padding (18px pitch); a chest is 2x2 tiles = 36px pitch. Styles
    run left-to-right and wrap by the sheet width (row pitch 38px). Compositing the four
    tiles adjacently drops the internal padding so the chest is seamless."""
    sheet = sheet_cache.get(tile)
    if sheet is None:
        path = os.path.join(src, f"Tiles_{tile}.xnb")
        try:
            sheet = xnb.read_item_texture(path) if os.path.exists(path) else False
        except Exception:
            sheet = False
        sheet_cache[tile] = sheet
    if not sheet:
        return None
    return _composite_chest(sheet, style)


def _composite_chest(sheet, style: int):
    """Composite the 2x2 chest at ``style`` from a Containers tile sheet into a 32x32 image
    (the four 16x16 tiles placed adjacently, dropping the 2px inter-tile padding)."""
    from PIL import Image

    per_row = max(1, sheet.width // 36)
    fx = (style % per_row) * 36
    fy = (style // per_row) * 38
    out = Image.new("RGBA", (32, 32), (0, 0, 0, 0))
    for sx, sy, dx, dy in ((0, 0, 0, 0), (18, 0, 16, 0), (0, 18, 0, 16), (18, 18, 16, 16)):
        box = (fx + sx, fy + sy, fx + sx + 16, fy + sy + 16)
        if box[2] <= sheet.width and box[3] <= sheet.height:
            t = sheet.crop(box)
            out.paste(t, (dx, dy), t)
    return out


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
def _runs(empty) -> list[tuple[int, int]]:
    """Runs of content (False) in a boolean "this line is fully transparent" mask."""
    out, start = [], None
    for i, e in enumerate(empty):
        if not e and start is None:
            start = i
        elif e and start is not None:
            out.append((start, i - 1))
            start = None
    if start is not None:
        out.append((start, len(empty) - 1))
    return out


def _first_grid_cell(img):
    """Crop a grid sheet to its top-left cell.

    Not every NPC sheet is a vertical strip. Some lay frames out in columns too — Queen
    Slime's 16 frames are 2 columns of 8, Deerclops uses 5 columns, Moon Lord a 3x3 — and
    ``npcFrameCount`` counts frames, not rows, so the vertical crop alone leaves a row of
    little pictures.

    A grid gives itself away as several **evenly sized** blocks separated by fully
    transparent lines. Anything else is left alone, which is what stops this slicing a
    single sprite that happens to have a detached piece: those blocks differ in size, so
    the evenness test rejects them. Measured over all 697 sheets, it changes 19 and leaves
    678 untouched.
    """
    for axis in (0, 1):                     # columns first, then rows
        blocks = _even_blocks(img, axis)
        if not blocks:
            continue                        # one sprite with detached parts, not a grid
        lo, hi = blocks[0]
        w, h = img.size
        img = img.crop((lo, 0, hi + 1, h)) if axis == 0 else img.crop((0, lo, w, hi + 1))
    return img


def _even_blocks(img, axis: int):
    """Evenly sized content blocks along an axis, or None if this is not a grid."""
    a = np.asarray(img.convert("RGBA"))
    blocks = _runs((a[:, :, 3] == 0).all(axis=axis))
    if len(blocks) < 2:
        return None
    sizes = [hi - lo + 1 for lo, hi in blocks]
    return blocks if max(sizes) <= min(sizes) * _GRID_EVENNESS else None


def _first_frame(img, frames: int):
    """Crop an NPC sheet to its first frame, using the game's own frame count.

    Exact where ``_deanimate`` is a guess: that one infers a strip from
    ``height >= 2*width`` and evenly spaced content blocks, which is right for tall item
    strips and wrong for wide NPCs — a two-frame Blue Slime sheet is 32x52, and a
    one-frame Moon Lord is 573x804.

    **The frame count counts frames, not rows.** Where a sheet is laid out in columns as
    well, the rows are ``frames / columns`` — Queen Slime's 16 frames are 2 columns of 8,
    so dividing the height by 16 yields half a slime. Columns are counted first, and the
    height divided by the rows that implies.

    The division is floored rather than required to be exact: several sheets carry a few
    rows of padding (Duke Fishron is 1298 tall over 8 frames, Skeletron Prime 940 over 6),
    and refusing those left the whole strip on screen.
    """
    w, h = img.size
    cols = _even_blocks(img, 0)
    if cols and frames % len(cols) == 0 and h // (frames // len(cols)) >= _MIN_FRAME_PX:
        lo, hi = cols[0]
        return img.crop((lo, 0, hi + 1, h // (frames // len(cols))))
    if frames >= 2 and h // frames >= _MIN_FRAME_PX:
        img = img.crop((0, 0, w, h // frames))
    return _first_grid_cell(img)


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
    # No frame counts means no way to crop a sheet to one frame, so NPCs are skipped
    # rather than cached as whole strips: a wrong icon would persist until the scope is
    # bumped, while a missing one is fixed by the next run. The counts come from the
    # privileged side (Service.compendium), which may not have run yet.
    npc_frames, npc_tints = load_npc_draw_data()
    npc_types = sorted(npc_frames)
    out_dir = cache_dir(version)
    os.makedirs(out_dir, exist_ok=True)
    # A stale cache (older scope) must be rebuilt, not skipped — its PNGs may be wrong
    # (missing items, un-cropped animations). Treat a scope mismatch like ``force``.
    refresh = force or not is_cached(version)
    tileicons = recipes.load().get("tileicons", {})   # itemID -> [createTile, placeStyle]
    sheet_cache: dict = {}
    ok = failed = 0
    total = len(ids) + len(npc_types)
    for n, i in enumerate(ids):
        dst = icon_path(i, version)
        if not refresh and os.path.exists(dst):
            ok += 1
        else:
            img = None
            xnb_path = os.path.join(src, f"Item_{i}.xnb")
            if os.path.exists(xnb_path):
                try:
                    img = _deanimate(xnb.read_item_texture(xnb_path))
                except Exception:                    # malformed/unsupported item sprite
                    img = None
            if img is None:                          # no item sprite (e.g. trapped chest):
                ti = tileicons.get(str(i))           # draw it from the tile sheet
                if ti:
                    img = _tile_icon(src, ti[0], ti[1], sheet_cache)
            if img is not None:
                img.save(dst)
                ok += 1
            else:
                failed += 1
        if progress and (n % 100 == 0):
            progress(n + 1, total)

    done_items = len(ids)
    for n, t in enumerate(npc_types):
        dst = npc_icon_path(t, version)
        if not refresh and os.path.exists(dst):
            ok += 1
        else:
            img = None
            xnb_path = os.path.join(src, f"NPC_{t}.xnb")
            if os.path.exists(xnb_path):
                try:
                    img = _first_frame(xnb.read_item_texture(xnb_path), npc_frames[t])
                except Exception:                # malformed/unsupported NPC sheet
                    img = None
            if img is not None:
                img.save(dst)
                ok += 1
            else:
                failed += 1
        if progress and (n % 50 == 0):
            progress(done_items + n + 1, total)

    # Tinted variants. Every coloured slime shares one neutral sheet and differs only by
    # NPC.color, which is per netID, so each needs its own icon rather than the type's.
    from PIL import Image

    for net_id, spec in npc_tints.items():
        dst = npc_tinted_icon_path(net_id, version)
        if not refresh and os.path.exists(dst):
            continue
        base = npc_icon_path(spec.get("type", net_id), version)
        if not os.path.exists(base):
            continue
        try:
            _tinted(Image.open(base), spec["color"]).save(dst)
        except Exception:                    # a tint we cannot apply is not a failure
            pass

    with open(os.path.join(out_dir, ".done"), "w") as f:
        json.dump({"version": version, "scope": _SCOPE, "ok": ok, "failed": failed,
                   "total": total, "npcs": len(npc_types),
                   "tinted": len(npc_tints)}, f)
    if progress:
        progress(total, total)
    return ok, failed, total
