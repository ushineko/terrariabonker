"""Clamping the smart cursor's search radius (spec 034).

SmartCursorLookup sizes its search box from GetTileRegion, which calls the GetRanges that
tool_reach forces and then adds blockRange. The box is an area, so a reach of 75 means
22,801 tiles scanned per frame while Shift is held. The clamp works on the box after the
fact, because GetTileRegion itself has nine callers including the tile-interaction checks
that tool_reach exists to extend.
"""

import struct

import pytest

from terrariabonker import patcher as P
from terrariabonker.patcher import ANCHORS, INJECTIONS, Patcher, _shrink_smart_cursor

BASE = 0x40000000
CODE = BASE + 0x2000
AT = 0x100


@pytest.fixture
def game(tmp_path, monkeypatch):
    from conftest import FakeMem

    from terrariabonker import profile
    monkeypatch.setattr(P, "_STATE", str(tmp_path / "patches.json"))
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    m = FakeMem(BASE, 0x8000)
    m.write(CODE + AT, ANCHORS["smart_cursor"].pattern.raw)
    m.write(CODE + AT + INJECTIONS["smart_cursor"].inject_off,
            INJECTIONS["smart_cursor"].overwrite)
    m.write(CODE + 0xC00, b"\xcc" * 0x100)
    p = Patcher(m)
    p._exec_regions = lambda writable=False: [(CODE, CODE + 0x1000)]
    # Stubs live in memory we allocate, so a synthetic game needs an arena too.
    # Stubbed rather than bootstrapped: allocating means making the game call
    # VirtualAlloc, which a fake process cannot do.
    p._arena = BASE
    p.arena = lambda *a, **k: BASE
    return m, p


def test_the_stub_reproduces_the_displaced_store_first(game):
    """The clamp result must land before we read the field back for the midpoint."""
    body = _shrink_smart_cursor(20)
    assert body[:3] == b"\x89\x46\x3c"


def test_the_displaced_test_is_last_so_the_following_je_sees_right_flags(game):
    """test ebx,ebx sets the flags the instruction after our jump-back branches on; doing
    arithmetic after it would clobber them."""
    body = _shrink_smart_cursor(20)
    assert body[-2:] == b"\x85\xdb"
    assert b"\x85\xdb" not in body[:-2], "the test must appear only at the end"


def test_the_radius_is_baked_into_the_stub(game):
    for n in (5, 20, 150):
        body = _shrink_smart_cursor(n)
        assert body.count(struct.pack("<i", n)) == 4, "two sub + two add, x and y"


def test_the_box_covers_both_the_player_and_the_cursor():
    """Two earlier shapes failed in play. Player-centred: the box stopped containing the
    cursor, and these same fields are the in-reach test right after, so smart placement
    dropped out. Cursor-centred: the span back toward the player was cut, and the search
    works outward from the player. So the box must span both ends."""
    body = _shrink_smart_cursor(20)
    assert b"\x8b\x4e\x28" in body, "reads screenTargetX (the cursor)"
    assert b"\x8b\x4e\x2c" in body, "reads screenTargetY"
    # the player end comes from the original box's own midpoint
    assert body.count(b"\xd1\xf8") == 2, "sar eax,1 per axis -> midpoint = player tile"
    assert body.count(b"\x91") == 2, "xchg per axis, so lo/hi are min/max of the pair"


def test_the_box_only_ever_shrinks():
    """Intersection, not replacement: max() the starts and min() the ends, so a genuinely
    out-of-reach target still bails exactly as vanilla does."""
    body = _shrink_smart_cursor(20)
    assert body.count(b"\x7e\x03") == 2, "jle guard on each start (keep the larger)"
    assert body.count(b"\x7d\x03") == 2, "jge guard on each end (keep the smaller)"


def test_a_zero_or_negative_radius_is_floored(game):
    for n in (0, -5):
        assert struct.pack("<i", 1) in _shrink_smart_cursor(n)


def test_it_touches_only_the_four_region_fields(game):
    """Placement reach, tool reach and interaction range must be untouched — the stub only
    rewrites the box SmartCursorLookup already computed."""
    body = _shrink_smart_cursor(20)
    for off in (0x30, 0x34, 0x38, 0x3C):          # the reachable* fields via esi
        assert bytes([0x46, off]) in body or bytes([0x4E, off]) in body, hex(off)


def test_enable_installs_a_jump_and_disable_restores(game):
    m, p = game
    site = CODE + AT + INJECTIONS["smart_cursor"].inject_off
    p.enable("smart_cursor")
    assert m.read(site, 1) == b"\xe9"
    p.disable("smart_cursor")
    assert m.read(site, 5) == INJECTIONS["smart_cursor"].overwrite


def test_retuning_the_value_rewrites_the_stub(game):
    """Changing the radius must take effect without a disable/enable cycle."""
    m, p = game
    p.enable("smart_cursor", value=20)
    cave = p._inj["smart_cursor"]["sites"][0]["cave"]
    assert struct.pack("<i", 20) in m.read(cave, p._inj["smart_cursor"]["stub_len"])
    p.enable("smart_cursor", value=8)
    cave = p._inj["smart_cursor"]["sites"][0]["cave"]
    stub = m.read(cave, p._inj["smart_cursor"]["stub_len"])
    assert struct.pack("<i", 8) in stub and struct.pack("<i", 20) not in stub


def test_anchor_still_resolves_with_the_cheat_applied(game):
    _, p = game
    p.enable("smart_cursor")
    p._sites.clear()
    assert p.resolution("smart_cursor").available


def test_it_lands_in_the_build_section_beside_the_reach_cheats(game):
    from terrariabonker.patcher import PATCH_CATALOG
    assert PATCH_CATALOG["smart_cursor"].section == "Build"
