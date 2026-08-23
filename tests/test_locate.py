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
    assert not valid_block([100, 120, 80, 20, 20, 20])     # max2 != max
    assert not valid_block([100, 100, 80, 20, 36, 36])     # mana not multiple of 20
    assert not valid_block([100, 100, 80, 20, 20, 40])     # manamax2 != manamax
    assert not valid_block([100, 100, 140, 20, 20, 20])    # life > max
    assert not valid_block([600, 600, 300, 20, 20, 20])    # life max out of range


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
