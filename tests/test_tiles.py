"""Reading the world's tiles and finding a vein (spec 040).

The layout is not guessed at — it is what Player.PickTile does, read out of its own JIT'd
code. The trap it hides is that there are *two* sizes: the tile buffer is allocated at the
largest supported world size and reused, so its height is the indexing stride while
Main.maxTilesX/maxTilesY is the world's real extent. Reading those the other way round
walks a search past the world edge into tiles left over from the previous world.
"""

import struct

from conftest import FakeMem
from terrariabonker import tiles as T

BASE = 0x10000000
STATIC = BASE + 0x100        # pretend Main's static block starts here
BUF = BASE + 0x8000          # the tile buffer
TILES = BUF + T._ENTRIES_OFF
BOUNDS = BASE + 0x200
OBJS = BASE + 0x40000        # per-tile objects live here

STRIDE = 40                  # buffer height (bigger than the world, as in the game)
WORLD_W, WORLD_H = 20, 30


def _world(fill=None):
    """A tiny world with its own oversized buffer, laid out exactly like the game's."""
    m = FakeMem(BASE, 0x200000)
    m.write(STATIC + T.MAIN_TILE_OFF, struct.pack("<I", BUF))
    m.poke_i32(STATIC + T.MAIN_MAX_TILES_OFF, WORLD_W)
    m.poke_i32(STATIC + T.MAIN_MAX_TILES_OFF + 4, WORLD_H)
    m.write(BUF + T._BOUNDS_OFF, struct.pack("<I", BOUNDS))
    for off, v in ((0x00, 64), (0x04, 0), (0x08, STRIDE), (0x0C, 0)):
        m.poke_i32(BOUNDS + off, v)
    slot = 0
    for x in range(WORLD_W):
        for y in range(WORLD_H):
            obj = OBJS + slot * 0x18
            slot += 1
            t = 0 if fill is None else fill(x, y)
            m.write(obj + T._TILE_TYPE_OFF, struct.pack("<H", t))
            m.write(TILES + 4 * (STRIDE * x + y), struct.pack("<I", obj))
    return m


def test_the_stride_is_the_buffer_height_not_the_world_height():
    """The bug this guards: using the world height as the stride reads the wrong tile."""
    m = _world(lambda x, y: 7 if (x, y) == (5, 9) else 0)
    tm = T.TileMap(m, STATIC)
    assert tm.stride == STRIDE and tm.max_y == WORLD_H
    assert tm.stride != tm.max_y, "premise: the buffer is bigger than the world"
    assert tm.type_at(5, 9) == 7
    assert tm.type_at(5, 8) == 0


def test_the_world_extent_bounds_the_search_not_the_buffer():
    """Past maxTilesX/Y lie tiles left over from whatever world was loaded before."""
    m = _world()
    tm = T.TileMap(m, STATIC)
    assert tm.in_world(WORLD_W - 1, WORLD_H - 1)
    assert not tm.in_world(WORLD_W, 0)
    assert not tm.in_world(0, WORLD_H)
    assert tm.type_at(WORLD_W + 5, 0) is None, "read past the world edge"


def test_a_column_reads_the_same_as_single_tiles():
    m = _world(lambda x, y: (y % 5) + 1)
    tm = T.TileMap(m, STATIC)
    assert tm.column(3, 0, 10) == [tm.type_at(3, y) for y in range(10)]


def test_a_vein_is_one_ore_not_every_neighbouring_ore():
    """Copper touching iron must not take both — the flood matches the *starting* id."""
    def fill(x, y):
        if 2 <= x <= 4 and 2 <= y <= 4:
            return 7            # copper
        if x == 5 and 2 <= y <= 4:
            return 6            # iron, touching it
        return 1                # stone
    tm = T.TileMap(_world(fill), STATIC)
    vein = T.flood(tm, 3, 3, whitelist={6, 7})
    assert len(vein) == 9
    assert {tm.type_at(*p) for p in vein} == {7}


def test_a_non_whitelisted_start_mines_nothing():
    tm = T.TileMap(_world(lambda x, y: 1), STATIC)      # all stone
    assert T.flood(tm, 5, 5, whitelist=set(T.ORES)) == []


def test_the_cap_stops_a_runaway():
    """The rail that matters: a mistake must not strip a region."""
    tm = T.TileMap(_world(lambda x, y: 7), STATIC)      # the whole world is copper
    assert len(T.flood(tm, 5, 5, whitelist={7}, limit=12)) == 12


def test_a_vein_never_leaves_the_world():
    tm = T.TileMap(_world(lambda x, y: 7), STATIC)
    vein = T.flood(tm, 0, 0, whitelist={7}, limit=10_000)
    assert all(0 <= x < WORLD_W and 0 <= y < WORLD_H for x, y in vein)
    assert len(vein) == WORLD_W * WORLD_H


def test_diagonals_can_be_turned_off():
    def fill(x, y):
        return 7 if (x, y) in {(5, 5), (6, 6)} else 1   # touching only at a corner
    tm = T.TileMap(_world(fill), STATIC)
    assert len(T.flood(tm, 5, 5, whitelist={7}, diagonal=True)) == 2
    assert len(T.flood(tm, 5, 5, whitelist={7}, diagonal=False)) == 1


def test_gems_are_a_separate_list_from_ores():
    """Gems are not ores, and some players will not want them swept up."""
    assert not (set(T.ORES) & set(T.GEMS))
    assert 63 in T.GEMS and 7 in T.ORES


# --- the whitelist is checked against the game, not against memory ------------

def _tile_names():
    import json
    import os
    from terrariabonker import tiles
    path = os.path.join(os.path.dirname(os.path.abspath(tiles.__file__)),
                        "data", "tiles.json")
    with open(path) as f:
        return {int(k): v for k, v in json.load(f).items()}


def test_every_whitelisted_id_matches_the_games_own_tile_name():
    """The list began hand-written and this check found two omissions (Luminite and
    Fossil Ore) plus a wrong assumption about how the constants are typed. It exists so
    the ids are the game's answer rather than somebody's memory."""
    names = _tile_names()
    assert len(names) > 700, "tiles.json looks empty — regenerate it"
    for group, label in ((T.ORES, "ore"), (T.EXTRACTABLES, "extractable"),
                         (T.GEMS, "gem")):
        for tid, mine in group.items():
            real = names.get(tid)
            assert real is not None, f"{label} id {tid} is not a TileID at all"
            assert mine.lower().replace(" ", "") in real.lower(), \
                f"{label} {tid}: called it {mine!r}, the game calls it {real!r}"


def test_the_hardmode_and_endgame_ores_are_all_present():
    """Easy to build the list from what is in front of you in a pre-hardmode world."""
    names = _tile_names()
    for want in ("Cobalt", "Palladium", "Mythril", "Orichalcum", "Adamantite",
                 "Titanium", "Chlorophyte", "LunarOre"):
        tid = next(i for i, n in names.items() if n == want)
        assert tid in T.ORES, f"{want} (id {tid}) missing from the ore whitelist"


def test_silt_and_slush_are_swept_by_default_and_gems_are_not():
    assert T.whitelist(gems=False) >= {123, 224}
    assert not (T.whitelist(gems=False) & set(T.GEMS))
    assert T.whitelist(gems=True) > T.whitelist(gems=False)
