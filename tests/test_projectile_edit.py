"""Per-type projectile overrides (spec 047), against a planted memory image.

The cases worth having are the ones the recon got wrong in the real game: a bool written
four bytes wide, a field enforced when it should be set once, and a projectile identified
by its slot rather than by the object in it.
"""

import struct

import pytest

from conftest import FakeMem
from terrariabonker import projectiles as P
from terrariabonker import projectile_edit as PE

BASE = 0x20000000
ARR = BASE + 0x1000
OBJ0 = BASE + 0x40000
STRIDE = 0x200

SKULL = 837
BONE = 532


@pytest.fixture
def mem():
    return FakeMem(BASE, 0x400000)


def plant(mem, live: dict[int, int], obj_at=None):
    """Plant the array with ``{slot: projectile type}`` active; everything else inactive.

    ``obj_at`` overrides the object address for a slot, which is how a test says "the game
    recycled this slot into a different projectile".
    """
    mem.write(ARR + P.ARRAY_LEN_OFF, struct.pack("<i", P.ARRAY_LEN))
    for slot in range(P.ARRAY_LEN):
        obj = (obj_at or {}).get(slot, OBJ0 + slot * STRIDE)
        mem.write(ARR + P.ARRAY_DATA_OFF + slot * 4, struct.pack("<I", obj))
        mem.write(obj + P.ACTIVE_OFF, b"\x01" if slot in live else b"\x00")
        if slot in live:
            mem.write_i32(obj + P.TYPE_OFF, live[slot])
    return {slot: (obj_at or {}).get(slot, OBJ0 + slot * STRIDE) for slot in live}


def test_only_the_named_type_is_touched(mem):
    objs = plant(mem, {3: SKULL, 4: BONE})
    mem.write(objs[3] + P.TILECOLLIDE_OFF, b"\x01")
    mem.write(objs[4] + P.TILECOLLIDE_OFF, b"\x01")

    PE.ProjectileEditor().sweep(mem, ARR, {SKULL: {"tileCollide": 0}})

    assert mem.read(objs[3] + P.TILECOLLIDE_OFF, 1) == b"\x00"
    assert mem.read(objs[4] + P.TILECOLLIDE_OFF, 1) == b"\x01"    # a different type


def test_an_inactive_projectile_is_left_alone(mem):
    objs = plant(mem, {3: SKULL})
    dead = OBJ0 + 9 * STRIDE
    mem.write_i32(dead + P.TYPE_OFF, SKULL)                       # right type, not active
    mem.write(dead + P.TILECOLLIDE_OFF, b"\x01")

    PE.ProjectileEditor().sweep(mem, ARR, {SKULL: {"tileCollide": 0}})

    assert mem.read(dead + P.TILECOLLIDE_OFF, 1) == b"\x01"
    assert mem.read(objs[3] + P.TILECOLLIDE_OFF, 1) == b"\x00"


def test_a_bool_is_one_byte_and_spares_its_neighbour(mem):
    """`reflected` sits at 0x0C9, immediately after `hostile`.

    The probe this module replaces wrote bools as int32, which reaches three bytes past
    the field. Here the guard is `extraUpdates`, which begins at the word after
    `tileCollide` and must survive a write of 0.
    """
    objs = plant(mem, {3: SKULL})
    mem.write_i32(objs[3] + P.EXTRAUPDATES_OFF, 7)
    mem.write(objs[3] + P.TILECOLLIDE_OFF + 1, b"\xAA\xBB\xCC")

    PE.ProjectileEditor().sweep(mem, ARR, {SKULL: {"tileCollide": 0}})

    assert mem.read(objs[3] + P.TILECOLLIDE_OFF, 1) == b"\x00"
    assert mem.read(objs[3] + P.TILECOLLIDE_OFF + 1, 3) == b"\xAA\xBB\xCC"
    assert mem.read_i32(objs[3] + P.EXTRAUPDATES_OFF) == 7


def test_penetrate_carries_maxpenetrate_with_it(mem):
    """SetDefaults ends with `maxPenetrate = penetrate`; the game treats them as a pair."""
    objs = plant(mem, {3: SKULL})
    PE.ProjectileEditor().sweep(mem, ARR, {SKULL: {"penetrate": -1}})
    assert mem.read_i32(objs[3] + P.PENETRATE_OFF) == -1
    assert mem.read_i32(objs[3] + P.MAXPENETRATE_OFF) == -1


def test_values_are_clamped_where_they_are_written(mem):
    """Not in the widget. A value reaching the game unbounded is the thing to prevent."""
    objs = plant(mem, {3: SKULL})
    PE.ProjectileEditor().sweep(mem, ARR, {SKULL: {"extraUpdates": 9999, "scale": 500.0}})
    assert mem.read_i32(objs[3] + P.EXTRAUPDATES_OFF) == PE.FIELDS["extraUpdates"].hi
    assert struct.unpack("<f", mem.read(objs[3] + P.SCALE_OFF, 4))[0] == PE.FIELDS["scale"].hi


def test_timeleft_is_set_once_and_not_pinned(mem):
    """Pinning it would stop projectiles expiring and fill all 1001 slots.

    The game allocates the array up front and reuses it; a projectile that can never run
    out of life never frees its slot, and the player's weapons quietly stop firing.
    """
    objs = plant(mem, {3: SKULL})
    ed = PE.ProjectileEditor()
    ed.sweep(mem, ARR, {SKULL: {"timeLeft": 3000}})
    assert mem.read_i32(objs[3] + P.TIMELEFT_OFF) == 3000

    mem.write_i32(objs[3] + P.TIMELEFT_OFF, 12)        # the game spends it down
    ed.sweep(mem, ARR, {SKULL: {"timeLeft": 3000}})
    assert mem.read_i32(objs[3] + P.TIMELEFT_OFF) == 12   # not topped back up


def test_a_recycled_slot_counts_as_a_new_projectile(mem):
    """Identity is the object, not the index -- fast weapons reuse slots constantly."""
    objs = plant(mem, {3: SKULL})
    ed = PE.ProjectileEditor()
    ed.sweep(mem, ARR, {SKULL: {"timeLeft": 3000}})

    # Past every slot's default object, so planting cannot mark it inactive again.
    fresh = OBJ0 + P.ARRAY_LEN * STRIDE + 0x1000
    plant(mem, {3: SKULL}, obj_at={3: fresh})
    ed.sweep(mem, ARR, {SKULL: {"timeLeft": 3000}})
    assert mem.read_i32(fresh + P.TIMELEFT_OFF) == 3000
    assert objs[3] != fresh


def test_no_overrides_writes_nothing(mem):
    objs = plant(mem, {3: SKULL})
    mem.write(objs[3] + P.TILECOLLIDE_OFF, b"\x01")
    assert PE.ProjectileEditor().sweep(mem, ARR, {}) == {"patched": 0, "types": {}}
    assert mem.read(objs[3] + P.TILECOLLIDE_OFF, 1) == b"\x01"


def test_an_unknown_field_is_ignored_not_written(mem):
    """The GUI and a stale profile both reach this with names it may not know."""
    plant(mem, {3: SKULL})
    out = PE.ProjectileEditor().sweep(mem, ARR, {SKULL: {"aiStyle": 1, "nonsense": 3}})
    assert out["patched"] == 0


def test_the_dangerous_fields_are_not_reachable():
    """aiStyle crashes; hostile/friendly turn the player's own projectiles on them."""
    for name in ("aiStyle", "hostile", "friendly", "damage"):
        assert name not in PE.FIELDS


def test_a_projectile_that_vanishes_mid_sweep_does_not_raise(mem):
    """The array is read once, then each object is read individually -- they can differ."""
    plant(mem, {3: SKULL})
    mem.write(ARR + P.ARRAY_DATA_OFF + 3 * 4, struct.pack("<I", BASE + 0x3F0000))
    out = PE.ProjectileEditor().sweep(mem, ARR, {SKULL: {"tileCollide": 0}})
    assert out["patched"] == 0


# --- the CLI's override parser -------------------------------------------------

def test_the_parser_builds_a_type_keyed_map():
    from terrariabonker.cli import _parse_overrides

    got = _parse_overrides(["837:tileCollide=0", "837:timeLeft=3000", "532:scale=2.5"])
    assert got == {837: {"tileCollide": 0, "timeLeft": 3000}, 532: {"scale": 2.5}}


def test_an_unknown_field_is_refused_not_dropped():
    """A typo in a profile would otherwise look exactly like a field that does nothing.

    That is a diagnosis this project has already paid an afternoon for, so the parser is
    loud about it rather than quietly writing less than it was asked to.
    """
    from terrariabonker.cli import _parse_overrides

    with pytest.raises(SystemExit):
        _parse_overrides(["837:tileColide=0"])          # one letter short
    with pytest.raises(SystemExit):
        _parse_overrides(["837:aiStyle=1"])             # real field, deliberately unsafe


def test_a_malformed_pair_is_refused():
    from terrariabonker.cli import _parse_overrides

    for bad in ("837:tileCollide", "tileCollide=0", "837:scale=huge"):
        with pytest.raises(SystemExit):
            _parse_overrides([bad])


def test_the_argv_is_stable_for_the_same_overrides():
    """Sorted, so the same request does not reorder itself between calls."""
    from terrariabonker.gui.client import projectile_tick_argv

    a = projectile_tick_argv({837: {"timeLeft": 3000, "tileCollide": 0}})
    b = projectile_tick_argv({837: {"tileCollide": 0, "timeLeft": 3000}})
    assert a == b
    assert "837:tileCollide=0" in a and "837:timeLeft=3000" in a


def test_the_argv_round_trips_through_the_parser():
    """The GUI's argv and the CLI's parser are the two halves of one contract."""
    from terrariabonker.cli import _parse_overrides
    from terrariabonker.gui.client import projectile_tick_argv

    want = {837: {"tileCollide": 0, "timeLeft": 3000}, 532: {"scale": 2.5}}
    argv = projectile_tick_argv(want)
    pairs = [argv[i + 1] for i, a in enumerate(argv) if a == "--set"]
    assert _parse_overrides(pairs) == want
