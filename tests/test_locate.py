"""Locator logic against a synthetic process image - no game, no root."""

from terrariabonker.locate import (find_players, pick_live, read_mono_string,
                                    valid_block)

BASE = 0x10000000
LIFE = BASE + 0x1000        # statLife lives here; name ptr sits 0x6C0 before it
NAME_AT = BASE + 0x200      # a mono String well inside the buffer


def _mem(block=(100, 100, 80, 20, 20, 20), name="terrariabonker"):
    from conftest import FakeMem
    m = FakeMem(BASE, 0x8000)
    m.plant_mono_string(NAME_AT, name)
    m.plant_player(LIFE, list(block), NAME_AT)
    return m


def test_valid_block_accepts_real_player():
    assert valid_block([100, 100, 80, 20, 20, 20])
    assert valid_block([400, 400, 400, 200, 200, 200])


def test_valid_block_rejects_near_misses():
    assert not valid_block([100, 120, 80, 20, 20, 20])     # boosted cap BELOW the base
    assert not valid_block([100, 100, 80, 20, 36, 36])     # mana max not a multiple of 20
    assert not valid_block([100, 100, 80, 20, 20, 10])     # boosted mana cap below base
    assert not valid_block([100, 100, 140, 20, 20, 20])    # life above the boosted cap
    assert not valid_block([600, 600, 300, 20, 20, 20])    # life max out of range
    assert not valid_block([100, 100, 80, 25, 20, 20])     # mana above the boosted cap


def test_a_player_wearing_mana_gear_is_still_a_player():
    """The bug this guards cost a session, and silently.

    `statManaMax2` is the cap AFTER equipment and buffs, so a mana accessory makes it
    exceed `statManaMax` and lets the current value exceed the permanent cap. The
    validator demanded equality, so the live player failed it, the scan fell back to an
    inert load-time snapshot, and every write the trainer made landed on a copy the game
    ignores -- cheats did nothing, with no error anywhere.

    These are the real numbers from the game it was found in: 400 life, 200 mana with a
    +20 accessory, currently full at the boosted cap.
    """
    assert valid_block([400, 400, 400, 220, 200, 220])


def test_life_boosting_gear_is_allowed_too():
    """Same shape on the life side: `statLifeMax2` includes equipment, so it may exceed
    the permanent cap, and current life may exceed the permanent cap with it."""
    assert valid_block([520, 500, 520, 200, 200, 200])
    assert valid_block([400, 400, 400, 200, 200, 200])     # and the unboosted case


def test_read_mono_string_roundtrip():
    m = _mem()
    assert read_mono_string(m, NAME_AT) == "terrariabonker"


def test_find_players_finds_the_plant():
    m = _mem()
    players = find_players(m)
    assert len(players) == 1
    p = players[0]
    assert p.life_addr == LIFE
    assert p.name == "terrariabonker"
    assert p.stat_life == 80 and p.stat_life_max == 100


def test_find_players_rejects_block_without_name_pointer():
    from conftest import FakeMem
    m = FakeMem(BASE, 0x8000)
    m.plant_player(LIFE, [100, 100, 80, 20, 20, 20], name_ptr=BASE + 0x40)  # junk ptr
    assert find_players(m) == []


def test_pick_live_single_copy():
    m = _mem()
    players = find_players(m)
    assert pick_live(m, players) is players[0]


def test_pick_live_prefers_below_max_when_static():
    """Two copies, one at full HP one below: the below-max one is the live guess."""
    from conftest import FakeMem
    m = FakeMem(BASE, 0x9000)
    m.plant_mono_string(NAME_AT, "hero")
    m.plant_player(LIFE, [100, 100, 100, 20, 20, 20], NAME_AT)      # snapshot, full
    other = BASE + 0x2000
    m.plant_player(other, [100, 100, 63, 20, 20, 20], NAME_AT)      # live, below max
    players = find_players(m)
    assert len(players) == 2
    live = pick_live(m, players, samples=2, dt=0.0)
    assert live is not None and live.life_addr == other


def test_local_player_at_returns_none_when_the_anchor_stops_reading():
    """A cached anchor that goes bad must read as None so the caller re-finds it.

    Reads return None on failure; feeding that straight into the next read raised an
    uncaught TypeError, which broke every served request instead of relocalizing
    (codex review, P2).
    """
    from conftest import FakeMem
    from terrariabonker.locate import local_player_at
    m = FakeMem(0x40000000, 0x1000)                 # anchor points outside the image
    assert local_player_at(m, 0x40000000 + 0x900) is None


def test_local_player_at_returns_none_on_a_null_player_array():
    from conftest import FakeMem
    from terrariabonker.locate import local_player_at
    m = FakeMem(0x40000000, 0x2000)
    anchor = 0x40000000 + 0x100
    m.poke_i32(anchor - 0xA, 0x40000000 + 0x800)    # statics resolve...
    m.poke_i32(anchor - 4, 0x40000000 + 0x804)
    m.poke_i32(0x40000000 + 0x800, 0)               # ...but Main.player is null
    m.poke_i32(0x40000000 + 0x804, 0)
    assert local_player_at(m, anchor) is None
