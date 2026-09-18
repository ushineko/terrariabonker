"""Noticing that the world changed (spec 049).

Two bugs, one missing fact. The trainer had no idea which world was loaded, so auto-restore
never fired on a world switch (it keyed on the pid, which does not change), and auto-sell's
piggy-bank cache could carry an answer from a world the player had left.

The offset below is a **literal on purpose**. It was found by diffing Main's static block
across a real world switch: of 139 changed dwords it was the one whose value matched the
world files on both sides ('The Lousy Yeet' -> The_Lousy_Yeet.wld, 'Royal Brewery of
Maggots' -> Royal_Brewery_of_Maggots.wld.bak).
"""

import struct

from conftest import FakeMem
from terrariabonker import layout
from terrariabonker import tiles as T


def test_the_offset_is_the_measured_one():
    assert layout.MAIN_WORLD_NAME_OFF == 0x660


BASE = 0x10000000
STATIC = BASE + 0x100
BUF = BASE + 0x8000
BOUNDS = BASE + 0x200
NAME = BASE + 0x3000


def _svc(name="The Lousy Yeet", w=4200, h=1200, name_ptr=None):
    """A Service over a synthetic world, with Main's statics where locate would find them."""
    from terrariabonker.service import Service

    m = FakeMem(BASE, 0x40000)
    m.write(STATIC + T.MAIN_TILE_OFF, struct.pack("<I", BUF))
    m.poke_i32(STATIC + T.MAIN_MAX_TILES_OFF, w)
    m.poke_i32(STATIC + T.MAIN_MAX_TILES_OFF + 4, h)
    m.write(BUF + T._BOUNDS_OFF, struct.pack("<I", BOUNDS))
    for off, v in ((0x00, 64), (0x04, 0), (0x08, h + 1), (0x0C, 0)):
        m.poke_i32(BOUNDS + off, v)
    if name is not None:
        m.plant_mono_string(NAME, name)
    m.write(STATIC + layout.MAIN_WORLD_NAME_OFF,
            struct.pack("<I", NAME if name_ptr is None else name_ptr))
    svc = Service(m)
    svc._main_base = STATIC
    return svc, m


def test_world_id_reads_the_loaded_world():
    svc, _ = _svc("The Lousy Yeet")
    assert svc.world_id() == ("The Lousy Yeet", 4200, 1200)


def test_two_worlds_of_the_same_size_are_told_apart():
    """The measured case: the tile buffer and the dimensions were byte-identical across a
    real switch between two 4200x1200 worlds, so only the name separates them."""
    a, _ = _svc("The Lousy Yeet")
    b, _ = _svc("Royal Brewery of Maggots")
    assert a.world_id() != b.world_id()
    assert a.world_id()[1:] == b.world_id()[1:], "premise: same dimensions"


def test_world_id_is_none_when_the_name_cannot_be_read():
    """A rotted offset must return None, not a plausible string: callers fall back to
    their old behaviour rather than acting on a wrong answer."""
    svc, _ = _svc(name_ptr=0)
    assert svc.world_id() is None


def test_world_id_is_none_with_no_world_loaded():
    from terrariabonker.service import ServiceError
    svc, _ = _svc("The Lousy Yeet")

    def boom():
        raise ServiceError("no world")
    svc.tilemap = boom
    assert svc.world_id() is None


def test_the_static_base_is_scanned_once_not_per_call():
    """main_static_base is a full memory scan (~1.5s) and world_id runs on every status
    poll. Paying for a scan each time would be the entire frame budget."""
    from terrariabonker import locate

    svc, _ = _svc("The Lousy Yeet")
    svc._main_base = None
    calls = []
    real = locate.main_static_base
    locate.main_static_base = lambda mem: (calls.append(1), STATIC)[1]
    try:
        for _ in range(5):
            svc.world_id()
    finally:
        locate.main_static_base = real
    assert len(calls) == 1




# --- the two callers --------------------------------------------------------

def test_auto_sell_rescans_when_the_world_changes_but_the_size_does_not():
    """The bug shipped in v0.41.0: the cache key was (tile buffer, max_x, max_y), which a
    measurement showed is byte-identical across a switch between two 4200x1200 worlds."""
    svc, m = _svc("The Lousy Yeet")

    class Tiles:
        buf, max_x, max_y = BUF, 4200, 1200
        scans = 0

        def find_type(self, want, limit=0):
            Tiles.scans += 1
            return [(1, 1)]

    svc.tilemap = lambda: Tiles()
    svc._live_inventory = lambda: type("I", (), {"slots": lambda self: []})()
    svc.live_block = lambda: type("B", (), {"life_addr": 0})()

    svc.bank_reachable()
    svc.bank_reachable()
    assert Tiles.scans == 1, "same world: cached"

    m.plant_mono_string(NAME, "Royal Brewery of Maggots")   # same size, different world
    svc.bank_reachable()
    assert Tiles.scans == 2, "different world: rescanned"
















# --- restore progress (measured: ~80s on a cold game) -----------------------
# Two things make a cold restore slow and only one is a fault: every pass re-resolves the
# anchors (14.3s on a fresh pid vs ~5s warm), and several cheats hook methods the game
# JIT-compiles only when the feature is first used. The panel used to go quiet after the
# first pass, so a legitimate wait was indistinguishable from a hang.

def test_progress_names_what_is_still_waiting():
    from terrariabonker import argv as client
    line = client.restore_progress(
        {"cheats": ["mining", "reach"], "pending": ["fast_place"], "items": []}, 2)
    assert "2 applied" in line and "1 waiting" in line
    assert "first time you use" in line, "say why, or it reads as stuck"


def test_progress_is_quiet_once_everything_is_applied():
    from terrariabonker import argv as client
    line = client.restore_progress({"cheats": ["mining"], "pending": [], "items": []}, 3)
    assert "1 cheats applied" in line
    assert client.restore_progress({"cheats": [], "pending": [], "items": []}, 1) is None
    assert client.restore_progress(None, 1) is None


def _run_restore(w, rep):
    """Drive the real _do_restore round-trip: capture its callback and feed it a report."""
    import json
    captured = {}
    w._call = lambda argv, on_output=None: captured.setdefault("cb", on_output)
    w._do_restore()
    captured["cb"](json.dumps(rep))




