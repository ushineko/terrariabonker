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
    m.write(CODE + 0x100, ANCHORS["reset_block"].raw)
    m.write(CODE + 0x400, ANCHORS["place"].raw)
    p = Patcher(m)
    p._exec_regions = lambda: [(CODE, CODE + 0x1000)]   # only scan the planted region
    return m, p


def test_status_all_off_initially(game):
    _, p = game
    assert p.status() == {"mining": False, "reach": False, "fast_place": False,
                          "tool_reach": False, "pickup": False, "spawn_rate": False,
                          "loot": False}


def test_tool_reach_injection_roundtrip(game):
    m, p = game
    m.write(CODE + 0x700, ANCHORS["getranges"].raw)     # plant the method prologue
    inj = P.INJECTIONS["tool_reach"]
    inject = CODE + 0x700 + inj.inject_off
    m.write(inject, inj.overwrite)                        # original bytes at the site
    p.enable("tool_reach", value=40)
    assert m.read(inject, 1) == b"\xe9"                   # jmp to the cave installed
    assert p.is_enabled("tool_reach")
    rec = p._inj["tool_reach"]["sites"][0]
    # the cave holds the stub: mov [esi],40 ; mov [edi],40 ; <overwrite> ; jmp back
    assert m.read(rec["cave"], 2) == b"\xc7\x06"
    assert struct.unpack("<i", m.read(rec["cave"] + 2, 4))[0] == 40
    p.disable("tool_reach")
    assert m.read(inject, 5) == inj.overwrite            # site restored
    assert not p.is_enabled("tool_reach")


def test_pickup_injection_uses_imul_stub(game):
    m, p = game
    m.write(CODE + 0x900, ANCHORS["grabitems"].raw)      # plant the grab-range site
    inj = P.INJECTIONS["pickup"]
    inject = CODE + 0x900 + inj.inject_off
    m.write(inject, inj.overwrite)
    p.enable("pickup", value=50)
    assert m.read(inject, 1) == b"\xe9"                   # jmp installed
    rec = p._inj["pickup"]["sites"][0]
    # stub begins with imul eax,eax,50 (6B C0 32), then the 6 overwritten bytes
    assert m.read(rec["cave"], 3) == b"\x6b\xc0\x32"
    assert m.read(rec["cave"] + 3, 6) == inj.overwrite
    p.disable("pickup")
    assert m.read(inject, 6) == inj.overwrite
    assert not p.is_enabled("pickup")


def test_spawn_rate_injection_forces_outputs(game):
    m, p = game
    m.write(CODE + 0x900, ANCHORS["get_spawn_rate"].raw)   # plant the prologue
    inj = P.INJECTIONS["spawn_rate"]
    inject = CODE + 0x900 + inj.inject_off
    m.write(inject, inj.overwrite)
    p.enable("spawn_rate", value=40)
    assert m.read(inject, 1) == b"\xe9"
    rec = p._inj["spawn_rate"]["sites"][0]
    # stub: mov [esi],6 (spawnRate) ; mov [edi],40 (maxSpawns) ; then the overwrite
    assert m.read(rec["cave"], 2) == b"\xc7\x06"
    assert struct.unpack("<i", m.read(rec["cave"] + 2, 4))[0] == 6
    assert struct.unpack("<i", m.read(rec["cave"] + 8, 4))[0] == 40
    p.disable("spawn_rate")
    assert m.read(inject, 5) == inj.overwrite
    assert not p.is_enabled("spawn_rate")


def test_loot_injection_caps_denominator(game):
    m, p = game
    inj = P.INJECTIONS["loot"]
    m.write(CODE + 0x600, P.ANCHORS["trydrop"].raw)      # plant the prologue anchor
    inject = CODE + 0x600 + inj.inject_off
    m.write(inject, inj.overwrite)                        # original 7 bytes at the site
    p.enable("loot", value=100)                           # 100% -> cap denominator to 1
    assert m.read(inject, 1) == b"\xe9"                   # jmp to the cave installed
    assert p.is_enabled("loot")
    rec = p._inj["loot"]
    site = rec["sites"][0]
    # stub replaces the load in place (rerun_overwrite=False): it must NOT re-run the
    # displaced `mov ecx,[esi+10]; mov [esp+04],ecx` after the body.
    body = m.read(site["cave"], rec["stub_len"])
    assert body.startswith(b"\x8b\x4e\x10\x81\xf9")       # mov ecx,[esi+10]; cmp ecx,imm
    assert struct.unpack("<i", body[5:9])[0] == 1         # cap == 100//100 == 1
    assert body.endswith(b"\x89\x4c\x24\x04" + b"\xe9" + body[-4:])   # store, then jmp back
    assert inj.overwrite not in body[:-9]                 # displaced load NOT re-run wholesale
    # a 50% floor caps the denominator at 2
    p.disable("loot")
    p.enable("loot", value=50)
    body = m.read(p._inj["loot"]["sites"][0]["cave"], p._inj["loot"]["stub_len"])
    assert struct.unpack("<i", body[5:9])[0] == 2         # cap == 100//50 == 2
    p.disable("loot")
    assert m.read(inject, len(inj.overwrite)) == inj.overwrite   # site restored
    assert not p.is_enabled("loot")


def test_loot_multi_site_patches_every_twin(game):
    m, p = game
    inj = P.INJECTIONS["loot"]
    assert inj.multi
    # plant the anchor TWICE (CommonDrop and its structural twin)
    a, b = CODE + 0x200, CODE + 0x600
    m.write(a, P.ANCHORS["trydrop"].raw)
    m.write(b, P.ANCHORS["trydrop"].raw)
    inj_a, inj_b = a + inj.inject_off, b + inj.inject_off
    m.write(inj_a, inj.overwrite)
    m.write(inj_b, inj.overwrite)
    p.enable("loot", value=100)
    sites = p._inj["loot"]["sites"]
    assert len(sites) == 2                               # both twins patched
    assert {s["inject"] for s in sites} == {inj_a, inj_b}
    assert m.read(inj_a, 1) == b"\xe9" and m.read(inj_b, 1) == b"\xe9"
    p.disable("loot")
    assert m.read(inj_a, len(inj.overwrite)) == inj.overwrite   # both restored
    assert m.read(inj_b, len(inj.overwrite)) == inj.overwrite


def test_loot_reenable_is_idempotent_without_rescan(game):
    # Regression: the loot site sits inside its own anchor's reach, so a re-scan after
    # the jump is installed would find nothing ("anchor not found"). Re-enable / live
    # value change must reuse the recorded sites and only rewrite the stub.
    m, p = game
    inj = P.INJECTIONS["loot"]
    m.write(CODE + 0x600, P.ANCHORS["trydrop"].raw)
    inject = CODE + 0x600 + inj.inject_off
    m.write(inject, inj.overwrite)
    p.enable("loot", value=100)
    sites_before = [s["inject"] for s in p._inj["loot"]["sites"]]
    # obliterate the anchor: a re-resolve would now raise. Idempotent enable must not care.
    m.write(CODE + 0x600, b"\x90" * len(P.ANCHORS["trydrop"].raw))
    p.enable("loot", value=50)                            # would raise if it re-scanned
    assert [s["inject"] for s in p._inj["loot"]["sites"]] == sites_before
    body = m.read(p._inj["loot"]["sites"][0]["cave"], p._inj["loot"]["stub_len"])
    assert struct.unpack("<i", body[5:9])[0] == 2         # stub rewritten to cap=2


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


def test_values_default_before_any_apply(game):
    _, p = game
    # every valued cheat reports its ValueSpec default until one is applied
    assert p.values() == {"mining": 0.2, "reach": 20, "tool_reach": 30,
                          "pickup": 50, "spawn_rate": 15, "loot": 100}
    assert "fast_place" not in p.values()   # carries no value


def test_applied_value_is_recorded_and_restored(game):
    m, p = game
    m.write(CODE + 0x700, ANCHORS["getranges"].raw)
    inj = P.INJECTIONS["tool_reach"]
    m.write(CODE + 0x700 + inj.inject_off, inj.overwrite)
    p.enable("tool_reach", value=77)        # injection value
    p.enable("reach", value=42)             # value-cheat override
    assert p.values()["tool_reach"] == 77
    assert p.values()["reach"] == 42
    # a fresh instance (same pid) restores the recorded values from the state file
    p2 = Patcher(m)
    assert p2.values()["tool_reach"] == 77
    assert p2.values()["reach"] == 42


def test_value_survives_disable(game):
    m, p = game
    p.enable("reach", value=42)
    p.disable("reach")                      # toggled off, but the value is remembered
    assert p.values()["reach"] == 42


def test_resolve_raises_when_anchor_missing(tmp_path, monkeypatch):
    from conftest import FakeMem
    monkeypatch.setattr(P, "_STATE", str(tmp_path / "s.json"))
    m = FakeMem(BASE, 0x2000)               # no anchors planted
    p = Patcher(m)
    p._exec_regions = lambda: [(BASE, BASE + 0x2000)]
    with pytest.raises(PatchError):
        p.enable("fast_place")
