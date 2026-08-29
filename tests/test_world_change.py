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


def _settle(w, status):
    """Report the same status until the panel accepts it as settled.

    A world load is the most turbulent moment in the game's lifetime, so nothing is
    written until the same world has been seen for a few consecutive polls.
    """
    from terrariabonker.gui.main_window import WORLD_SETTLE_POLLS
    for _ in range(WORLD_SETTLE_POLLS):
        w._note_world(status)
        w._maybe_restore(status)


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


def test_restore_fires_on_a_world_change_with_the_same_pid(gui_window):
    """The reported bug: the trigger keyed on the pid, and a world switch keeps it."""
    w = gui_window()
    done = []
    w._do_restore = lambda: done.append(1)
    import terrariabonker.gui.main_window as mw
    mw.profile.cheats = lambda: {"god": None}
    mw.profile.item_edits = lambda: {}

    _settle(w, {"pid": 42, "name": "P", "world": ["A", 4200, 1200]})
    assert len(done) == 1
    _settle(w, {"pid": 42, "name": "P", "world": ["A", 4200, 1200]})
    assert len(done) == 1, "same world, same pid: no repeat"
    _settle(w, {"pid": 42, "name": "P", "world": ["B", 4200, 1200]})
    assert len(done) == 2, "world changed with the pid unchanged"


def test_an_unreadable_world_does_not_restore_every_poll(gui_window):
    """world=None must not read as "a different world" or the profile is re-applied
    several times a second."""
    w = gui_window()
    done = []
    w._do_restore = lambda: done.append(1)
    import terrariabonker.gui.main_window as mw
    mw.profile.cheats = lambda: {"god": None}
    mw.profile.item_edits = lambda: {}

    for _ in range(5):
        st = {"pid": 42, "name": "P", "world": None}
        w._note_world(st)
        w._maybe_restore(st)
    assert len(done) == 1


def test_a_world_that_briefly_cannot_be_read_is_not_a_world_change(gui_window):
    """The load-screen case, and the one that separates `world is not None and ...` from a
    plain inequality: mid-load the name may not read, and treating that as a change would
    re-apply the profile on the way out and again on the way back.

    (An earlier version of the test above fed only None, so a mutant dropping the
    None-guard survived it -- both versions settle after one restore if the value never
    goes back.)"""
    w = gui_window()
    done = []
    w._do_restore = lambda: done.append(1)
    import terrariabonker.gui.main_window as mw
    mw.profile.cheats = lambda: {"god": None}
    mw.profile.item_edits = lambda: {}

    _settle(w, {"pid": 42, "name": "P", "world": ["A", 4200, 1200]})
    _settle(w, {"pid": 42, "name": "P", "world": None})                # mid-load
    _settle(w, {"pid": 42, "name": "P", "world": ["A", 4200, 1200]})
    assert len(done) == 1, "the same world, briefly unreadable, is not a new world"


def test_the_retry_budget_resets_on_a_world_change(gui_window):
    """A lazily-JIT'd cheat needs its retries in the new world too."""
    w = gui_window()
    w._do_restore = lambda: None
    import terrariabonker.gui.main_window as mw
    mw.profile.cheats = lambda: {"god": None}
    mw.profile.item_edits = lambda: {}

    _settle(w, {"pid": 42, "name": "P", "world": ["A", 4200, 1200]})
    w._restore_attempts = 7
    _settle(w, {"pid": 42, "name": "P", "world": ["B", 4200, 1200]})
    assert w._restore_attempts == 0


def test_nothing_is_written_until_the_world_has_settled(gui_window):
    """The reason this exists: a player becomes locatable partway through a world load,
    while the arrays the restore writes into are still being built."""
    from terrariabonker.gui.main_window import WORLD_SETTLE_POLLS
    w = gui_window()
    done = []
    w._do_restore = lambda: done.append(1)
    import terrariabonker.gui.main_window as mw
    mw.profile.cheats = lambda: {"god": None}
    mw.profile.item_edits = lambda: {}

    assert WORLD_SETTLE_POLLS >= 2, "one poll is no settle at all"
    st = {"pid": 42, "name": "P", "world": ["A", 4200, 1200]}
    w._note_world(st)
    w._maybe_restore(st)
    assert done == [], "the first sighting of a world must not write to it"
    for _ in range(WORLD_SETTLE_POLLS - 1):
        w._note_world(st)
        w._maybe_restore(st)
    assert len(done) == 1


def test_a_world_flickering_during_a_load_never_settles(gui_window):
    """While the game is still loading, the reported world can change poll to poll. None
    of those is stable, so none of them is written to."""
    w = gui_window()
    done = []
    w._do_restore = lambda: done.append(1)
    import terrariabonker.gui.main_window as mw
    mw.profile.cheats = lambda: {"god": None}
    mw.profile.item_edits = lambda: {}

    for world in (["A", 4200, 1200], None, ["B", 4200, 1200], None, ["A", 4200, 1200]):
        st = {"pid": 42, "name": "P", "world": world}
        w._note_world(st)
        w._maybe_restore(st)
    assert done == [], "nothing held still long enough to be written to"


def test_auto_sell_also_waits_for_the_world_to_settle(gui_window):
    """Auto-sell is the heaviest writer the panel has -- it copies template blocks into
    bank and inventory slots twice a second. Mid-load those are being rebuilt."""
    w = gui_window()
    sent = []
    w.helper.available = True
    w.helper.request = lambda argv, done: (sent.append(argv), True)[1]

    w._note_world({"world": ["A", 4200, 1200]})       # first sighting: not settled
    w._tick_sell()
    assert sent == [], "no sale into a world that has not settled"

    w._note_world({"world": ["A", 4200, 1200]})
    w._tick_sell()
    assert len(sent) == 1, "settled: the round runs"


# --- restore progress (measured: ~80s on a cold game) -----------------------
# Two things make a cold restore slow and only one is a fault: every pass re-resolves the
# anchors (14.3s on a fresh pid vs ~5s warm), and several cheats hook methods the game
# JIT-compiles only when the feature is first used. The panel used to go quiet after the
# first pass, so a legitimate wait was indistinguishable from a hang.

def test_progress_names_what_is_still_waiting():
    from terrariabonker.gui import client
    line = client.restore_progress(
        {"cheats": ["mining", "reach"], "pending": ["fast_place"], "items": []}, 2)
    assert "2 applied" in line and "1 waiting" in line
    assert "first time you use" in line, "say why, or it reads as stuck"


def test_progress_is_quiet_once_everything_is_applied():
    from terrariabonker.gui import client
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


def test_the_panel_reports_every_pass_not_only_the_first(gui_window):
    """The first pass always logged; later ones said nothing, so a cold restore went
    quiet for the ~80s it actually takes."""
    w = gui_window()
    rep = {"cheats": ["mining"], "pending": ["fast_place"], "items": [], "skipped": []}
    w._restore_attempts = 3            # a later pass, not the first
    w.log.clear()
    _run_restore(w, rep)
    assert "waiting on the game" in w.log.toPlainText()


def test_the_same_progress_line_is_not_repeated(gui_window):
    """It retries every 2s; repeating an unchanged line would bury the log.

    Only the PROGRESS line is suppressed. A second pass with the same leftovers is the
    existing "no progress, here is why" case and still reports -- that line is a
    conclusion, not a heartbeat.
    """
    w = gui_window()
    rep = {"cheats": ["mining"], "pending": ["fast_place"], "items": [], "skipped": []}
    w._restore_attempts = 3
    _run_restore(w, rep)
    w.log.clear()
    w._restore_attempts = 3            # same state, next pass
    _run_restore(w, rep)
    assert "waiting on the game" not in w.log.toPlainText()
