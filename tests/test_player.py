"""Player field read/write and freeze-tick logic against a synthetic image."""

from terrariabonker.player import Player
from terrariabonker.trainer import Freezer

BASE = 0x20000000
LIFE = BASE + 0x1000


def _mem(block=(100, 100, 80, 20, 15, 20)):
    from conftest import FakeMem
    m = FakeMem(BASE, 0x4000)
    m.plant_player(LIFE, list(block), name_ptr=0)
    return m


def test_reads_match_planted_block():
    p = Player(_mem(), LIFE)
    assert (p.stat_life_max2, p.stat_life_max, p.stat_life) == (100, 100, 80)
    assert (p.stat_mana, p.stat_mana_max, p.stat_mana_max2) == (20, 15, 20)


def test_set_life_and_heal_full():
    m = _mem()
    p = Player(m, LIFE)
    assert p.set_life(55) and p.stat_life == 55
    assert p.heal_full() and p.stat_life == 100


def test_set_max_life_writes_cap_and_permanent():
    m = _mem()
    p = Player(m, LIFE)
    assert p.set_max_life(400)
    assert p.stat_life_max == 400 and p.stat_life_max2 == 400


def test_mana_full():
    m = _mem(block=(100, 100, 100, 5, 20, 20))
    p = Player(m, LIFE)
    assert p.mana_full() and p.stat_mana == 20


def test_freezer_tick_restores_life_to_max():
    m = _mem(block=(100, 100, 100, 20, 20, 20))
    fr = Freezer(m, godmode=True)
    fr.players = [Player(m, LIFE)]
    Player(m, LIFE).set_life(30)              # simulate a hit
    assert fr._tick() is True
    assert Player(m, LIFE).stat_life == 100   # restored
    assert fr.saves == 1


def test_freezer_tick_reports_stale_when_reads_fail():
    m = _mem()
    fr = Freezer(m, godmode=True)
    fr.players = [Player(m, BASE + 0x999999)]  # address outside the buffer
    assert fr._tick() is False
