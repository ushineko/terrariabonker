"""Accessories taking effect from the inventory (spec 033).

Unlike the vanity cheat, this cannot be a loop-bound change: inventory items live in a
different array from armor[20]. It hooks the loop UpdateEquips already runs over all 58
inventory slots, at the point where the Item* is in eax, and calls the accessory
machinery on the items that are accessories.
"""

import struct

import pytest

from terrariabonker import patcher as P
from terrariabonker.patcher import ANCHORS, INJECTIONS, Patcher

BASE = 0x40000000
CODE = BASE + 0x2000
SCAN_AT = 0x100           # inventory_scan anchor
APPLY_AT = 0x300          # equip_apply (its call rel32 names ApplyEquipFunctional)
BENEFITS_AT = 0x500       # equip_benefits (GrantPrefixBenefits + GrantArmorBenefits)

APPLY_FN = CODE + 0xA00   # pretend method entries the call rel32s point at
PREFIX_FN = CODE + 0xA40
ARMOR_FN = CODE + 0xA80


def _plant_call(m, at, target):
    """Write `call rel32` at `at` so it lands on `target`."""
    m.write(at, b"\xe8" + struct.pack("<i", target - (at + 5)))


@pytest.fixture
def game(tmp_path, monkeypatch):
    from conftest import FakeMem

    from terrariabonker import profile
    monkeypatch.setattr(P, "_STATE", str(tmp_path / "patches.json"))
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    m = FakeMem(BASE, 0x8000)
    m.write(CODE + SCAN_AT, ANCHORS["inventory_scan"].pattern.raw)
    m.write(CODE + SCAN_AT + INJECTIONS["inventory_accs"].inject_off,
            INJECTIONS["inventory_accs"].overwrite)
    m.write(CODE + APPLY_AT, ANCHORS["equip_apply"].pattern.raw)
    m.write(CODE + BENEFITS_AT, ANCHORS["equip_benefits"].pattern.raw)
    _plant_call(m, CODE + APPLY_AT + 15, APPLY_FN)
    _plant_call(m, CODE + BENEFITS_AT + 20, PREFIX_FN)
    _plant_call(m, CODE + BENEFITS_AT + 36, ARMOR_FN)
    m.write(CODE + 0xC00, b"\xcc" * 0x100)          # cave space
    p = Patcher(m)
    p._exec_regions = lambda writable=False: [(CODE, CODE + 0x1000)]
    # Stubs live in memory we allocate, so a synthetic game needs an arena too.
    # Stubbed rather than bootstrapped: allocating means making the game call
    # VirtualAlloc, which a fake process cannot do.
    p._arena = BASE
    p.arena = lambda *a, **k: BASE
    return m, p


def test_call_targets_are_read_from_the_anchored_call_sites(game):
    _, p = game
    assert p._call_target("equip_apply", 15) == APPLY_FN
    assert p._call_target("equip_benefits", 20) == PREFIX_FN
    assert p._call_target("equip_benefits", 36) == ARMOR_FN


def test_the_site_takes_a_jump_and_disable_restores_it(game):
    m, p = game
    site = CODE + SCAN_AT + INJECTIONS["inventory_accs"].inject_off
    p.enable("inventory_accs")
    assert m.read(site, 1) == b"\xe9"
    p.disable("inventory_accs")
    assert m.read(site, 5) == INJECTIONS["inventory_accs"].overwrite


def test_stub_tests_item_accessory_before_calling_anything(game):
    """The loop runs 58x a frame and ApplyEquipFunctional is 11.6 KB — a stack of dirt
    must not pay for it."""
    m, p = game
    p.enable("inventory_accs")
    cave = p._inj["inventory_accs"]["sites"][0]["cave"]
    stub = m.read(cave, p._inj["inventory_accs"]["stub_len"])
    assert stub[:2] == b"\x8b\x00", "displaced load of the Item* comes first"
    assert stub[2:6] == b"\x80\x78\x7d\x00", "cmp byte [eax+0x7D],0 (item.accessory)"
    assert stub[6] == 0x74, "a je must skip the calls for non-accessories"


def test_stub_calls_all_three_methods(game):
    m, p = game
    p.enable("inventory_accs")
    cave = p._inj["inventory_accs"]["sites"][0]["cave"]
    stub = m.read(cave, p._inj["inventory_accs"]["stub_len"])
    for name, fn in (("ApplyEquipFunctional", APPLY_FN), ("GrantPrefixBenefits", PREFIX_FN),
                     ("GrantArmorBenefits", ARMOR_FN)):
        assert b"\xb8" + struct.pack("<I", fn) + b"\xff\xd0" in stub, name


def test_stub_protects_registers_and_restores_esp(game):
    """pushad/popad around the calls, and esp restored from ebx after each one so the
    stub is right whether or not mono cleaned the arguments."""
    m, p = game
    p.enable("inventory_accs")
    cave = p._inj["inventory_accs"]["sites"][0]["cave"]
    stub = m.read(cave, p._inj["inventory_accs"]["stub_len"])
    assert b"\x60" in stub and b"\x61" in stub, "pushad / popad"
    assert b"\x8b\xdc" in stub, "mov ebx,esp before the calls"
    assert stub.count(b"\x8b\xe3") == 3, "mov esp,ebx after each of the three calls"


def test_the_skip_branch_lands_on_the_displaced_type_read(game):
    """The je must jump exactly past the guarded section, onto the reproduced
    `mov eax,[eax+0x6c]` — landing anywhere else executes garbage."""
    m, p = game
    p.enable("inventory_accs")
    cave = p._inj["inventory_accs"]["sites"][0]["cave"]
    stub = m.read(cave, p._inj["inventory_accs"]["stub_len"])
    # a short je is relative to the next instruction, which starts at offset 8
    target = 8 + stub[7]
    assert stub[target:target + 3] == b"\x8b\x40\x6c"


def test_stub_ends_by_jumping_back(game):
    m, p = game
    p.enable("inventory_accs")
    rec = p._inj["inventory_accs"]
    cave, n = rec["sites"][0]["cave"], rec["stub_len"]
    stub = m.read(cave, n)
    assert stub[-5] == 0xE9, "jmp rel32 back to the site"
    back = cave + n + struct.unpack("<i", stub[-4:])[0]
    site = CODE + SCAN_AT + INJECTIONS["inventory_accs"].inject_off
    assert back == site + len(INJECTIONS["inventory_accs"].overwrite)


def test_anchor_still_resolves_with_the_cheat_applied(game):
    _, p = game
    p.enable("inventory_accs")
    p._sites.clear()
    assert p.resolution("inventory_scan").available


def test_disable_scrubs_the_cave(game):
    m, p = game
    p.enable("inventory_accs")
    rec = dict(p._inj["inventory_accs"])
    p.disable("inventory_accs")
    cave, n = rec["sites"][0]["cave"], rec["stub_len"]
    assert m.read(cave, n) == b"\xcc" * n
