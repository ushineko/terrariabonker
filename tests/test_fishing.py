"""Fishing kit and bait top-up (spec 042), against a synthetic process image."""

import struct

import pytest

from terrariabonker.inventory import (ARR_DATA_OFF, ARR_LEN_OFF, INVENTORY_PTR_OFF,
                                      INVENTORY_SLOTS, ITEM_BAIT, ITEM_FISHING_POLE,
                                      ITEM_STACK, ITEM_TYPE, Inventory)

BASE = 0x50000000
LIFE = BASE + 0x6000
ARR = BASE + 0x100
ITEMS = BASE + 0x1000

GOLDEN_ROD, MASTER_BAIT, FIREFLY, DIRT = 2294, 2676, 1992, 2


@pytest.fixture
def qt_app():
    from PyQt6.QtWidgets import QApplication

    yield QApplication.instance() or QApplication([])


def _mem(items):
    """items: {slot: (type, stack, fishingPole, bait)}."""
    from conftest import FakeMem

    m = FakeMem(BASE, 0x20000)
    m.write(LIFE + INVENTORY_PTR_OFF, struct.pack("<I", ARR))
    m.poke_i32(ARR + ARR_LEN_OFF, INVENTORY_SLOTS)
    for i in range(INVENTORY_SLOTS):
        addr = ITEMS + i * 0x200
        m.write(ARR + ARR_DATA_OFF + i * 4, struct.pack("<I", addr))
        t, stack, pole, bait = items.get(i, (0, 0, 0, 0))
        m.poke_i32(addr + ITEM_TYPE, t)
        m.poke_i32(addr + ITEM_STACK, stack)
        m.write(addr + ITEM_FISHING_POLE, bytes([pole]))
        m.write(addr + ITEM_BAIT, bytes([bait]))
    return m


def _service(items):
    from terrariabonker import service as S

    m = _mem(items)
    inv = Inventory(m, LIFE)
    svc = S.Service.__new__(S.Service)
    svc.mem = m
    svc._live_inventory = lambda: inv
    svc._all_inventories = lambda: [inv]
    given = []

    def give(item_type, stack=1):
        slot = next(i for i in range(10, INVENTORY_SLOTS)
                    if m.read_i32(ITEMS + i * 0x200 + ITEM_TYPE) == 0)
        addr = ITEMS + slot * 0x200
        m.poke_i32(addr + ITEM_TYPE, item_type)
        m.poke_i32(addr + ITEM_STACK, stack)
        # the real give copies a template, which carries these
        m.write(addr + ITEM_FISHING_POLE, bytes([50 if item_type == GOLDEN_ROD else 0]))
        m.write(addr + ITEM_BAIT, bytes([50 if item_type == MASTER_BAIT else 0]))
        given.append((item_type, stack, slot))
        return slot

    svc.give_item = give
    return m, svc, given


# --- reading the gear ---------------------------------------------------------

def test_rods_and_baits_are_told_apart():
    m = _mem({0: (GOLDEN_ROD, 1, 50, 0), 1: (MASTER_BAIT, 12, 0, 50),
              2: (DIRT, 999, 0, 0)})
    gear = Inventory(m, LIFE).fishing_gear()
    assert gear["rods"] == [(0, 50)]
    assert gear["baits"] == [(1, 50, 12)]


def test_every_bait_stack_is_reported_not_only_the_first():
    """Topping up one stack while another runs dry is not "bait never runs out"."""
    m = _mem({3: (MASTER_BAIT, 4, 0, 50), 7: (FIREFLY, 2, 0, 20)})
    assert Inventory(m, LIFE).fishing_gear()["baits"] == [(3, 50, 4), (7, 20, 2)]


def test_an_empty_slot_is_not_mistaken_for_gear():
    """Every slot holds a real Item object, empty ones included, so a stale fishingPole
    byte in an emptied slot would otherwise read as a rod."""
    m = _mem({})
    m.write(ITEMS + 5 * 0x200 + ITEM_FISHING_POLE, bytes([50]))   # type stays 0
    assert Inventory(m, LIFE).fishing_gear()["rods"] == []


# --- the kit ------------------------------------------------------------------

def test_a_player_with_nothing_gets_a_rod_and_bait():
    _, svc, given = _service({})
    got = svc.fishing_kit()
    assert [g[0] for g in given] == [svc.KIT_ROD, svc.KIT_BAIT]
    assert got["gave"]["rod"]["type"] == svc.KIT_ROD
    assert got["rods"] and got["baits"]


def test_a_player_who_already_has_a_rod_is_given_only_bait():
    _, svc, given = _service({0: (2292, 1, 30, 0)})       # Fiberglass, power 30
    got = svc.fishing_kit()
    assert [g[0] for g in given] == [svc.KIT_BAIT]
    assert "rod" not in got["gave"]


def test_switching_the_kit_on_twice_does_not_hand_out_a_second_rod():
    """A cheat that gives a rod every time it is switched on fills the inventory."""
    _, svc, given = _service({})
    svc.fishing_kit()
    svc.fishing_kit()
    assert [g[0] for g in given] == [svc.KIT_ROD, svc.KIT_BAIT], given


def test_the_kit_leaves_a_players_own_gear_alone():
    """The rod someone chose is likelier to be the one they want."""
    m, svc, given = _service({0: (2292, 1, 30, 0), 1: (FIREFLY, 5, 0, 20)})
    svc.fishing_kit()
    assert given == []
    assert m.read_i32(ITEMS + 0 * 0x200 + ITEM_TYPE) == 2292
    assert m.read_i32(ITEMS + 1 * 0x200 + ITEM_STACK) == 5


# --- the bait pin -------------------------------------------------------------

def test_a_bait_stack_below_the_floor_is_topped_up():
    m, svc, _ = _service({4: (MASTER_BAIT, 3, 0, 50)})
    got = svc.bait_tick(keep=30)
    assert got["topped"] == [{"slot": 4, "power": 50, "was": 3, "now": 30}]
    assert m.read_i32(ITEMS + 4 * 0x200 + ITEM_STACK) == 30


def test_a_stack_at_or_above_the_floor_is_left_alone():
    """Not merely 'writes the same value'. A player carrying 999 bait should see the
    trainer do nothing at all to it."""
    m, svc, _ = _service({4: (MASTER_BAIT, 999, 0, 50), 5: (FIREFLY, 30, 0, 20)})
    got = svc.bait_tick(keep=30)
    assert got["topped"] == []
    assert m.read_i32(ITEMS + 4 * 0x200 + ITEM_STACK) == 999
    assert m.read_i32(ITEMS + 5 * 0x200 + ITEM_STACK) == 30


def test_every_low_stack_is_topped_up_in_one_round():
    m, svc, _ = _service({4: (MASTER_BAIT, 1, 0, 50), 9: (FIREFLY, 2, 0, 20)})
    got = svc.bait_tick(keep=25)
    assert [t["slot"] for t in got["topped"]] == [4, 9]
    assert m.read_i32(ITEMS + 4 * 0x200 + ITEM_STACK) == 25
    assert m.read_i32(ITEMS + 9 * 0x200 + ITEM_STACK) == 25


def test_carrying_no_bait_is_not_an_error():
    _, svc, _ = _service({0: (GOLDEN_ROD, 1, 50, 0)})
    got = svc.bait_tick(keep=30)
    assert got["topped"] == [] and got["baits"] == 0


def test_a_floor_below_one_is_refused():
    """keep=0 would 'top up' a stack to nothing, which deletes the player's bait."""
    from terrariabonker.service import ServiceError

    _, svc, _ = _service({4: (MASTER_BAIT, 5, 0, 50)})
    with pytest.raises(ServiceError):
        svc.bait_tick(keep=0)


def test_the_watch_loop_reports_what_it_refilled():
    _, svc, _ = _service({4: (MASTER_BAIT, 1, 0, 50)})
    got = svc.watch_bait(keep=10, interval=0.0, rounds=1)
    assert got == {"rounds": 1, "refills": 1}


# --- the CLI and GUI surfaces -------------------------------------------------

def test_the_fishing_command_exists_and_the_worker_may_run_it():
    from terrariabonker.cli import SERVE_OPS, build_parser

    args = build_parser().parse_args(["fishing"])
    assert args.func.__name__ == "cmd_fishing"
    assert (args.keep, args.no_kit, args.watch) == (30, False, False)
    assert "fishing" in SERVE_OPS, "the GUI worker cannot run a command it may not run"


def test_the_gui_asks_for_a_round_the_cli_can_parse():
    from terrariabonker.cli import build_parser
    from terrariabonker.gui import client

    argv = client.fishing_argv(50, kit=False)
    args = build_parser().parse_args(argv)          # the real argv, not a rebuilt one
    assert args.keep == 50 and args.no_kit and args.json
    assert not args.watch, "the worker must not block on a watch loop"
    assert build_parser().parse_args(client.fishing_argv()).no_kit is False


def test_the_checkbox_drives_the_bait_timer(qt_app, monkeypatch):
    """The checkbox is the whole interface to the cheat. Wired to nothing it looks exactly
    like a cheat whose bait quietly runs out."""
    from terrariabonker.gui import main_window as mw

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)

    w = mw.MainWindow()
    try:
        assert not w._fishing_timer.isActive()
        w.cb_fishing.setChecked(True)
        assert w._fishing_timer.isActive()
        assert not w._fishing_kit_done, "the kit must be asked for on the first round"
        w.cb_fishing.setChecked(False)
        assert not w._fishing_timer.isActive()
    finally:
        w.close()


def test_the_first_round_asks_for_the_kit_and_later_ones_do_not(qt_app, monkeypatch):
    """The kit is what makes the cheat usable to someone with no gear. A GUI that never
    asks for it leaves them with a checkbox that does nothing until they go shopping."""
    from terrariabonker.gui import main_window as mw

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)

    w = mw.MainWindow()
    try:
        sent = []

        def fake_request(argv, cb):
            sent.append(list(argv))
            cb('{"kit": {"gave": {}}, "bait": {"topped": []}}')
            return True

        w.helper.available = True
        monkeypatch.setattr(w.helper, "request", fake_request)

        w._tick_fishing()
        assert "--no-kit" not in sent[0], "the first round did not ask for the kit"
        w._tick_fishing()
        assert "--no-kit" in sent[1], "kept asking for the kit after it was handled"
    finally:
        w.close()


def test_a_refilled_stack_is_logged_once_not_on_every_bait(qt_app, monkeypatch):
    """Reported from the game: every bait consumed produced its own "29 -> 30" line, so
    a few minutes of fishing buried everything else in the panel."""
    from terrariabonker.gui import main_window as mw

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)

    w = mw.MainWindow()
    try:
        reply = ('{"bait": {"topped": [{"slot": 17, "power": 30, '
                 '"was": 29, "now": 30}]}}')

        def fake_request(argv, cb):
            cb(reply)
            return True

        w.helper.available = True
        monkeypatch.setattr(w.helper, "request", fake_request)

        for _ in range(5):
            w._tick_fishing()
        lines = [ln for ln in w.log.toPlainText().splitlines() if "[fishing]" in ln]
        assert len(lines) == 1, lines
        assert "slot 17" in lines[0]

        # a fresh session says it again: the player has been told nothing this time
        w._set_fishing_watch(False)
        w._set_fishing_watch(True)
        w._tick_fishing()
        assert len([ln for ln in w.log.toPlainText().splitlines()
                    if "[fishing]" in ln]) == 2
    finally:
        w.close()


# --- fishing power: raise it, and be able to put it back ----------------------

@pytest.fixture
def profile_tmp(tmp_path, monkeypatch):
    from terrariabonker import profile

    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    return profile


def test_every_rod_carried_is_raised_and_its_original_recorded(profile_tmp):
    m, svc, _ = _service({0: (2292, 1, 30, 0), 3: (GOLDEN_ROD, 1, 50, 0)})
    got = svc.set_fishing_power(255)
    assert [(c["slot"], c["was"], c["now"]) for c in got["changed"]] == [
        (0, 30, 255), (3, 50, 255)]
    assert m.read(ITEMS + 0 * 0x200 + ITEM_FISHING_POLE, 1)[0] == 255
    assert profile_tmp.rod_powers_to_restore() == {2292: 30, GOLDEN_ROD: 50}


def test_the_original_survives_a_second_switch_on(profile_tmp):
    _, svc, _ = _service({0: (2292, 1, 30, 0)})
    svc.set_fishing_power(255)
    svc.set_fishing_power(200)
    assert profile_tmp.rod_powers_to_restore() == {2292: 30}, "the original was lost"


def test_restoring_puts_the_rod_back_and_clears_the_debt(profile_tmp):
    m, svc, _ = _service({0: (2292, 1, 30, 0)})
    svc.set_fishing_power(255)
    got = svc.restore_fishing_power()
    assert got["restored"] == [{"slot": 0, "type": 2292, "power": 30}]
    assert m.read(ITEMS + 0 * 0x200 + ITEM_FISHING_POLE, 1)[0] == 30
    assert profile_tmp.rod_powers_to_restore() == {}


def test_a_rod_that_moved_slots_is_still_restored(profile_tmp):
    """Restores are by item type. Spec 038 exists because slot-keyed restores lose a rod
    the moment the player rearranges their inventory."""
    m, svc, _ = _service({0: (2292, 1, 30, 0)})
    svc.set_fishing_power(255)
    # the player moves the rod from slot 0 to slot 12
    for off, val in ((ITEM_TYPE, 0), (ITEM_STACK, 0)):
        m.poke_i32(ITEMS + 0 * 0x200 + off, val)
    m.write(ITEMS + 0 * 0x200 + ITEM_FISHING_POLE, bytes([0]))
    m.poke_i32(ITEMS + 12 * 0x200 + ITEM_TYPE, 2292)
    m.poke_i32(ITEMS + 12 * 0x200 + ITEM_STACK, 1)
    m.write(ITEMS + 12 * 0x200 + ITEM_FISHING_POLE, bytes([255]))

    got = svc.restore_fishing_power()
    assert got["restored"] == [{"slot": 12, "type": 2292, "power": 30}]
    assert m.read(ITEMS + 12 * 0x200 + ITEM_FISHING_POLE, 1)[0] == 30


def test_restoring_with_nothing_owed_is_not_an_error(profile_tmp):
    _, svc, _ = _service({0: (2292, 1, 30, 0)})
    assert svc.restore_fishing_power() == {"restored": []}


def test_a_rod_not_carried_at_restore_time_is_forgotten_not_kept(profile_tmp):
    """Keeping the debt would re-apply an old power to a rod picked up months later."""
    _, svc, _ = _service({0: (2292, 1, 30, 0)})
    svc.set_fishing_power(255)
    _, svc2, _ = _service({})                      # the rod is gone from the inventory
    svc2.restore_fishing_power()
    assert profile_tmp.rod_powers_to_restore() == {}


def test_power_beyond_the_byte_is_refused(profile_tmp):
    from terrariabonker.service import ServiceError

    _, svc, _ = _service({0: (2292, 1, 30, 0)})
    for bad in (0, 256, 1000):
        with pytest.raises(ServiceError):
            svc.set_fishing_power(bad)


def test_the_ceiling_is_the_bytes_own_limit():
    from terrariabonker import service as S

    assert S.Service.MAX_FISHING_POWER == 255, "a byte cannot hold more"


def test_the_original_is_recorded_before_the_rod_is_written(profile_tmp, monkeypatch):
    """Order matters for the one case the persistence exists for. Dying between the two
    steps must leave a spare record (harmless — it causes a restore that changes nothing)
    rather than a raised rod with no record, whose real power would be gone for good."""
    from terrariabonker import profile as prof

    m, svc, _ = _service({0: (2292, 1, 30, 0)})

    def boom(item_type, power):
        raise OSError("disk full")

    monkeypatch.setattr(prof, "remember_rod_power", boom)
    with pytest.raises(OSError):
        svc.set_fishing_power(255)
    assert m.read(ITEMS + 0 * 0x200 + ITEM_FISHING_POLE, 1)[0] == 30, \
        "the rod was raised before its original was safely recorded"


def test_switching_the_cheat_off_asks_for_the_rod_back(qt_app, monkeypatch):
    """Rod power is the only thing on this tab written into the save. Leaving it raised
    would make one cheat in a group of held effects quietly permanent."""
    from terrariabonker.gui import main_window as mw

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)

    w = mw.MainWindow()
    try:
        sent = []
        w.helper.available = True
        monkeypatch.setattr(w.helper, "request",
                            lambda argv, cb: (sent.append(list(argv)), True)[1])

        w.sp_power.setValue(200)
        w.cb_fishing.setChecked(True)
        assert ["--power", "200"] == sent[-1][-2:], sent
        w.cb_fishing.setChecked(False)
        assert "--restore" in sent[-1], sent
    finally:
        w.close()


def test_a_rod_left_raised_by_a_killed_trainer_is_put_back_on_the_next_start(
        qt_app, monkeypatch):
    """The reason the record is on disk rather than in memory."""
    from terrariabonker.gui import main_window as mw

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: True)
    monkeypatch.setattr(mw.MainWindow, "_call", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn", lambda self, *a, **k: None)
    monkeypatch.setattr(mw.MainWindow, "_spawn_user", lambda self, *a, **k: None)

    sent = []
    real = mw.MainWindow.refresh_patches

    def spy(self):
        self.helper.available = True
        monkeypatch.setattr(self.helper, "request",
                            lambda argv, cb: (sent.append(list(argv)), True)[1])
        return real(self)

    monkeypatch.setattr(mw.MainWindow, "refresh_patches", spy)
    w = mw.MainWindow()
    try:
        assert any("--restore" in a for a in sent), sent
    finally:
        w.close()
