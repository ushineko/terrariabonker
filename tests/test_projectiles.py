"""Bobber location and the bite condition, against a planted memory image.

The interesting cases are the ones that misled the live probes: the catch counter
sharing a slot with the catch, and a bite that is over being indistinguishable from
one that never happened unless ``ai[1]`` is consulted.
"""

import struct

import pytest

from conftest import FakeMem
from terrariabonker import projectiles as P

BASE = 0x10000000
MAIN = BASE + 0x1000            # Main's static-data block
ARR = BASE + 0x40000            # the Projectile[1001] array
OBJ0 = BASE + 0x80000           # first projectile object
STRIDE = 0x198
VTABLE = 0xABCD0000


def plant(mem, bobber_slots=(2,), ai=(0.0, 0.0, 0.0), local_ai=(0.0, 0.0, 0.0)):
    """Plant Main.projectile, 1001 objects, and float[3] ai/localAI for each."""
    mem.write(ARR + P.ARRAY_LEN_OFF, struct.pack("<i", P.ARRAY_LEN))
    for slot in range(P.ARRAY_LEN):
        obj = OBJ0 + slot * STRIDE
        mem.write(ARR + P.ARRAY_DATA_OFF + slot * 4, struct.pack("<I", obj))
        mem.write(obj, struct.pack("<I", VTABLE))
        mem.write(obj + P.ACTIVE_OFF, b"\x01" if slot in bobber_slots else b"\x00")
        mem.write(obj + P.BOBBER_OFF, b"\x01" if slot in bobber_slots else b"\x00")
        for off, vals in ((P.AI_OFF, ai), (P.LOCALAI_OFF, local_ai)):
            arrp = obj + 0x100 + (0 if off == P.AI_OFF else 0x20)
            mem.write(obj + off, struct.pack("<I", arrp))
            mem.write(arrp + P.ARRAY_LEN_OFF, struct.pack("<i", 3))
            mem.write(arrp + P.ARRAY_DATA_OFF, struct.pack("<3f", *vals))
    mem.write(MAIN + P.MAIN_PROJECTILE_OFF, struct.pack("<I", ARR))


@pytest.fixture
def mem():
    return FakeMem(BASE, 0x200000)


def test_array_found_through_the_known_static(mem):
    plant(mem)
    assert P.projectile_array(mem, MAIN) == ARR


def test_array_still_found_when_the_static_moves(mem):
    """A game update that moves Main.projectile costs a scan, not the cheat."""
    plant(mem)
    mem.write(MAIN + P.MAIN_PROJECTILE_OFF, struct.pack("<I", 0))
    mem.write(MAIN + 0x2000, struct.pack("<I", ARR))
    assert P.projectile_array(mem, MAIN) == ARR


def test_no_array_is_none_not_a_wrong_answer(mem):
    assert P.projectile_array(mem, MAIN) is None


def test_only_bobbers_are_returned(mem):
    plant(mem, bobber_slots=(2, 7))
    assert [b.slot for b in P.find_bobbers(mem, ARR)] == [2, 7]


def test_a_bite_is_the_games_own_condition(mem):
    plant(mem, ai=(0.0, -240.0, 0.0), local_ai=(0.0, 2290.0, 0.0))
    bite = P.find_bite(mem, ARR)
    assert bite is not None and bite.catch == 2290


def test_a_negative_catch_is_an_npc(mem):
    plant(mem, ai=(0.0, -240.0, 0.0), local_ai=(0.0, -586.0, 0.0))
    assert P.find_bite(mem, ARR).catch == -586


def test_the_counter_climbing_is_not_a_bite(mem):
    """localAI[1] holds the counter between bites; without ai[1] < 0 it means nothing.

    This is the trap the live probes fell into from the other side: the slot is busy
    almost all the time, so reading it alone would report a fish every tick.
    """
    plant(mem, ai=(0.0, 0.0, 0.0), local_ai=(0.0, 651.0, 0.0))
    assert P.find_bite(mem, ARR) is None
    assert P.find_bobbers(mem, ARR)[0].counter == 651.0


def test_an_expired_bite_is_not_a_bite(mem):
    """The window closes by ai[1] reaching 0 and both fields being cleared."""
    plant(mem, ai=(0.0, 0.0, 0.0), local_ai=(0.0, 0.0, 0.0))
    assert P.find_bite(mem, ARR) is None


def test_a_bobber_already_being_reeled_is_not_a_bite(mem):
    """ai[0] == 1 means the pull path has claimed it; taking it twice is a double catch."""
    plant(mem, ai=(1.0, -240.0, 0.0), local_ai=(0.0, 2290.0, 0.0))
    assert P.find_bite(mem, ARR) is None


def test_catch_is_zero_outside_the_bite_window(mem):
    """Never name a fish from the counter -- 651 is progress, not a Rockfish."""
    plant(mem, ai=(0.0, 0.0, 0.0), local_ai=(0.0, 651.0, 0.0))
    assert P.find_bobbers(mem, ARR)[0].catch == 0


def test_a_bite_reports_no_counter(mem):
    plant(mem, ai=(0.0, -240.0, 0.0), local_ai=(0.0, 2290.0, 0.0))
    assert P.find_bite(mem, ARR).counter == 0.0


def test_an_unreadable_ai_reference_is_not_a_bobber(mem):
    """A null float[3] reference reads as absent rather than as zeroed state."""
    plant(mem)
    obj = OBJ0 + 2 * STRIDE
    mem.write(obj + P.LOCALAI_OFF, struct.pack("<I", 0))
    assert P.find_bobbers(mem, ARR) == []


def test_a_finished_bobber_is_not_in_the_water(mem):
    """The array keeps every object forever, flags and all.

    A projectile the game has finished with is marked inactive and its fields are left
    untouched -- so `bobber` alone reports a line that came out of the water minutes ago.
    Measured in-game: a reeled bobber read as present for over fifteen seconds, and the
    water never looked empty enough to cast into.
    """
    plant(mem, ai=(1.0, 0.0, 0.0), local_ai=(0.0, 0.0, 0.0))
    obj = OBJ0 + 2 * STRIDE
    mem.write(obj + P.ACTIVE_OFF, b"\x00")          # the game is done with it
    assert P.find_bobbers(mem, ARR) == []
    assert P.read_bobber(mem, ARR, 2) is None


def test_active_is_the_offset_the_runtime_gives(mem):
    """Pin the offsets as literals, because no other test in this file can.

    Every test here plants its fixture through ``P.ACTIVE_OFF`` and then reads it back
    through ``P.ACTIVE_OFF``, so the whole suite passes for any value of that constant.
    It did: ``active`` was read at ``0x03C`` for eight releases, which is ``Entity.wet``.
    Nothing noticed, because the only projectile the trainer looked at was a fishing
    bobber -- and a bobber floats in water, so ``wet`` is true exactly when a live bobber
    exists. Projectiles in flight are dry, which is why no probe ever saw one.

    These numbers come from the mono runtime's own field metadata and are re-checkable
    against a running game with ``sudo python3 tools/monofields.py --verify``.
    """
    assert P.ACTIVE_OFF == 0x078
    assert P.WET_OFF == 0x03C
    assert P.ACTIVE_OFF != P.WET_OFF
    assert (P.BOBBER_OFF, P.AI_OFF, P.LOCALAI_OFF) == (0x088, 0x044, 0x048)


def test_a_dry_projectile_is_still_active(mem):
    """The bug in one assertion: liveness must not depend on being in water.

    A bobber sits in water and a skull does not, so reading ``wet`` as ``active`` looks
    perfect for fishing and is blind to everything else the game shoots.
    """
    plant(mem, ai=(0.0, -5.0, 0.0), local_ai=(0.0, 17.0, 0.0))
    obj = OBJ0 + 2 * STRIDE
    mem.write(obj + P.WET_OFF, b"\x00")             # out of the water...
    assert P.read_bobber(mem, ARR, 2) is not None   # ...but still a live projectile

    mem.write(obj + P.ACTIVE_OFF, b"\x00")          # only `active` decides
    assert P.read_bobber(mem, ARR, 2) is None
