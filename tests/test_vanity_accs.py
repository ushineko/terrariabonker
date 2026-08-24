"""The vanity-accessory cheat: two loop bounds plus a slot-clamp cave (spec 032).

Vanilla runs ApplyEquipVanity for the vanity accessory slots (13-19) but never
ApplyEquipFunctional, so info accessories work there and nothing else does. This widens
both UpdateEquips loop bounds and clamps the slot before the call, because
ApplyEquipFunctional indexes hideVisibleAccessory[slot] and that array is bool[10].
"""

import pytest

from terrariabonker import patcher as P
from terrariabonker.patcher import ANCHORS, INJECTIONS, Patcher

BASE = 0x40000000
CODE = BASE + 0x2000
APPLY_AT = 0x100          # where the equip_apply anchor is planted
BENEFITS_AT = 0x800       # where the equip_benefits anchor is planted

BOUND_OFF_APPLY = 27      # the `cmp [ebp-X],0xa` immediate inside equip_apply
BOUND_OFF_BENEFITS = 48   # ditto inside equip_benefits


@pytest.fixture
def game(tmp_path, monkeypatch):
    from conftest import FakeMem

    from terrariabonker import profile
    monkeypatch.setattr(P, "_STATE", str(tmp_path / "patches.json"))
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    m = FakeMem(BASE, 0x8000)
    # plant both anchor bodies, with the real bytes at the wildcarded positions
    m.write(CODE + APPLY_AT, ANCHORS["equip_apply"].pattern.raw)
    m.write(CODE + APPLY_AT + INJECTIONS["vanity_accs"].inject_off,
            INJECTIONS["vanity_accs"].overwrite)
    m.write(CODE + APPLY_AT + BOUND_OFF_APPLY, b"\x0a")
    m.write(CODE + BENEFITS_AT, ANCHORS["equip_benefits"].pattern.raw)
    m.write(CODE + BENEFITS_AT + BOUND_OFF_BENEFITS, b"\x0a")
    # a run of int3 for the cave allocator to borrow
    m.write(CODE + 0xB00, b"\xcc" * 0x80)
    p = Patcher(m)
    p._exec_regions = lambda writable=False: [(CODE, CODE + 0x1000)]
    return m, p


def test_both_loop_bounds_widen_to_20(game):
    m, p = game
    p.enable("vanity_accs")
    assert m.read(CODE + APPLY_AT + BOUND_OFF_APPLY, 1) == b"\x14", "ApplyEquipFunctional loop"
    assert m.read(CODE + BENEFITS_AT + BOUND_OFF_BENEFITS, 1) == b"\x14", "benefits loop"


def test_disable_restores_both_bounds(game):
    m, p = game
    p.enable("vanity_accs")
    p.disable("vanity_accs")
    assert m.read(CODE + APPLY_AT + BOUND_OFF_APPLY, 1) == b"\x0a"
    assert m.read(CODE + BENEFITS_AT + BOUND_OFF_BENEFITS, 1) == b"\x0a"


def test_the_call_site_jumps_to_a_cave(game):
    m, p = game
    p.enable("vanity_accs")
    site = CODE + APPLY_AT + INJECTIONS["vanity_accs"].inject_off
    assert m.read(site, 1) == b"\xe9", "expected a jmp rel32 at the injection point"


def test_the_stub_clamps_a_vanity_slot_then_replays_the_displaced_stores(game):
    m, p = game
    p.enable("vanity_accs")
    cave = p._inj["vanity_accs"]["sites"][0]["cave"]
    stub = m.read(cave, p._inj["vanity_accs"]["stub_len"])
    # cmp eax,0xa / jl +3 / sub eax,0xa
    assert stub[:8] == bytes.fromhex("83f80a7c0383e80a")
    # then the two stores it displaced, then a jmp back
    assert stub[8:15] == INJECTIONS["vanity_accs"].overwrite
    assert stub[15:16] == b"\xe9"


def test_clamp_maps_every_vanity_slot_into_the_hide_array(game):
    """The whole point: 13..19 must land in 3..9, and 0..9 must pass through, because
    hideVisibleAccessory is bool[10] and an out-of-range read throws every frame."""
    def clamped(slot):                      # what the stub computes
        return slot - 10 if slot >= 10 else slot

    for slot in range(0, 10):
        assert clamped(slot) == slot
    for slot in range(10, 20):
        assert 0 <= clamped(slot) < 10, slot
    assert [clamped(s) for s in range(13, 20)] == [3, 4, 5, 6, 7, 8, 9]


def test_disable_scrubs_the_cave_and_restores_the_call_site(game):
    m, p = game
    p.enable("vanity_accs")
    rec = dict(p._inj["vanity_accs"])
    p.disable("vanity_accs")
    site = CODE + APPLY_AT + INJECTIONS["vanity_accs"].inject_off
    assert m.read(site, 7) == INJECTIONS["vanity_accs"].overwrite
    cave, n = rec["sites"][0]["cave"], rec["stub_len"]
    assert m.read(cave, n) == b"\xcc" * n, "the stub should be scrubbed"


def test_is_enabled_tracks_the_toggle(game):
    _, p = game
    assert p.is_enabled("vanity_accs") is False
    p.enable("vanity_accs")
    assert p.is_enabled("vanity_accs") is True
    p.disable("vanity_accs")
    assert p.is_enabled("vanity_accs") is False


def test_anchor_still_resolves_once_the_cheat_is_applied(game):
    """A cold re-resolve must work with the cheat on, or a restart cannot turn it off:
    the anchor wildcards both the displaced bytes and the bound it patches."""
    _, p = game
    p.enable("vanity_accs")
    p._sites.clear()                        # force a fresh scan
    assert p.resolution("equip_apply").available
    assert p.resolution("equip_benefits").available
