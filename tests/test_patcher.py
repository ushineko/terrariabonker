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
                          "loot": False, "teleport": False, "max_minions": False}


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


def test_teleport_managed_call_stub(game):
    m, p = game
    inj = P.INJECTIONS["teleport"]
    assert inj.make_body is None and inj.call_anchor == "player_teleport"
    # plant the inject anchor (TriggerPing tail) and the call-target anchor (Teleport)
    tp = CODE + 0x300
    tele = CODE + 0xA00
    m.write(tp, P.ANCHORS["trigger_ping"].raw)
    m.write(tele, P.ANCHORS["player_teleport"].raw)
    inject = tp + inj.inject_off                          # inject_off == 0
    m.write(inject, inj.overwrite)                        # first 7 bytes at the site
    p.enable("teleport")
    assert m.read(inject, 1) == b"\xe9"                   # jmp to the cave installed
    assert p.is_enabled("teleport")
    rec = p._inj["teleport"]
    site = rec["sites"][0]
    assert len(rec["sites"]) == 1                         # single-site
    body = m.read(site["cave"], rec["stub_len"])
    # pushad ; mov ebx,esp ; mov eax,[ebp+08]
    assert body[:6] == b"\x60\x8b\xdc\x8b\x45\x08"
    # tile->pixel ×16 on both coords: add eax,0x02000000 and add ecx,0x02000000
    assert b"\x05" + struct.pack("<i", P._F32_TIMES16) in body        # add eax,imm32
    assert b"\x81\xc1" + struct.pack("<i", P._F32_TIMES16) in body    # add ecx,imm32
    assert b"\x8b\x4d\x0c" in body                                     # mov ecx,[ebp+0C]
    # push player_base (this) = life_addr - 0x738
    player_base = LIFE - P.STATLIFE_FROM_OBJ
    assert b"\x68" + struct.pack("<I", player_base) in body
    # mov eax, Teleport entry (= call-target anchor match - 0x32) ; call eax
    call_target = tele - inj.call_target_off
    idx = body.index(b"\xb8" + struct.pack("<I", call_target))
    assert body[idx + 5: idx + 7] == b"\xff\xd0"          # call eax
    # tail: restore esp via ebx, popad, reproduce the two displaced instructions, jmp back
    assert body.endswith(b"\x8b\xe3\x61\x8b\x4d\x08\x89\x4c\x24\x04"
                         + b"\xe9" + body[-4:])
    p.disable("teleport")
    assert m.read(inject, len(inj.overwrite)) == inj.overwrite   # site restored
    assert not p.is_enabled("teleport")


def test_teleport_stub_is_stack_convention_agnostic():
    # The stub must NOT hand-balance the arg pushes with `add esp,N` (mono emits `ret N`
    # for some methods); it saves esp in ebx before the pushes and restores it after the
    # call, so it is correct whether the callee cleans the stack or not.
    body = P._teleport_body(0x11223344, 0x55667788)
    assert body[1:3] == b"\x8b\xdc"                       # mov ebx,esp (save restore point)
    assert b"\x8b\xe3" in body                            # mov esp,ebx (restore, either conv)
    assert b"\x83\xc4" not in body                        # no `add esp,imm8` cleanup guess
    # tile->pixel ×16 (exponent += 4) applied to both coords before the call
    assert b"\x05" + struct.pack("<i", P._F32_TIMES16) in body
    assert b"\x81\xc1" + struct.pack("<i", P._F32_TIMES16) in body
    # exactly five dword args pushed for Teleport(this, X, Y, Style, extraInfo)
    assert body.count(b"\x6a\x00") == 2                   # push 0 (Style, extraInfo)
    assert b"\x68\x44\x33\x22\x11" in body               # push this (little-endian)


def test_max_minions_tunable_immediate_patch(game):
    import struct as _s
    m, p = game
    m.write(CODE + 0x800, P.ANCHORS["reset_minions"].raw)   # plant the reset instruction
    inj = CODE + 0x800 + CHEATS["max_minions"].patch_off     # the immediate at +6
    m.write(inj, CHEATS["max_minions"].orig)                 # original value 1
    assert not p.is_enabled("max_minions")
    p.enable("max_minions", value=12)
    assert _s.unpack("<i", m.read(inj, 4))[0] == 12          # immediate rewritten to the cap
    assert p.is_enabled("max_minions")                       # ground truth: != original
    p.enable("max_minions", value=25)                        # live re-tune
    assert _s.unpack("<i", m.read(inj, 4))[0] == 25
    p.disable("max_minions")
    assert m.read(inj, 4) == CHEATS["max_minions"].orig      # restored to 1
    assert not p.is_enabled("max_minions")


def test_anchor_resolves_whether_or_not_patched(game):
    # Regression: the place/reset_block anchors wildcard the bytes the cheat overwrites,
    # so a cold-cache re-resolve still finds the site once the cheat is applied (the old
    # anchors included the patched bytes and raised "anchor not found" after a restart +
    # a state-file race that dropped the cached site).
    m, p = game
    for anchor, cheat_name in (("place", "fast_place"), ("reset_block", "reach"),
                               ("reset_block", "mining")):
        cheat = CHEATS[cheat_name]
        base = p._resolve(anchor)
        site = base + cheat.patch_off
        # pristine: original bytes at the site -> resolves
        m.write(site, cheat.orig)
        p._sites.pop(anchor, None)
        assert p._resolve(anchor) == base
        # patched: cheat bytes at the site -> STILL resolves (bytes are wildcarded)
        m.write(site, cheat.patched)
        p._sites.pop(anchor, None)
        assert p._resolve(anchor) == base, f"{anchor} lost its match once {cheat_name} patched"
        assert p.is_enabled(cheat_name)              # ground truth reads the patched bytes
        m.write(site, cheat.orig)                    # restore for the next iteration
        p._sites.pop(anchor, None)


def test_is_enabled_reads_ground_truth_without_cache(game):
    # status must reflect real memory even when the cache is cold (state race / new pid):
    # is_enabled resolves rather than trusting the cache.
    m, p = game
    cheat = CHEATS["fast_place"]
    site = p._resolve("place") + cheat.patch_off
    m.write(site, cheat.patched)
    p._sites.clear()                                 # simulate a lost cache
    assert p.is_enabled("fast_place") is True
    m.write(site, cheat.orig)
    p._sites.clear()
    assert p.is_enabled("fast_place") is False


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
                          "pickup": 50, "spawn_rate": 15, "loot": 100,
                          "max_minions": 10}
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
