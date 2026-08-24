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


def _world(fill=None, active=None, w=None, h=None, stride=None):
    """A tiny world with its own oversized buffer, laid out exactly like the game's.

    ``active`` decides which tiles carry the active bit; by default any non-zero id does,
    which is how a real world looks. Pass one to model tiles that have been mined out --
    the game keeps their id and only clears the bit.
    """
    w = WORLD_W if w is None else w
    h = WORLD_H if h is None else h
    stride = STRIDE if stride is None else stride
    m = FakeMem(BASE, 0x2000000)
    m.write(STATIC + T.MAIN_TILE_OFF, struct.pack("<I", BUF))
    m.poke_i32(STATIC + T.MAIN_MAX_TILES_OFF, w)
    m.poke_i32(STATIC + T.MAIN_MAX_TILES_OFF + 4, h)
    m.write(BUF + T._BOUNDS_OFF, struct.pack("<I", BOUNDS))
    for off, v in ((0x00, 64), (0x04, 0), (0x08, stride), (0x0C, 0)):
        m.poke_i32(BOUNDS + off, v)
    slot = 0
    for x in range(w):
        for y in range(h):
            obj = OBJS + slot * 0x18
            slot += 1
            t = 0 if fill is None else fill(x, y)
            on = bool(t) if active is None else active(x, y)
            m.write(obj + T._TILE_TYPE_OFF, struct.pack("<H", t))
            m.write(obj + T._TILE_HEADER_OFF,
                    struct.pack("<H", T._ACTIVE_BIT if on else 0))
            m.write(TILES + 4 * (stride * x + y), struct.pack("<I", obj))
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


# --- the stub -----------------------------------------------------------------

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


def _writes_to_esi(code: bytes) -> list[int]:
    """Offsets of instructions in `code` that WRITE through esi+disp.

    Only the forms this stub could plausibly emit are decoded; the point is not a general
    disassembler but a tripwire on the one mistake that matters. mod=01 rm=110 is
    [esi+disp8], so modrm & 0xC7 == 0x46 with the reg field carrying the opcode extension.
    """
    hits = []
    i = 0
    while i < len(code) - 2:
        op, modrm = code[i], code[i + 1]
        esi_disp8 = (modrm & 0xC7) == 0x46
        reg = (modrm >> 3) & 7
        if esi_disp8:
            if op in (0xC7, 0x89, 0x88, 0x01, 0x29, 0x31, 0x21, 0x09, 0x11, 0x19):
                hits.append(i)                       # mov/add/sub/xor/and/or to memory
            elif op == 0xFF and reg in (0, 1):
                hits.append(i)                       # inc/dec dword [esi+d]
            elif op == 0x83 and reg != 7:
                hits.append(i)                       # arithmetic; reg==7 is cmp (a read)
        i += 1
    return hits


def test_the_stub_never_writes_to_its_own_cave():
    """The crash this guards, in full: a code cave is borrowed padding inside somebody
    else's mapping, and those are read-execute — this one lands in a code section of
    CUESDK_2015.dll, the Corsair SDK shipped with the game. Installing a stub works
    regardless because /proc/pid/mem ignores page protection; the CPU running it does not.

    An earlier version kept an "in flight" state in its slot and died on the first swing:

        page fault on write access to 0x7795424b in wow64 32-bit code (0x77954216)

    0x77954216 was `inc dword [esi+0x3c]`, 0x7795424b was the slot. Reads are fine.
    """
    body = _ore_stub()
    assert _writes_to_esi(body) == [], \
        "the stub writes into its own cave — it will fault on a read-execute page"
    # and the reads it does make are still there
    assert b"\x83\x7e" in body, "the armed check is gone"
    assert body.count(b"\xff\x76") == 2, "the x/y reads are gone"


def test_an_injection_that_writes_its_cave_must_ask_for_a_writable_one():
    """`writes_cave` is the escape hatch for a stub that genuinely needs to write: it makes
    _find_cave demand a writable page rather than letting the mismatch surface as an
    access violation mid-game. The extractor does not need it — its guard lives on the
    stack — but the plumbing has to actually filter, or the flag is decoration."""
    from unittest.mock import mock_open, patch
    from terrariabonker import patcher as P

    maps = ("77950000-77980000 r-xp 00000000 00:00 0    /game/CUESDK_2015.dll\n"
            "0418a000-0418c000 rwxp 00000000 00:00 0 \n"
            "0b000000-0b010000 rw-p 00000000 00:00 0 \n")

    pat = P.Patcher.__new__(P.Patcher)

    class mem:
        pid = 1234
    pat.mem = mem()

    with patch("builtins.open", mock_open(read_data=maps)):
        any_exec = pat._exec_regions()
        writable = pat._exec_regions(writable=True)

    assert (0x77950000, 0x77980000) in any_exec, "the r-x cave region should be offered"
    assert (0x77950000, 0x77980000) not in writable, \
        "a read-execute region was offered to a stub that writes to its cave"
    assert writable == [(0x0418A000, 0x0418C000)], "only the rwx region is writable"
    assert P.INJECTIONS["ore_extract"].writes_cave is False


def test_the_guard_is_the_sentinel_the_stub_itself_passes():
    """PickTile re-enters this stub, and the guard has to hold for the whole nested call.
    A flag would mean a write, which the cave cannot take — so the nested call marks itself
    by passing ORE_SENTINEL as PickTile's `cap`, and the stub compares the incoming `cap`
    on the stack before doing anything. Depth is one by construction.

    The two values must be the same one: pass a different sentinel than you check for and
    the stub recurses into itself on every swing."""
    from terrariabonker import patcher as P

    body = _ore_stub()
    sent = struct.pack("<I", P.ORE_SENTINEL)
    guard = body.index(b"\x81\x7c\x24\x34")        # cmp dword [esp+0x34],imm32
    assert body[guard + 4:guard + 8] == sent, "the guard checks a different value"
    pushed = body.index(b"\x68" + sent)               # push imm32  (cap)
    call = body.index(b"\xff\xd0")
    assert guard < pushed < call, "the sentinel is not passed to the call it guards"
    # the guard must come before the armed check, so our own call is the cheapest exit
    assert guard < body.index(b"\x83\x7e"), "the re-entry check is not first"


def test_the_sentinel_is_large_and_positive():
    """PickTile treats any cap other than -1 as `damage = Min(damage, cap*damage/pickPower)`.
    A large positive cap leaves damage untouched, exactly as -1 does. A negative one would
    clamp the damage negative and no tile would ever break."""
    from terrariabonker import patcher as P

    assert 0 < P.ORE_SENTINEL < 2 ** 31, "a negative cap would clamp mining damage"
    assert P.ORE_SENTINEL != -1 & 0xFFFFFFFF
    damage, power = 30, 100
    assert min(damage, int(P.ORE_SENTINEL * (damage / power))) == damage


def test_the_slot_address_matches_what_the_stub_emits():
    """`ore_slot` derives the address from stub_len rather than remembering it, so this
    pins the derivation to the layout `_ore_extract_body` actually emits."""
    from terrariabonker import patcher as P

    inj = P.INJECTIONS["ore_extract"]
    body = _ore_stub()
    stub_len = len(body) + 5
    derived = stub_len - 5 - len(inj.overwrite) - P.ORE_SLOT_BYTES
    armed = body.index(b"\x83\x7e")                  # cmp dword [esi+delta],0
    delta = body[armed + 2]
    assert 6 + delta == derived, "the slot the stub reads is not where ore_slot says"
    assert body[derived:derived + P.ORE_SLOT_BYTES] == b"\x00" * P.ORE_SLOT_BYTES
    assert body[-len(inj.overwrite):] == inj.overwrite, "displaced prologue not reproduced"


def test_the_stub_hands_pick_tile_a_mono_aligned_stack():
    """Mono's x86 JIT builds 16-byte-aligned frames assuming esp is 12 (mod 16) on entry
    (PickTile's own prologue: 4 pushes + `sub esp,0x7C` == 140 bytes, which only lands on
    16 from that start). pushad plus the five args do not preserve that."""
    body = _ore_stub()
    align = body.index(b"\x83\xe4\xf0")              # and esp,-16
    pad = body.index(b"\x83\xec")                     # sub esp,imm8
    call = body.index(b"\xff\xd0")
    assert align < pad < call, "the stack is not realigned before the call"
    assert (-body[pad + 2] - 5 * 4 - 4) % 16 == 12, \
        "the padding does not leave PickTile the entry alignment mono assumes"


def test_arming_writes_the_coordinate_before_the_flag():
    """Only the unprivileged side writes the slot, so a half-written coordinate is a real
    hazard: the game could read x from the new tile and y from the old one. The flag goes
    last, and disarming is what stops an armed tile being re-mined on every swing."""
    from terrariabonker import patcher as P

    class FakeSlot:
        def __init__(self, state=0):
            self.state = state
            self.writes = []

        def read_i32(self, a):
            return self.state

        def write(self, a, b):
            self.writes.append((a, b))

    p = P.Patcher.__new__(P.Patcher)
    p.mem = FakeSlot()
    p.ore_slot = lambda: 0x4000
    assert p.ore_arm(7, 9) is True
    assert [a for a, _ in p.mem.writes] == [0x4004, 0x4000], \
        "the flag must be written after the coordinate, never before"
    assert p.mem.writes[0][1] == struct.pack("<ii", 7, 9)

    p.mem = FakeSlot(state=1)
    assert p.ore_armed() is True
    p.ore_disarm()
    assert p.mem.writes == [(0x4000, struct.pack("<i", 0))]


def test_a_dirt_block_is_not_air():
    """Dirt is TileID 0 and so is empty space, so `type_at` cannot tell them apart. The
    active bit is the only thing that can, which is the whole reason `solid_type_at`
    exists — `KillTile` goes through `Tile.ClearEverything` and zeroes `type` and
    `sTileHeader` together, so a *mined* tile already reads back as id 0 either way."""
    # a solid floor of dirt (id 0, active) under open air (id 0, inactive)
    tm = T.TileMap(_world(lambda x, y: 0, lambda x, y: y >= 10), STATIC)

    assert tm.type_at(5, 5) == 0 and tm.type_at(5, 15) == 0, "premise: both read as id 0"
    assert tm.active_at(5, 5) is False and tm.active_at(5, 15) is True
    assert tm.solid_type_at(5, 5) is None, "air is not a tile"
    assert tm.solid_type_at(5, 15) == 0, "a dirt block is"


def _surface_world():
    """Tall enough for the validator's two bands: open sky, then rock."""
    return T.TileMap(_world(lambda x, y: 0 if y < 250 else 1,
                            lambda x, y: y >= 250,
                            w=90, h=400, stride=420), STATIC)


def test_the_active_offset_check_accepts_the_real_layout():
    got = _surface_world().check_active_offset(45, 200)
    assert got["ok"], got
    assert got["sky_active_pct"] == 0.0 and got["deep_active_pct"] == 100.0


def test_the_active_offset_check_rejects_padding():
    """The failure this guards is silent: mono leaves two bytes of padding where adding up
    the field widths says sTileHeader should be, so a wrong offset reads a constant zero
    and reports every tile as empty — and a fixture written to match the same wrong guess
    agrees with it. The check has to be run against the live game to mean anything."""
    tm = _surface_world()
    real = T._TILE_HEADER_OFF
    try:
        T._TILE_HEADER_OFF = 0x0C          # where the field widths say it is: padding
        got = tm.check_active_offset(45, 200)
        assert not got["ok"], "a constant-zero offset validated: %r" % (got,)
        assert got["deep_active_pct"] == 0.0
    finally:
        T._TILE_HEADER_OFF = real
