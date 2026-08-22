"""Crafting-recipe extraction and lookup.

Terraria builds its recipes in code (`Recipe.SetupRecipes`), not a data table, so
there is nothing to read statically from the exe (unlike item names). The fully-built
list lives at runtime in `Main.recipe : Recipe[]`, so we extract it once from the
running game — over `/proc`, walking the mono array from `Main`'s static base — and
cache it to `data/recipes.json`. The browser then works offline from the cache.

Recipe object layout (1.4.5.7): `createItem` (Item) @ 0x8, `requiredItem` (Item[]) @
0xC, `requiredTile` (int, the station tile id; <0 = hand-crafted) @ 0x1C. `Main.recipe`
is at Main-static +0xA68. Item type/stack are the usual +0x6C / +0x88.
"""

from __future__ import annotations

import json
import os
import struct

from terrariabonker import names
from terrariabonker.locate import _exec_regions, main_static_base, read_mono_string
from terrariabonker.patcher import _pat

_DATA = os.path.join(os.path.dirname(__file__), "data", "recipes.json")

MAIN_RECIPE_OFF = 0xA68
RECIPE_CREATE_ITEM = 0x8
RECIPE_REQUIRED_ITEM = 0xC
RECIPE_REQUIRED_TILE = 0x1C
ITEM_TYPE = 0x6C
ITEM_STACK = 0x88
ITEM_CREATE_TILE = 0xA0          # Item.createTile (placeable tile id, or -1)
ITEM_PLACE_STYLE = 0xA8          # Item.placeStyle (which style within that tile)
ARR_LEN = 0xC
ARR_DATA = 0x10
LOCALIZEDTEXT_VALUE = 0xC        # LocalizedText._value (String*)

# AOBs (operand at offset 2) that read the two statics the tile-name lookup needs:
# MapHelper.tileLookup (ushort[]: tileType -> map-legend index) from TileToLookup's
# `lea eax,[eax+esi*2+10]; movzx eax,word[eax]; add eax,edi`, and Lang._mapLegendCache
# (LocalizedText[]) from GetMapObjectName's double-read + array index.
_TILELOOKUP_PAT = _pat("8B 05 ?? ?? ?? ?? 39 70 0C 0F 86 ?? ?? ?? ?? "
                       "8D 44 70 10 0F B7 00 03 C7")
_MAPLEGEND_PAT = _pat("8B 05 ?? ?? ?? ?? 85 C0 74 24 8B 05 ?? ?? ?? ?? "
                      "8B 4D 08 39 48 0C 0F 86 ?? ?? ?? ?? 8D 44 88 10 8B 00")


class RecipeError(RuntimeError):
    pass


def station_name(tile: int) -> str:
    """Name for a station tile from the cached game-derived map, else "Tile #N"."""
    return load().get("stations", {}).get(str(tile)) or f"Tile #{tile}"


# --- extraction (reads the running game) -------------------------------------
def _ri(mem, addr):
    try:
        return mem.read_i32(addr)
    except Exception:
        return None


def _ru(mem, addr):
    try:
        return mem.read_u32(addr)
    except Exception:
        return None


def _resolve_operand(mem, pat) -> int | None:
    """Unique match of ``pat``; return the u32 operand of its `mov eax,[abs]` (offset 2)."""
    seed_off, seed = pat.seed()
    found = None
    for start, end in _exec_regions(mem):
        buf = mem.read(start, end - start)
        i = buf.find(seed)
        while i != -1:
            pos = i - seed_off
            if pat.matches(buf, pos):
                if found is not None:
                    return None                      # not unique
                found = start + pos
            i = buf.find(seed, i + 1)
    return mem.read_u32(found + 2) if found is not None else None


def _tile_names(mem, tiles) -> dict[str, str]:
    """Map station tile ids -> display names via Lang._mapLegendCache[tileLookup[t]].
    This is Terraria's own tile name (what the map/crafting UI show). {} if the AOBs
    aren't found (game updated) — callers then fall back to "Tile #N"."""
    tl_addr = _resolve_operand(mem, _TILELOOKUP_PAT)
    ml_addr = _resolve_operand(mem, _MAPLEGEND_PAT)
    if not tl_addr or not ml_addr:
        return {}
    tl, ml = _ru(mem, tl_addr), _ru(mem, ml_addr)
    tl_len = _ri(mem, tl + ARR_LEN) if tl else 0
    ml_len = _ri(mem, ml + ARR_LEN) if ml else 0
    out = {}
    for t in tiles:
        if not (tl_len and 0 <= t < tl_len):
            continue
        idx = struct.unpack("<H", mem.read(tl + ARR_DATA + t * 2, 2))[0]
        if not (ml_len and 0 <= idx < ml_len):
            continue
        lt = _ru(mem, ml + ARR_DATA + idx * 4)
        s = read_mono_string(mem, _ru(mem, lt + LOCALIZEDTEXT_VALUE)) if lt else None
        if s:
            out[str(t)] = s
    return out


def _read_ingredients(mem, item_arr: int | None) -> list[list[int]]:
    if not item_arr:
        return []
    n = _ri(mem, item_arr + ARR_LEN) or 0
    out = []
    for j in range(min(n, 40)):
        it = _ru(mem, item_arr + ARR_DATA + j * 4)
        if not it:
            continue
        t = _ri(mem, it + ITEM_TYPE)
        if t:
            out.append([t, _ri(mem, it + ITEM_STACK) or 1])
    return out


def extract(mem) -> dict:
    """Walk `Main.recipe[]` in the running game. Returns
    ``{"recipes": [{"out", "n", "ing": [[id,count],...], "tile"?}], "stations": {tile: name}}``.
    Station names come from Terraria's own tile display names (see `_tile_names`), so
    they are exact and build-specific."""
    base = main_static_base(mem)
    if base is None:
        raise RecipeError("could not locate Main (get_LocalPlayer AOB missing?)")
    arr = _ru(mem, base + MAIN_RECIPE_OFF)
    maxr = _ri(mem, arr + ARR_LEN) if arr else None
    if not arr or not maxr or not (0 < maxr <= 50000):
        raise RecipeError("Main.recipe array not found or implausible")
    recipes, tiles, tileicons = [], set(), {}
    for i in range(maxr):
        ro = _ru(mem, arr + ARR_DATA + i * 4)
        if not ro:
            continue
        ci = _ru(mem, ro + RECIPE_CREATE_ITEM)
        ot = _ri(mem, ci + ITEM_TYPE) if ci else None
        if not ot:                                   # empty (unused) recipe slot
            continue
        rec = {"out": ot, "n": _ri(mem, ci + ITEM_STACK) or 1,
               "ing": _read_ingredients(mem, _ru(mem, ro + RECIPE_REQUIRED_ITEM))}
        tile = _ri(mem, ro + RECIPE_REQUIRED_TILE)
        if tile is not None and tile >= 0:
            rec["tile"] = tile
            tiles.add(tile)
        # Placeable outputs record their tile + style so a sprite-less item (e.g. a
        # trapped chest, which has no Item_<id>.xnb) can be drawn from the tile sheet.
        ct = _ri(mem, ci + ITEM_CREATE_TILE)
        if ct is not None and ct >= 0 and str(ot) not in tileicons:
            tileicons[str(ot)] = [ct, _ri(mem, ci + ITEM_PLACE_STYLE) or 0]
        recipes.append(rec)
    return {"recipes": recipes, "stations": _tile_names(mem, tiles),
            "tileicons": tileicons}


def save(data: dict, path: str = _DATA) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        json.dump(data, f, separators=(",", ":"))


# --- browsing (reads the cache) ----------------------------------------------
_CACHE: dict | None = None


def load(path: str = _DATA) -> dict:
    global _CACHE
    if _CACHE is None:
        try:
            with open(path) as f:
                _CACHE = json.load(f)
        except (OSError, ValueError):
            _CACHE = {}
        _CACHE.setdefault("recipes", [])
        _CACHE.setdefault("stations", {})
        _CACHE.setdefault("tileicons", {})
    return _CACHE


def _ids_for(query: str) -> set[int]:
    if query.strip().isdigit():
        return {int(query)}
    return {i for i, _ in names.search(query, limit=60)}


def by_output(query: str, limit: int = 200) -> list[dict]:
    """Recipes whose output item matches ``query`` (an item name substring or ItemID)."""
    ids = _ids_for(query)
    return [r for r in load()["recipes"] if r["out"] in ids][:limit]


def using(item_query: str, limit: int = 200) -> list[dict]:
    """Recipes that use ``item_query`` as an ingredient (what can I make with X)."""
    ids = _ids_for(item_query)
    return [r for r in load()["recipes"]
            if any(t in ids for t, _ in r["ing"])][:limit]
