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

from terrariabonker import names
from terrariabonker.locate import main_static_base

_DATA = os.path.join(os.path.dirname(__file__), "data", "recipes.json")

MAIN_RECIPE_OFF = 0xA68
RECIPE_CREATE_ITEM = 0x8
RECIPE_REQUIRED_ITEM = 0xC
RECIPE_REQUIRED_TILE = 0x1C
ITEM_TYPE = 0x6C
ITEM_STACK = 0x88
ITEM_CREATE_TILE = 0xA0
ARR_LEN = 0xC
ARR_DATA = 0x10

# Station tile ids for stations that are NOT themselves crafted (so they never appear
# as a recipe output and can't be auto-named from an item's createTile). The rest are
# derived from the game at extraction time; unknown ids show as "Tile #N".
_STATIONS_NOT_CRAFTED: dict[int, str] = {
    16: "Anvil", 17: "Furnace", 26: "Demon/Crimson Altar", 77: "Hellforge",
    133: "Adamantite/Titanium Forge", 134: "Mythril/Orichalcum Anvil",
    412: "Ancient Manipulator",
}


class RecipeError(RuntimeError):
    pass


def station_name(tile: int) -> str:
    """Name for a station tile: the game-derived map (an item's createTile), then the
    not-crafted supplement, else "Tile #N"."""
    st = load().get("stations", {})
    return st.get(str(tile)) or _STATIONS_NOT_CRAFTED.get(tile) or f"Tile #{tile}"


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
    Station names are derived from each output item's ``createTile`` (an item whose
    createTile is a station tile *is* that station), which is exact and build-specific."""
    base = main_static_base(mem)
    if base is None:
        raise RecipeError("could not locate Main (get_LocalPlayer AOB missing?)")
    arr = _ru(mem, base + MAIN_RECIPE_OFF)
    maxr = _ri(mem, arr + ARR_LEN) if arr else None
    if not arr or not maxr or not (0 < maxr <= 50000):
        raise RecipeError("Main.recipe array not found or implausible")
    recipes, stations = [], {}
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
        ct = _ri(mem, ci + ITEM_CREATE_TILE)         # this output item places tile ct
        if ct is not None and ct >= 0 and ct not in stations:
            stations[ct] = names.label(ot)
        recipes.append(rec)
    return {"recipes": recipes, "stations": {str(k): v for k, v in stations.items()}}


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
