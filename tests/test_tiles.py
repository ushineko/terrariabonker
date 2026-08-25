"""Reading the world's tiles and finding a vein (spec 040).

The layout is not guessed at — it is what Player.PickTile does, read out of its own JIT'd
code. The trap it hides is that there are *two* sizes: the tile buffer is allocated at the
largest supported world size and reused, so its height is the indexing stride while
Main.maxTilesX/maxTilesY is the world's real extent. Reading those the other way round
walks a search past the world edge into tiles left over from the previous world.
"""

import inspect
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
                         (T.WORLD_FORMED, "world-formed"), (T.GEMS, "gem")):
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


def test_obsidian_is_swept_by_default_but_is_not_called_an_ore():
    """It has no ore tile of its own — it is made where water meets lava — so it was
    missed by a list built from what ores exist. It is still what a vein miner is for."""
    names = _tile_names()
    tid = next(i for i, n in names.items() if n == "Obsidian")
    assert tid in T.whitelist(gems=False)
    assert tid not in T.ORES and tid not in T.GEMS


def test_silt_and_slush_are_swept_by_default_and_gems_are_not():
    assert T.whitelist(gems=False) >= {123, 224}
    assert not (T.whitelist(gems=False) & set(T.GEMS))
    assert T.whitelist(gems=True) > T.whitelist(gems=False)


def test_a_vein_report_names_the_tile_even_when_it_is_not_an_ore():
    """The report names a tile by looking it up in each group in turn, so a tile in a
    group nobody remembered to add reads back with a blank name rather than an error."""
    from terrariabonker import service as S

    obsidian = next(i for i, n in _tile_names().items() if n == "Obsidian")

    class FakeTiles:
        max_x, max_y = 400, 400

        def type_at(self, x, y):
            return obsidian if (x, y) == (5, 5) else None

        solid_type_at = type_at

    svc = S.Service.__new__(S.Service)
    svc.tilemap = lambda: FakeTiles()
    got = svc.vein_at(5, 5)
    assert got["name"] == "Obsidian"
    assert got["whitelisted"] and got["count"] == 1


# --- the stub -----------------------------------------------------------------

ARENA = 0x68000000


def _ore_stub():
    """Build the extractor stub against stand-ins for the addresses it bakes in."""
    from terrariabonker import patcher as P
    import terrariabonker.locate as L

    class FakePatcher:
        _inj = {}

        def _resolve(self, key):
            return 0x2178CD48

        def arena(self, *a, **k):
            return ARENA

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


def _mem_writes_via(code: bytes, rm_reg: int) -> list[int]:
    """Offsets of instructions writing through [reg] or [reg+disp8].

    Not a general disassembler -- a tripwire on one class of mistake. mod=00 and mod=01
    with r/m == rm_reg are [reg] and [reg+disp8]; the reg field carries the opcode
    extension for the group opcodes.
    """
    hits, i = [], 0
    while i < len(code) - 2:
        op, modrm = code[i], code[i + 1]
        mod, reg, rm = modrm >> 6, (modrm >> 3) & 7, modrm & 7
        if rm == rm_reg and mod in (0, 1):
            if op in (0xC7, 0x89, 0x88, 0x01, 0x29, 0x31, 0x21, 0x09, 0x11, 0x19):
                hits.append(i)
            elif op == 0xFF and reg in (0, 1):          # inc/dec dword [reg]
                hits.append(i)
            elif op == 0x83 and reg != 7:               # arithmetic; reg==7 is cmp
                hits.append(i)
        i += 1
    return hits


def test_the_stub_never_writes_through_its_queue_pointer():
    """esi walks the queue, and the stub must only ever read through it.

    The arena is RWX so a write would no longer *fault* -- the earlier crash was a stub
    writing into a read-execute cave in CUESDK_2015.dll. It would do something worse:
    silently corrupt the coordinate list, and mining the wrong tile cannot be undone.
    """
    body = _ore_stub()
    assert _mem_writes_via(body, 6) == [], "the stub writes through esi — it would " \
        "corrupt its own queue"
    assert body.count(b"\xff\x76\x04") == 1, "the y read is gone"
    assert body.count(b"\xff\x36") == 1, "the x read is gone"


def test_a_corrupt_count_cannot_mine_more_than_the_batch():
    """The count lives in the game's memory, so it is not trustworthy input. A bad count
    would not crash -- it would walk off the end of the queue and mine whatever integers
    followed, which damages a world irreversibly. The stub clamps as well as the caller."""
    from terrariabonker import patcher as P

    body = _ore_stub()
    cmp_at = body.index(b"\x83\xff")                    # cmp edi,imm8
    assert body[cmp_at + 2] == P.ORE_MAX_BATCH, "clamped against the wrong bound"
    assert body[cmp_at + 3:cmp_at + 5] == b"\x76\x05", "no jbe past the clamp"
    assert body[cmp_at + 5:cmp_at + 6] == b"\xbf", "the clamp does not set edi"
    assert struct.unpack_from("<I", body, cmp_at + 6)[0] == P.ORE_MAX_BATCH


def test_the_batch_loop_walks_one_pair_per_tile():
    """Each iteration consumes one (x, y) pair and one count. Advancing by anything but 8
    would read coordinates straddling two tiles."""
    body = _ore_stub()
    call = body.index(b"\xff\xd0")                      # call eax
    assert body[call + 2:call + 5] == b"\x83\xc6\x08", "esi does not advance 8 per tile"
    assert body[call + 5] == 0x4F, "edi (the counter) is not decremented"
    assert body[call + 6] == 0x75, "the loop does not branch back"
    back = body[call + 7]
    assert back > 0x80, "the loop branch goes forward, not back"


def test_the_hook_runs_every_frame_not_only_when_the_player_swings():
    """The bug this guards is a sequencing one, and it made the feature look broken.

    Hooked at PickTile, the stub only ran when the player swung -- but the queue is armed
    *after* a swing has broken a tile, so a vein sat armed until they happened to swing
    again. Breaking one block and stopping did nothing at all. The hook belongs on
    Player.Update's per-frame call to GrabItems, so an armed queue drains on the next
    frame.

    The displaced bytes matter too: the call itself cannot be displaced, because its rel32
    differs every session. The five bytes before it carry no relative address.

    They are wildcarded in the anchor rather than spelled out. Spelling them out reads as
    the safer choice and is the opposite: the anchor then stops matching as soon as the
    jump is written over them, so nothing can find the site again. What those bytes
    actually are is checked against ``overwrite`` at write time by ``_check_site``, which
    reads the site instead of trusting a pattern.
    """
    from terrariabonker import patcher as P

    inj = P.INJECTIONS["ore_extract"]
    assert inj.anchor == "grabitems_call", "the extractor is hooked per-swing again"
    assert inj.overwrite == b"\x89\x04\x24\x8b\xc0", "displaced bytes changed"
    assert not any(P.ANCHORS[inj.anchor].pattern.mask[-5:]), \
        "the displaced bytes must be wildcarded — see _check_site for what guards them"
    # and PickTile is now only a call target, never a hook site
    assert "pick_tile" in P.ANCHORS
    src = inspect.getsource(P._ore_extract_body)
    assert '_resolve("pick_tile")' in src, "the call target is no longer resolved directly"
    assert "SENTINEL" not in src, \
        "a re-entrancy sentinel is dead weight once PickTile is not the hook site"


def test_the_call_passes_the_caps_the_game_passes():
    """PickTile(this, x, y, pickPower, cap). A cap other than -1 is not ignored: it
    becomes `damage = Min(damage, cap * damage/pickPower)`, so a small one silently
    scales mining damage down and tiles simply never break -- no crash, no error, the
    feature just quietly does nothing. -1 is the sentinel meaning "no cap", and it is
    what both of the game's own call sites pass."""
    import struct
    from terrariabonker import patcher as P

    body = _ore_stub()
    call = body.index(b"\xff\xd0")                      # call eax
    # args are pushed right-to-left just before it: cap, pickPower, y, x, this
    pushes = body[:call]
    at = pushes.rindex(b"\x68")                         # push imm32 — the pick power
    assert struct.unpack("<I", body[at + 1:at + 5])[0] == P.ORE_PICK_POWER, \
        "the pick power the stub pushes is not the one the constant says"
    assert body[at - 2:at] == b"\x6a\xff", \
        "cap is not -1 — any other value scales mining damage and nothing breaks"


def test_the_pick_power_survives_the_reduced_rate_some_tiles_are_credited_at():
    """Reported from the game: hellstone poofed on every hit and stayed put, still
    needing one swing at a time, while copper and obsidian went in one call. A tile
    breaks on accumulated damage and damage scales with pick power, so the fix is
    headroom -- enough that a tile credited at half rate still reaches a break."""
    from terrariabonker import patcher as P

    assert P.ORE_PICK_POWER // 2 >= 100, \
        "a tile credited at half rate does not reach the damage a break takes"
    assert P.ORE_PICK_POWER >= 210, \
        "below the stiffest pick requirement in the game (lihzahrd brick)"


def test_the_count_is_consumed_before_the_batch_is_mined():
    """The stub now runs 60 times a second, not once per swing. A count left standing
    would re-mine the same batch every frame -- 32 PickTile calls per frame forever, on
    tiles that are already gone. Reading it and zeroing it immediately makes a batch
    happen once."""
    body = _ore_stub()
    read = body.index(b"\x8b\x3d")                      # mov edi,[count]
    clear = body.index(b"\x83\x25")                     # and dword [count],0
    call = body.index(b"\xff\xd0")                      # call eax
    assert read < clear < call, "the count is not consumed before the work"
    assert struct.unpack_from("<I", body, read + 2)[0] == \
        struct.unpack_from("<I", body, clear + 2)[0], \
        "the count read and the count cleared are different addresses"
    assert body[clear + 6] == 0, "the count is not cleared to zero"


def test_every_call_in_the_batch_gets_a_mono_aligned_stack():
    """Mono builds 16-byte frames assuming esp is 12 (mod 16) on entry (PickTile's own
    prologue: 4 pushes + `sub esp,0x7C` = 140 bytes). One alignment before the loop is not
    enough -- PickTile may clean up its own arguments, so esp is restored from an aligned
    base at the top of every iteration."""
    body = _ore_stub()
    align = body.index(b"\x83\xe4\xf0")                 # and esp,-16
    pad = body.index(b"\x83\xec")                        # sub esp,imm8
    base = body.index(b"\x8b\xec")                       # mov ebp,esp  (aligned base)
    loop = body.index(b"\x8b\xe5")                       # mov esp,ebp  (each iteration)
    assert align < pad < base < loop, "the loop does not restore an aligned esp"
    assert (-body[pad + 2] - 5 * 4 - 4) % 16 == 12, \
        "the padding does not leave PickTile the entry alignment mono assumes"


def test_the_queue_lives_in_the_arena_not_in_a_cave():
    """The whole point of the arena: the queue is at a known address in memory we own, so
    it needs no derivation from stub length and cannot collide with borrowed padding."""
    from terrariabonker import patcher as P

    body = _ore_stub()
    q = ARENA + P.ORE_QUEUE_OFF
    assert struct.pack("<I", q) in body, "the stub does not read the arena queue"
    assert struct.pack("<I", q + 4) in body, "the stub does not point esi at the pairs"
    assert P.ORE_QUEUE_OFF >= len(body), "the queue overlaps the stub"
    assert P.ORE_QUEUE_OFF + P.ORE_QUEUE_BYTES <= P.Patcher.ARENA_SIZE, \
        "the queue runs past the end of the arena"
    assert P.INJECTIONS["ore_extract"].arena is True


def test_arming_writes_the_pairs_before_the_count():
    """A count covering half-written coordinates makes the stub mine garbage, and mining
    the wrong tile cannot be undone. The count goes last, and never exceeds the batch."""
    from terrariabonker import patcher as P

    class FakeMem:
        def __init__(self, count=0):
            self.count, self.writes = count, []

        def read_i32(self, a):
            return self.count

        def write(self, a, b):
            self.writes.append((a, b))

    p = P.Patcher.__new__(P.Patcher)
    p.mem = FakeMem()
    p._arena = ARENA
    q = ARENA + P.ORE_QUEUE_OFF
    assert p.ore_queue() == q

    assert p.ore_arm([(7, 9), (8, 9)]) == 2
    assert [a for a, _ in p.mem.writes] == [q + 4, q], \
        "the count must be written after the pairs, never before"
    assert p.mem.writes[0][1] == struct.pack("<iiii", 7, 9, 8, 9)
    assert p.mem.writes[1][1] == struct.pack("<i", 2)

    p.mem = FakeMem()
    over = [(i, i) for i in range(P.ORE_MAX_BATCH + 10)]
    assert p.ore_arm(over) == P.ORE_MAX_BATCH, "the caller-side cap is not applied"

    p.mem = FakeMem(count=3)
    assert p.ore_armed() is True
    p.ore_disarm()
    assert p.mem.writes == [(q, struct.pack("<i", 0))]


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


def test_arena_state_round_trips_without_breaking_the_patcher(tmp_path, monkeypatch):
    """The arena is not an injection record and must not be stored as one.

    Putting it in `_inj` made `_norm_inj` throw KeyError('inject') on the next load --
    and that is not a cosmetic bug: every Patcher construction raises, so the CLI and the
    GUI both stop working until the state file is hand-repaired.
    """
    from terrariabonker import patcher as P

    state = tmp_path / "patches.json"
    monkeypatch.setattr(P, "_STATE", str(state))

    class mem:
        pid = 4242

    p = P.Patcher.__new__(P.Patcher)
    p.mem = mem()
    p._sites, p._enabled, p._inj, p._values = {}, set(), {}, {}
    p._arena = 0x68000000
    p._inj["ore_extract"] = {"sites": [{"inject": 1, "cave": 2}], "stub_len": 3}
    p._save_state()

    again = P.Patcher(mem())            # must not raise
    assert again._arena == 0x68000000, "the arena did not survive a state round-trip"
    assert "__arena__" not in again._inj, "the arena is masquerading as an injection"
    assert again._inj["ore_extract"]["sites"][0]["inject"] == 1


def test_the_watch_window_covers_how_far_the_player_can_actually_mine():
    """The bug this guards: the watcher looked 45 tiles around the player while the
    tool-reach cheat let them break tiles 75 away. Mining at range then broke a tile the
    watcher was not looking at, so nothing triggered and the feature silently stopped
    working the moment the player moved and mined further out. The window has to follow
    the live reach, not a number that looks reasonable."""
    from terrariabonker.service import _VeinWatch

    class FakeTiles:
        max_x, max_y = 4200, 1200

        def solid_type_at(self, x, y):
            return None

    def watcher_for(reach):
        class FakePatcher:
            def is_enabled(self, n):
                return True

            def values(self):
                return {"tool_reach": reach} if reach is not None else {}

        svc = type("S", (), {})()
        svc.patcher = lambda: FakePatcher()
        svc.tilemap = lambda: FakeTiles()
        return _VeinWatch(svc)

    for reach in (30, 75, 120):
        w = watcher_for(reach)
        assert w.radius > reach, \
            f"a reach of {reach} needs a window wider than {reach}, got {w.radius}"
    # and with the reach cheat off there is still a sane default
    assert watcher_for(None).radius >= 75


def test_the_watcher_rechecks_known_ore_instead_of_rescanning_everything():
    """Detection latency is the whole feature: rescan the window every round (32k tiles,
    ~0.29s) and a fast miner breaks several blocks before the first is even noticed, so
    the vein goes "after a few" instead of on the first tile. Re-checking only the tiles
    already known to be ore is ~1200 reads, ~0.012s.

    This counts the reads per round rather than trusting the source, because the mistake
    is easy to reintroduce: a rescan inside the loop looks harmless and costs 18x.
    """
    from terrariabonker.service import Service

    W, H, ORE = 200, 200, 7
    calls = {"n": 0}

    class FakeTiles:
        max_x, max_y = W, H

        def solid_type_at(self, x, y):
            calls["n"] += 1
            # a small vein far from anything, so nothing ever triggers
            return ORE if (x, y) in {(50, 50), (50, 51)} else None

    class FakePatcher:
        def is_enabled(self, n):
            return True

        def values(self):
            return {"tool_reach": 75}

        def ore_disarm(self):
            return True

    svc = Service.__new__(Service)
    svc.patcher = lambda: FakePatcher()
    svc.tilemap = lambda: FakeTiles()
    svc.player_tile = lambda: (100, 100)

    import terrariabonker.tiles as T
    real = T.whitelist
    T.whitelist = lambda gems=False: {ORE}
    try:
        svc.watch_veins(rounds=1)
        first = calls["n"]
        calls["n"] = 0
        svc.watch_veins(rounds=6)
        six = calls["n"]
    finally:
        T.whitelist = real

    window = (2 * 90) ** 2
    assert first >= window * 0.9, "the first round should scan the window once"
    # rounds 2-6 must not rescan: they only re-check the 2 tracked ore tiles
    per_round = (six - window) / 5.0
    assert per_round < 100, \
        f"each round costs {per_round:.0f} reads — the window is being rescanned"


def test_tilemap_does_not_rescan_for_the_static_base_every_call():
    """`main_static_base` is a full memory scan (~1.5s); building the TileMap from it is
    six reads. The vein watcher calls tilemap() on every trigger, so paying the scan each
    time put a 1.5s delay in front of mining that itself takes 20ms — the feature looked
    broken when only this was slow. The statics do not move while the process lives."""
    from terrariabonker import service as S

    calls = {"n": 0}

    def fake_base(mem):
        calls["n"] += 1
        return 0x10000000

    class FakeTileMap:
        def __init__(self, mem, base):
            pass

    import terrariabonker.locate as L
    import terrariabonker.tiles as TT
    real_base, real_tm = L.main_static_base, TT.TileMap
    L.main_static_base, TT.TileMap = fake_base, FakeTileMap
    try:
        svc = S.Service.__new__(S.Service)
        svc.mem = object()
        svc._main_base = None
        for _ in range(5):
            svc.tilemap()
        assert calls["n"] == 1, f"the static base was rescanned {calls['n']} times"
        svc.invalidate()
        svc.tilemap()
        assert calls["n"] == 2, "invalidate() must force a rescan"
    finally:
        L.main_static_base, TT.TileMap = real_base, real_tm


def test_a_falling_deposit_is_followed_down_instead_of_lost():
    """Silt, slush and sand drop when the tile under them is mined. A deposit bigger than
    one batch has therefore *moved* by the time the second batch is armed, and mining the
    coordinates from the first flood hits empty air while the blocks sit lower down.

    This models that: a 40-tile silt column where every mined tile makes the survivors
    fall one row. Re-flooding between batches must finish it; trusting the first flood
    leaves most of it standing.
    """
    from terrariabonker import service as S
    from terrariabonker.patcher import ORE_MAX_BATCH

    SILT = 123
    live = {(50, y) for y in range(100, 140)}          # 40 tiles, one column

    class FakeTiles:
        max_x, max_y = 200, 400

        def solid_type_at(self, x, y):
            return SILT if (x, y) in live else None

    class FakePatcher:
        def is_enabled(self, n):
            return True

        def ore_arm(self, batch):
            batch = list(batch)[:ORE_MAX_BATCH]
            for q in batch:
                live.discard(q)
            # everything still standing falls one row per tile removed below it
            fallen = set()
            for (x, y) in sorted(live, key=lambda q: -q[1]):
                ny = y
                while ny + 1 < 400 and (x, ny + 1) not in live and (x, ny + 1) not in fallen:
                    ny += 1
                fallen.add((x, ny))
            live.clear()
            live.update(fallen)
            return len(batch)

        def ore_disarm(self):
            return True

    svc = S.Service.__new__(S.Service)
    svc.patcher = lambda: FakePatcher()
    svc.tilemap = lambda: FakeTiles()

    import terrariabonker.tiles as T
    real = T.whitelist
    T.whitelist = lambda gems=False: {SILT}
    try:
        got = svc.extract_vein(50, 139, timeout=0.5)
    finally:
        T.whitelist = real

    assert not live, f"{len(live)} tiles left standing — the deposit was lost as it fell"
    assert got["batches"] >= 2, "premise: a 40-tile deposit needs more than one batch"
    assert got["reason"] == "", got["reason"]


def _extract_world(vein, far, ground=(), ore=7, steal=0):
    """A Service over a synthetic world for extract_vein.

    `steal` models the player mining some of the vein themselves, which is what leaves
    the extractor short of its budget and sends it looking for more.
    """
    from terrariabonker import service as S
    from terrariabonker.patcher import ORE_MAX_BATCH

    STONE = 1
    live = set(vein) | set(far)
    state = {"first": True}

    class FakeTiles:
        max_x, max_y = 400, 400

        def solid_type_at(self, x, y):
            if (x, y) in live:
                return ore
            return STONE if (x, y) in set(ground) else None

    class FakePatcher:
        def is_enabled(self, n):
            return True

        def ore_arm(self, batch):
            batch = list(batch)[:ORE_MAX_BATCH]
            for q in batch:
                live.discard(q)
            if state["first"] and steal:
                state["first"] = False
                for q in sorted(live & set(vein))[:steal]:
                    live.discard(q)           # the player got these
            return len(batch)

        def ore_disarm(self):
            return True

    svc = S.Service.__new__(S.Service)
    svc.patcher = lambda: FakePatcher()
    svc.tilemap = lambda: FakeTiles()
    return svc, live


def test_a_vein_does_not_dig_through_the_ground_beneath_it():
    """Reported from the game: with the reach cheat on, mining a vein also took unrelated
    patches of the same ore far below — "ore suddenly flying at me from below, long after
    I stopped mining", each pass finding another deposit under the last.

    The cause was the search that follows a falling silt pile: it ran down the column to
    the world floor. A falling pile rests *on* the ground it lands on, so the search must
    stop at the first solid tile that is not ours — anything under that is someone else's
    deposit.
    """
    ORE = 7
    vein = {(50, 100 + i) for i in range(40)}
    ground = {(50, 200)}                      # what a falling pile would rest on
    far = {(50, 300), (51, 300), (52, 300)}   # a separate deposit, below the ground
    # the player takes 5 themselves, so the extractor is short of its budget and looks on
    svc, live = _extract_world(vein, far, ground, ore=ORE, steal=5)

    real = T.whitelist
    T.whitelist = lambda gems=False: {ORE}
    try:
        svc.extract_vein(50, 100, timeout=0.5)
    finally:
        T.whitelist = real

    assert not (live & vein), "the vein the player broke was not finished"
    assert live == far, f"it dug through the ground: took {far - live}"


def test_a_vein_never_yields_more_tiles_than_it_held():
    """Backstop for when there is no ground in the way — a pile can fall through a gap
    and land next to a different deposit. A vein cannot grow, so whatever the re-scan
    turns up, never take more tiles than the vein held to begin with."""
    ORE = 7
    vein = {(50, 100), (50, 101), (50, 102)}
    far = {(50, 150), (50, 151), (50, 152)}   # same column, nothing solid between
    svc, live = _extract_world(vein, far, ore=ORE)

    real = T.whitelist
    T.whitelist = lambda gems=False: {ORE}
    try:
        got = svc.extract_vein(50, 100, timeout=0.5)
    finally:
        T.whitelist = real

    assert not (live & vein), "the vein was not finished"
    assert live == far, f"it kept going into a second deposit: took {far - live}"
    assert got["mined"] == len(vein), got


def test_the_arena_is_warmed_while_the_game_runs_not_when_a_cheat_is_toggled():
    """Every stub lives in the arena, and allocating it needs the game running frames.
    But enabling a cheat from the panel means clicking the panel, which unfocuses Terraria,
    which pauses it — so if allocation waited for the toggle it would fail exactly when it
    is needed. It is done from the status poll instead, while the game is still focused.

    Must never raise and never block on a paused game: the next poll tries again.
    """
    from terrariabonker import service as S

    calls = {"arena": 0}

    class FakePatcher:
        _arena = None

        def _arena_ok(self, base):
            return False

        def arena(self, *a, **k):
            calls["arena"] += 1
            return 0x68000000

    svc = S.Service.__new__(S.Service)
    svc.patcher = lambda: FakePatcher()

    svc.frames_advancing = lambda *a, **k: False
    assert svc.ensure_arena() is False, "tried to allocate against a paused game"
    assert calls["arena"] == 0, "blocked on a game that runs no frames"

    svc.frames_advancing = lambda *a, **k: True
    assert svc.ensure_arena() is True
    assert calls["arena"] == 1

    # an allocation that fails is not an error the caller has to handle
    class Exploding(FakePatcher):
        def arena(self, *a, **k):
            raise RuntimeError("boom")

    svc.patcher = lambda: Exploding()
    assert svc.ensure_arena() is False

    # already allocated: no work at all
    class Warm(FakePatcher):
        _arena = 0x68000000

        def _arena_ok(self, base):
            return True

        def arena(self, *a, **k):
            raise AssertionError("re-allocated an arena that already exists")

    svc.patcher = lambda: Warm()
    assert svc.ensure_arena() is True
