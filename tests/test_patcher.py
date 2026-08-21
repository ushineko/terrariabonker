"""Patcher: AOB resolve + code patch + value, against a synthetic image."""

import struct

import pytest

from terrariabonker import patcher as P
from terrariabonker.patcher import ANCHORS, CHEATS, PatchError, Patcher

BASE = 0x40000000
CODE = BASE + 0x2000        # where we plant the anchors (an "executable" region)
LIFE = BASE + 0x5000        # player statLife
NAME_AT = BASE + 0x40


@pytest.fixture
def game(tmp_path, monkeypatch):
    from conftest import FakeMem
    monkeypatch.setattr(P, "_STATE", str(tmp_path / "patches.json"))
    m = FakeMem(BASE, 0x8000)
    # a player so value writes (mining/reach) have somewhere to go
    m.plant_mono_string(NAME_AT, "hero")
    m.plant_player(LIFE, [100, 100, 80, 20, 20, 20], NAME_AT)
    # plant the anchors at known spots in the "code" region
    m.write(CODE + 0x100, ANCHORS["reset_block"])
    m.write(CODE + 0x400, ANCHORS["place"])
    p = Patcher(m)
    p._exec_regions = lambda: [(CODE, CODE + 0x1000)]   # only scan the planted region
    return m, p


def test_status_all_off_initially(game):
    _, p = game
    assert p.status() == {"mining": False, "reach": False, "fast_place": False}


def test_enable_disable_fast_place(game):
    m, p = game
    site = CODE + 0x400 + CHEATS["fast_place"].patch_off
    p.enable("fast_place")
    assert m.read(site, 10) == CHEATS["fast_place"].patched
    assert p.is_enabled("fast_place")
    p.disable("fast_place")
    assert m.read(site, 10) == CHEATS["fast_place"].orig
    assert not p.is_enabled("fast_place")


def test_mining_patches_code_and_sets_value(game):
    m, p = game
    site = CODE + 0x100 + CHEATS["mining"].patch_off
    p.enable("mining")
    assert m.read(site, 6) == CHEATS["mining"].patched
    assert struct.unpack("<f", m.read(LIFE + 0x1A0, 4))[0] == pytest.approx(0.2)
    p.disable("mining")
    assert m.read(site, 6) == CHEATS["mining"].orig
    assert struct.unpack("<f", m.read(LIFE + 0x1A0, 4))[0] == pytest.approx(1.0)


def test_shared_anchor_reach_and_mining_independent(game):
    m, p = game
    p.enable("reach")                       # patches blockRange @ +0
    p.enable("mining")                      # patches pickSpeed @ +12, same anchor
    assert p.is_enabled("reach") and p.is_enabled("mining")
    assert struct.unpack("<i", m.read(LIFE + 0x2C0, 4))[0] == 20
    p.disable("reach")
    assert not p.is_enabled("reach") and p.is_enabled("mining")   # mining untouched


def test_state_persists_across_instances(game, tmp_path):
    m, p = game
    p.enable("fast_place")
    p2 = Patcher(m)                         # new instance, same pid -> reads state file
    assert p2.is_enabled("fast_place")


def test_resolve_raises_when_anchor_missing(tmp_path, monkeypatch):
    from conftest import FakeMem
    monkeypatch.setattr(P, "_STATE", str(tmp_path / "s.json"))
    m = FakeMem(BASE, 0x2000)               # no anchors planted
    p = Patcher(m)
    p._exec_regions = lambda: [(BASE, BASE + 0x2000)]
    with pytest.raises(PatchError):
        p.enable("fast_place")
