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


def _world(fill=None, active=None):
    """A tiny world with its own oversized buffer, laid out exactly like the game's.

    ``active`` decides which tiles carry the active bit; by default any non-zero id does,
    which is how a real world looks. Pass one to model tiles that have been mined out --
    the game keeps their id and only clears the bit.
    """
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
            on = bool(t) if active is None else active(x, y)
            m.write(obj + T._TILE_TYPE_OFF, struct.pack("<H", t))
            m.write(obj + T._TILE_HEADER_OFF,
                    struct.pack("<H", T._ACTIVE_BIT if on else 0))
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


# --- the stub's slot ---------------------------------------------------------

def _ore_stub():
    """Build the extractor stub against stand-ins for the addresses it bakes in."""
    from terrariabonker import patcher as P
    import terrariabonker.locate as L

    class FakePatcher:
        _inj = {}

        def _resolve(self, key):
            return 0x2178CD48

        class mem:
            @staticmethod
            def read_u32(a):
                return 0x5BDBAD4 if a == 0x1000 - 0xA else 0x5BDBAD0

    real = L.find_localplayer_anchor
    L.find_localplayer_anchor = lambda mem: 0x1000
    try:
        inj = P.INJECTIONS["ore_extract"]
        return inj.build_body(FakePatcher(), inj)
    finally:
        L.find_localplayer_anchor = real


def test_the_slot_address_matches_what_the_stub_emits():
    """`ore_slot` derives the address from stub_len rather than remembering it, so this
    pins the derivation to the layout `_ore_extract_body` actually emits."""
    from terrariabonker import patcher as P

    inj = P.INJECTIONS["ore_extract"]
    body = _ore_stub()

    stub_len = len(body) + 5
    derived = stub_len - 5 - len(inj.overwrite) - P.ORE_SLOT_BYTES
    # the emitted `cmp dword [esi+delta],1` must point at exactly that offset
    assert body[7:9] == b"\x83\x7e", "the guard is no longer a disp8 cmp on esi"
    delta = body[9]
    assert 6 + delta == derived, "the slot the stub reads is not where ore_slot says"
    assert body[derived:derived + P.ORE_SLOT_BYTES] == b"\x00" * P.ORE_SLOT_BYTES
    assert body[-len(inj.overwrite):] == inj.overwrite, "displaced prologue not reproduced"


def test_the_stub_holds_its_guard_across_the_nested_call():
    """PickTile re-enters this stub, so the guard has to stay closed for the whole nested
    call -- not merely until the queued tile has been read.

    Clearing a boolean flag on entry is *not* enough, and that is not a theoretical
    concern: the queueing loop polls that flag to decide when to hand over the next tile,
    so it re-arms within a poll interval (10ms) while the nested call is still running
    (dust, drops, sound and net take longer than that). The re-entered stub then finds the
    flag armed again and calls PickTile once more, recursing as deep as the vein is long
    until the game thread's stack gives out -- a hard crash with no managed exception.

    So the slot holds a state: the stub acts only on 1 (armed), moves it to 2 (in flight)
    *before* the call, and returns it to 0 only *after* the call has returned.
    """
    body = _ore_stub()
    arm = body.index(b"\x83\x7e")        # cmp dword [esi+delta],imm8
    assert body[arm + 3] == 1, "the stub acts on a state other than 1 (armed)"
    inflight = body.index(b"\xff\x46")   # inc dword [esi+delta]   (armed -> in flight)
    call = body.index(b"\xff\xd0")       # call eax
    idle = body.index(b"\x83\x66")       # and dword [esi+delta],0 (-> idle)
    assert arm < inflight < call < idle, \
        "the guard does not span the nested call — this recurses until the stack dies"


def test_the_stub_hands_pick_tile_a_mono_aligned_stack():
    """Mono's x86 JIT builds 16-byte-aligned frames assuming esp is 12 (mod 16) on entry
    (PickTile's own prologue: 4 pushes + `sub esp,0x7C` == 140 bytes, which only lands on
    16 from that start). pushad plus the five args do not preserve that, so the stub has
    to realign before the pushes."""
    body = _ore_stub()
    align = body.index(b"\x83\xe4\xf0")      # and esp,-16
    pad = body.index(b"\x83\xec")             # sub esp,imm8
    call = body.index(b"\xff\xd0")            # call eax
    assert align < pad < call, "the stack is not realigned before the call"
    # 5 args are pushed after the padding; entry must land back on 12 (mod 16)
    assert (-body[pad + 2] - 5 * 4 - 4) % 16 == 12, \
        "the padding does not leave PickTile the entry alignment mono assumes"


def test_pending_covers_the_tile_still_being_mined():
    """`ore_pending` must stay true through state 2, not just state 1. Reporting "done"
    when the stub dequeues the tile — rather than when the mining it triggers has
    finished — is what let the queueing loop re-arm mid-call."""
    from terrariabonker import patcher as P

    class FakeSlot:
        def __init__(self, state):
            self.state = state
            self.writes = []

        def read_i32(self, a):
            return self.state

        def write(self, a, b):
            self.writes.append((a, b))

    for state, pending in ((0, False), (1, True), (2, True)):
        p = P.Patcher.__new__(P.Patcher)
        p.mem = FakeSlot(state)
        p.ore_slot = lambda: 0x4000
        assert p.ore_pending() is pending, f"state {state} pending should be {pending}"
        # and a tile is only ever queued onto an idle slot
        assert p.ore_queue(7, 9) is (state == 0), \
            f"queueing onto state {state} should be {state == 0}"


def test_a_mined_out_tile_does_not_join_the_vein():
    """Terraria clears a tile's active bit when it is mined but leaves `type` alone, so a
    dug-out copper vein still reads as copper. Matching on the raw id hands the game
    coordinates for tiles that are not there — `CanKillTile` zeroes the damage so nothing
    breaks, but the vein looks bigger than it is and every one of those tiles costs a
    swing to discover. Only the three tiles still standing are part of the vein."""
    def fill(x, y):
        return 7 if x == 3 and 2 <= y <= 6 else 1

    # the top two of that copper run have already been mined out
    def active(x, y):
        return not (x == 3 and y in (2, 3))
    tm = T.TileMap(_world(fill, active), STATIC)

    assert tm.type_at(3, 2) == 7, "premise: the mined tile still reports its id"
    assert tm.active_at(3, 2) is False
    assert tm.solid_type_at(3, 2) is None

    assert T.flood(tm, 3, 4, {7}) == [(3, 4), (3, 5), (3, 6)] or \
        sorted(T.flood(tm, 3, 4, {7})) == [(3, 4), (3, 5), (3, 6)]
    assert T.flood(tm, 3, 2, {7}) == [], "a vein cannot start on a tile that is not there"


def test_the_active_offset_check_rejects_a_wrong_offset():
    """`check_active_offset` is what stops us trusting a field offset mono is free to move.
    Point it at the wrong field and it must say so rather than quietly reading garbage."""
    # a surface world: empty above, stone below
    tm = T.TileMap(_world(lambda x, y: 0 if y < 10 else 1), STATIC)
    assert tm.check_active_offset(5, 15)["ok"], "the real offset should validate"

    real = T._TILE_HEADER_OFF
    try:
        T._TILE_HEADER_OFF = 0x08          # point it at `type` instead
        got = tm.check_active_offset(5, 15)
        assert not got["ok"], "a wrong offset validated: %r" % (got,)
    finally:
        T._TILE_HEADER_OFF = real
