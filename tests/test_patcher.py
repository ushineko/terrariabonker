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

    from terrariabonker import profile
    monkeypatch.setattr(P, "_STATE", str(tmp_path / "patches.json"))
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))   # isolate profile
    m = FakeMem(BASE, 0x20000)
    # a player so value writes (mining/reach) have somewhere to go
    m.plant_mono_string(NAME_AT, "hero")
    m.plant_player(LIFE, [100, 100, 80, 20, 20, 20], NAME_AT)
    # plant the anchors at known spots in the "code" region
    m.write(CODE + 0x100, ANCHORS["reset_block"].pattern.raw)
    m.write(CODE + 0x400, ANCHORS["place"].pattern.raw)
    # pristine "off" baseline at the wildcarded patch site (raw has 0s there)
    m.write(CODE + 0x400 + CHEATS["fast_place"].patch_off, CHEATS["fast_place"].orig)
    p = Patcher(m)
    p._exec_regions = lambda writable=False: [(CODE, CODE + 0x1000)]   # planted region only
    # Stubs live in memory we allocate, so a synthetic game needs an arena too.
    # Stubbed rather than bootstrapped: allocating means making the game call
    # VirtualAlloc, which a fake process cannot do.
    # Clear of the planted player and code: a real arena is 0x10000 bytes of slots, and
    # slots are indexed by sorted injection name, so adding an injection moves every
    # other one. Overlapping the player made that shift look like "no player found".
    ARENA = BASE + 0x10000
    p._arena = ARENA
    p.arena = lambda *a, **k: ARENA
    return m, p


def test_status_all_off_initially(game):
    _, p = game
    assert p.status() == {"mining": False, "reach": False, "fast_place": False,
                          "tool_reach": False, "pickup": False, "spawn_rate": False,
                          "loot": False, "teleport": False, "max_minions": False,
                          "vanity_accs": False, "inventory_accs": False,
                          "smart_cursor": False, "pylons": False,
                          "ore_extract": False, "auto_use": False}


def test_tool_reach_injection_roundtrip(game):
    m, p = game
    m.write(CODE + 0x700, ANCHORS["getranges"].pattern.raw)     # plant the method prologue
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


def test_no_anchor_spells_out_the_bytes_its_own_injection_overwrites():
    """An anchor whose literal bytes include the patch site stops matching the moment the
    cheat is applied. Everything that resolves from cold then reports the cheat off and
    cannot find the site to take it back off again -- found on the live game, where the
    extractor was installed and running while a fresh process insisted it was not."""
    # pickup is a known exception: wildcarding its six bytes takes `grabitems` from one
    # live site to 124, and a wrong site is a corrupted game where a wrong status is only
    # a wrong status. Listed here so it stays a decision rather than an oversight.
    known = {"pickup"}
    for name, inj in sorted(P.INJECTIONS.items()):
        mask = ANCHORS[inj.anchor].pattern.mask
        lo, hi = inj.inject_off, inj.inject_off + len(inj.overwrite)
        pinned = sum(mask[lo:hi])
        if name in known:
            assert pinned, f"{name!r} no longer needs its exception — drop it from `known`"
            continue
        assert pinned == 0, (
            f"anchor {inj.anchor!r} pins {pinned} of the {hi - lo} bytes "
            f"{name!r} overwrites — it will not match its own patched site")


def test_a_fresh_process_sees_an_injection_another_process_installed(game):
    """Reported from the game: the GUI showed the extractor on, a CLI probe said every
    injection was off, and the game was in fact patched. A cold cache was being read as
    "not installed" rather than as "no answer yet" -- and it is cold in exactly the case
    the answer matters, a second process asking about a game it did not patch."""
    m, p = game
    m.write(CODE + 0x700, ANCHORS["getranges"].pattern.raw)
    inj = P.INJECTIONS["tool_reach"]
    m.write(CODE + 0x700 + inj.inject_off, inj.overwrite)
    p.enable("tool_reach", value=40)

    fresh = Patcher(m)
    fresh._exec_regions = lambda writable=False: [(CODE, CODE + 0x1000)]
    fresh._arena = BASE
    fresh.arena = lambda *a, **k: BASE
    fresh._inj, fresh._sites = {}, {}     # did not do the patching, so has no record of it
    assert fresh.is_enabled("tool_reach") is True


def test_pickup_injection_uses_imul_stub(game):
    m, p = game
    m.write(CODE + 0x900, ANCHORS["grabitems"].pattern.raw)      # plant the grab-range site
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
    m.write(CODE + 0x900, ANCHORS["get_spawn_rate"].pattern.raw)   # plant the prologue
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
    m.write(CODE + 0x600, P.ANCHORS["trydrop"].pattern.raw)      # plant the prologue anchor
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
    m.write(a, P.ANCHORS["trydrop"].pattern.raw)
    m.write(b, P.ANCHORS["trydrop"].pattern.raw)
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
    m.write(CODE + 0x600, P.ANCHORS["trydrop"].pattern.raw)
    inject = CODE + 0x600 + inj.inject_off
    m.write(inject, inj.overwrite)
    p.enable("loot", value=100)
    sites_before = [s["inject"] for s in p._inj["loot"]["sites"]]
    # obliterate the anchor: a re-resolve would now raise. Idempotent enable must not care.
    m.write(CODE + 0x600, b"\x90" * len(P.ANCHORS["trydrop"].pattern.raw))
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
    m.write(tp, P.ANCHORS["trigger_ping"].pattern.raw)
    m.write(tele, P.ANCHORS["player_teleport"].pattern.raw)
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
    m.write(CODE + 0x800, P.ANCHORS["reset_minions"].pattern.raw)   # plant the reset instruction
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
        patched = cheat.make_patched(1) if cheat.make_patched else cheat.patched
        base = p._resolve(anchor)
        site = base + cheat.patch_off
        # pristine: original bytes at the site -> resolves
        m.write(site, cheat.orig)
        p._sites.pop(anchor, None)
        assert p._resolve(anchor) == base
        # patched: cheat bytes at the site -> STILL resolves (bytes are wildcarded)
        m.write(site, patched)
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
    m.write(site, cheat.make_patched(4))
    p._sites.clear()                                 # simulate a lost cache
    assert p.is_enabled("fast_place") is True
    m.write(site, cheat.orig)
    p._sites.clear()
    assert p.is_enabled("fast_place") is False


def test_fast_place_presets(game):
    import struct as _s
    m, p = game
    site = CODE + 0x400 + CHEATS["fast_place"].patch_off
    p.enable("fast_place")                        # default preset: Fast (itemTime 4)
    assert m.read(site, 10) == b"\xbf" + _s.pack("<i", 4) + b"\x90" * 5
    assert p.is_enabled("fast_place")
    p.enable("fast_place", value=1)               # Hyper (itemTime 1) — live re-tune
    assert m.read(site, 5) == b"\xbf" + _s.pack("<i", 1)
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
                          "max_minions": 10, "fast_place": 4,   # Fast preset default
                          "smart_cursor": 20,
                          "ore_extract": 0}    # "Ores only"; gems are the opt-in


def test_applied_value_is_recorded_and_restored(game):
    m, p = game
    m.write(CODE + 0x700, ANCHORS["getranges"].pattern.raw)
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
    p._exec_regions = lambda writable=False: [(BASE, BASE + 0x2000)]
    # Stubs live in memory we allocate, so a synthetic game needs an arena too.
    # Stubbed rather than bootstrapped: allocating means making the game call
    # VirtualAlloc, which a fake process cannot do.
    p._arena = BASE
    p.arena = lambda *a, **k: BASE
    with pytest.raises(PatchError):
        p.enable("fast_place")


def test_pylons_returns_zero_not_one(game):
    """Polarity is the dangerous part of spec 037.

    The hook is registered with badReturn: 1 and TileObject.CanPlace rejects the
    placement when the hook's return *equals* badReturn. So the stub must return 0.
    Returning 1 would not remove the limit — it would ban pylons entirely.
    """
    from terrariabonker.patcher import CHEATS

    cheat = CHEATS["pylons"]
    assert cheat.patched == b"\x31\xc0\xc3", "not xor eax,eax; ret"
    assert cheat.patched[:2] in (b"\x31\xc0", b"\x33\xc0"), "does not zero eax"
    assert b"\xb8\x01\x00\x00\x00" not in cheat.patched, "returns 1 — this bans pylons"


def test_pylons_patches_the_method_entry(game):
    """patch_off 0: the stub replaces the prologue, so nothing is displaced that would
    have to be reproduced, and no code cave is needed."""
    from terrariabonker.patcher import CHEATS

    cheat = CHEATS["pylons"]
    assert cheat.patch_off == 0
    assert cheat.orig == b"\x55\x8b\xec", "push ebp; mov ebp,esp"
    assert len(cheat.patched) == len(cheat.orig), "patch must not change the length"


def test_the_pylon_anchor_wildcards_what_the_cheat_overwrites():
    """Otherwise a cold re-resolve fails once applied and it could never be turned off —
    the trap specs 032-034 each hit."""
    from terrariabonker.patcher import ANCHORS, CHEATS

    anchor = ANCHORS[CHEATS["pylons"].anchor]
    mask = anchor.pattern.mask if hasattr(anchor.pattern, "mask") else None
    if mask is None:
        pytest.skip("pattern exposes no mask")
    assert not any(mask[:3]), "the overwritten bytes are not wildcarded"


def test_pylons_is_a_build_section_cheat():
    from terrariabonker.patcher import SECTIONS

    build = dict(SECTIONS)["Build"]
    assert "pylons" in build


def test_the_arena_bootstrap_hooks_the_injection_point_not_the_anchor():
    """The bug this guards cost a morning and looked like a game update.

    `arena()` borrows the extractor's site for its springboard. It resolved the anchor but
    never added `inject_off`, which was harmless while that injection hooked an anchor at
    offset 0 — and became a jump written 0x15 bytes early the moment the injection moved
    to an anchor whose match starts before the site. It landed in the middle of
    `Player.Update`'s dead-check, a method that runs every frame, so the game died
    instantly on every launch that auto-restored the cheat.
    """
    import inspect
    from terrariabonker import patcher as P

    src = inspect.getsource(P.Patcher.arena)
    assert "inj.inject_off" in src, \
        "arena() resolves the anchor without adding inject_off — it will hook the wrong byte"


def test_a_jump_is_never_written_over_bytes_we_would_not_put_back(game):
    """A 5-byte jump goes over live code and the original is replayed from `overwrite`.
    If the address is wrong, both halves are wrong: the jump lands mid-instruction and the
    restore leaves `overwrite` where it never belonged. The site is checked first, which
    costs one read and makes a wrong address a refusal instead of corruption."""
    import pytest
    from terrariabonker import patcher as P

    m, p = game
    with pytest.raises(P.PatchError) as e:
        p._check_site(CODE + 0x40, b"\xde\xad\xbe\xef\x00", "test")
    msg = str(e.value)
    assert "refusing to patch" in msg
    assert "de ad be ef" in msg, "the message must show what it expected"

    # and it passes when the bytes are what we would restore
    m.write(CODE + 0x40, b"\xde\xad\xbe\xef\x00")
    p._check_site(CODE + 0x40, b"\xde\xad\xbe\xef\x00", "test")


def test_a_cave_is_never_carved_out_of_our_own_arena():
    """The crash this guards: `ore_extract`'s stub sat at the arena base and
    `inventory_accs` was handed 0x68000002 — two bytes inside it. One overwrote the other
    and the game executed the splice.

    The arena is RWX, VirtualAlloc returns it zero-filled, and disabling a stub scrubs its
    cave to 0xCC. To a scan hunting cold runs in executable memory it is the most
    attractive cave in the process. Memory we placed something in is not padding.

    Exercises the real `_exec_regions`; the shared fixture replaces it, which is how the
    first version of this test passed with the guard removed.
    """
    from unittest.mock import mock_open, patch
    from terrariabonker import patcher as P

    ARENA = 0x68000000
    maps = ("0418a000-0418c000 rwxp 00000000 00:00 0 \n"
            "%08x-%08x rwxp 00000000 00:00 0 \n" % (ARENA, ARENA + P.Patcher.ARENA_SIZE))

    pat = P.Patcher.__new__(P.Patcher)

    class mem:
        pid = 1234
    pat.mem = mem()

    pat._arena = None
    with patch("builtins.open", mock_open(read_data=maps)):
        without = pat._exec_regions()
    pat._arena = ARENA
    with patch("builtins.open", mock_open(read_data=maps)):
        with_arena = pat._exec_regions()

    assert (ARENA, ARENA + P.Patcher.ARENA_SIZE) in without, "premise: it is an exec region"
    assert (ARENA, ARENA + P.Patcher.ARENA_SIZE) not in with_arena, \
        "our own arena was offered as borrowable padding"
    assert (0x0418A000, 0x0418C000) in with_arena, "unrelated regions must survive"


def test_a_cave_is_never_handed_out_twice(game):
    """`claimed` only covers the current call. A stub installed by an earlier enable is
    invisible to it — and once that stub is disabled its cave is scrubbed to 0xCC, which is
    precisely the pattern the scan prefers. So a used cave becomes bait."""
    m, p = game
    p._arena = None
    p._inj = {}
    first = p._find_cave(48)
    p._inj["something"] = {"sites": [{"inject": CODE, "cave": first}], "stub_len": 48}
    second = p._find_cave(48)
    assert not (second < first + 48 and first < second + 48), \
        f"0x{second:X} overlaps the stub already at 0x{first:X}"


def test_arena_slots_are_deterministic_and_cannot_overlap():
    """Placement by index rather than by search is the whole point.

    Searching for space is what put `inventory_accs` two bytes inside `ore_extract`'s
    stub: a scan for cold bytes cannot tell free space from a stub that was disabled and
    scrubbed. An index has no such question to get wrong — and it gives the same address
    every time, so enable/disable/re-enable lands on the same bytes.
    """
    from terrariabonker import patcher as P

    p = P.Patcher.__new__(P.Patcher)
    p.arena = lambda *a, **k: 0x68000000

    # same answer every time, and different answers for different sites
    assert p.slot_for("loot", 0) == p.slot_for("loot", 0)
    assert p.slot_for("loot", 1) != p.slot_for("loot", 0)
    assert p.slot_for("loot", 0) != p.slot_for("pickup", 0)

    spans = sorted((p.slot_for(n, i), P.Patcher.ARENA_SLOT)
                   for n in P.INJECTIONS for i in range(P.Patcher.ARENA_MAX_SITES))
    for (a, n), (b, _) in zip(spans, spans[1:]):
        assert a + n <= b, f"slots 0x{a:X} and 0x{b:X} overlap"

    # nothing lands on the extractor's queue, or past the stamp
    lo = 0x68000000 + P.Patcher.ARENA_STUBS_OFF
    assert 0x68000000 + P.ORE_QUEUE_OFF + P.ORE_QUEUE_BYTES <= lo, "queue is inside a slot"
    assert spans[-1][0] + spans[-1][1] <= 0x68000000 + P.Patcher.ARENA_MAGIC_OFF

    import pytest
    with pytest.raises(P.PatchError):
        p.slot_for("loot", P.Patcher.ARENA_MAX_SITES)      # past the reservation
    with pytest.raises(P.PatchError):
        p.slot_for("not_an_injection", 0)


# --- auto-use (spec 043) -------------------------------------------------------

def _plant_auto_use(m, p):
    """Plant the borders_movement anchor and the bytes the stub will displace."""
    m.write(CODE + 0x900, ANCHORS["borders_movement"].pattern.raw)
    inj = P.INJECTIONS["auto_use"]
    site = CODE + 0x900 + inj.inject_off
    m.write(site, inj.overwrite)
    return inj, site


def test_auto_use_roundtrip(game):
    m, p = game
    inj, site = _plant_auto_use(m, p)
    p.enable("auto_use")
    assert m.read(site, 1) == b"\xe9"                  # jump to the stub installed
    assert p.is_enabled("auto_use")
    p.disable("auto_use")
    assert m.read(site, len(inj.overwrite)) == inj.overwrite
    assert not p.is_enabled("auto_use")


def test_auto_use_ships_disarmed(game):
    """Enabling the cheat must not press anything: the flag starts clear.

    A cheat that can fire the player's weapons has to be inert until something arms it.
    """
    m, p = game
    _plant_auto_use(m, p)
    p.enable("auto_use")
    armed = p.arena() + P.AUTO_USE_ARMED_OFF
    assert m.read_i32(armed) == 0


def test_auto_use_stub_consumes_the_flag_before_pressing(game):
    """The `and [armed],0` must come before the write to the use byte.

    Same ordering as the extractor: a stub that dies between the two presses nothing,
    where the other order would press every frame forever.
    """
    m, p = game
    _plant_auto_use(m, p)
    p.enable("auto_use")
    stub = m.read(p.slot_for("auto_use"), 96)
    clear = stub.index(b"\x83\x25")                    # and dword [armed],0
    press = stub.index(b"\xc6\x80")                    # mov byte [this+USE_ITEM_OFF],1
    assert clear < press


def test_auto_use_stub_targets_the_use_control(game):
    """The offset written is the confirmed control, from the Player object base."""
    m, p = game
    _plant_auto_use(m, p)
    p.enable("auto_use")
    stub = m.read(p.slot_for("auto_use"), 96)
    i = stub.index(b"\xc6\x80")
    assert struct.unpack("<I", stub[i + 2:i + 6])[0] == P.USE_ITEM_OFF
    assert stub[i + 6] == 1


def test_auto_use_stub_replays_the_displaced_bytes(game):
    """The stub ends with the site's own instructions; losing them corrupts the frame."""
    m, p = game
    inj, _ = _plant_auto_use(m, p)
    p.enable("auto_use")
    stub = m.read(p.slot_for("auto_use"), 96)
    assert inj.overwrite in stub
    assert stub.index(inj.overwrite) > stub.index(b"\xc6\x80")


def test_auto_use_refuses_a_site_that_is_not_what_it_will_restore(game):
    """_check_site's guard: wrong bytes at the site means both halves are wrong."""
    m, p = game
    inj, site = _plant_auto_use(m, p)
    m.write(site, b"\x90" * len(inj.overwrite))
    with pytest.raises(PatchError):
        p.enable("auto_use")


# --- arena slot assignment (the 2026-08-26 crash) -------------------------------

def test_every_injection_has_a_slot(game):
    """A new injection must be given a slot deliberately, not by whatever sorts first."""
    _, p = game
    missing = set(P.INJECTIONS) - set(P._SLOT_ORDER)
    assert not missing, f"{sorted(missing)} must be APPENDED to _SLOT_ORDER"


def test_slots_never_overlap(game):
    _, p = game
    slots = [p.slot_for(n) for n in P.INJECTIONS]
    assert len(set(slots)) == len(slots)
    for a in slots:
        for b in slots:
            assert a == b or abs(a - b) >= p.ARENA_SLOT * p.ARENA_MAX_SITES


def test_appending_an_injection_moves_no_existing_slot(game):
    """The crash: slots were indexed by sorted() name, so adding `auto_use` -- which
    sorts first -- shifted every other injection by 0x800. The new stub was written over
    a LIVE one in an arena left from a running session, and the game died a few frames
    later in unrelated code.

    Appending must be inert for everyone already in the tuple.
    """
    _, p = game
    order = P._SLOT_ORDER
    before = {n: order.index(n) for n in order}
    grown = order + ("a_new_cheat_that_sorts_first",)
    after = {n: grown.index(n) for n in order}
    assert before == after


def test_slot_order_is_not_merely_sorted(game):
    """A tuple that happens to be in sorted order would pass everything else here and
    then break the moment someone 'tidied' it back into sorted(INJECTIONS)."""
    assert list(P._SLOT_ORDER) != sorted(P._SLOT_ORDER)


def test_unknown_injection_has_no_slot(game):
    _, p = game
    with pytest.raises(PatchError):
        p.slot_for("not_an_injection")


def test_arena_stamp_changed_with_the_layout(game):
    """An arena laid out by the old numbering must not be adopted by this build.

    The stamp is the only thing that distinguishes one, and reusing it would put today's
    stubs on top of yesterday's live ones -- which is precisely what crashed the game.
    """
    _, p = game
    assert p.ARENA_MAGIC != b"TBARENA1"
