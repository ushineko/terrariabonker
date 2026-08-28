"""Find ``Main.projectile`` and read the bobber's fishing state.

Read-only. Nothing here writes to the game; it exists so a cheat can ask "is a fish
biting right now?" without every caller re-deriving the layout.

The bite condition is not invented here. It is the game's own, from
``Player.ItemCheck_PullFishingBobbers``: for a projectile that is the local player's
and has ``bobber`` set, a fish is on the line when

    ai[0] == 0        the line is still out, not already being reeled in
    ai[1] <  0        the bite window, counting up 1-5 a tick until it expires at 0
    localAI[1] != 0   the catch: > 0 an item type, < 0 an NPC type

``localAI[1]`` does double duty as the catch counter, climbing past 660 before the game
rolls a catch into the same slot. Both live behind a ``float[3]`` reference rather than
inline in the projectile, which is why scanning the object's own bytes for a counter
never found one (spec 042).

Offsets are for Terraria 1.4.5.8+24893155 and are build-specific; see spec 042 for how
they were derived and ``docs/discovery.md`` for the method.
"""

from __future__ import annotations

import struct
from dataclasses import dataclass

from terrariabonker import layout

MAIN_PROJECTILE_OFF = layout.MAIN_PROJECTILE_OFF
ARRAY_LEN = 1001              # Main.projectile is Projectile[1001]
ARRAY_LEN_OFF = layout.ARR_LEN_OFF
ARRAY_DATA_OFF = layout.ARR_DATA_OFF

ACTIVE_OFF = 0x078            # Projectile.active (bool)
#: 0x03C -- where ``active`` was wrongly read until v0.39.0 -- is ``Entity.wet``.
#: The two are indistinguishable while fishing, because a bobber floats in water and is
#: therefore always wet, so every test and every hour of play agreed with the wrong
#: offset. Projectiles in FLIGHT are dry, which is why no probe ever saw one. Confirmed
#: against the mono runtime's own field metadata (``tools/monofields.py``) and live: 12
#: active projectiles in the array, all with ``wet == 0``.
WET_OFF = 0x03C               # Entity.wet (bool) -- kept named so it cannot be reused
BOBBER_OFF = 0x088            # Projectile.bobber (bool)
AI_OFF = 0x044                # Projectile.ai      -> float[3]
LOCALAI_OFF = 0x048           # Projectile.localAI -> float[3]

COUNTER_THRESHOLD = 660       # localAI[1] past this and the game rolls a catch


@dataclass
class Bobber:
    """One live bobber: its slot in ``Main.projectile``, its address and its state."""

    slot: int
    addr: int
    ai: tuple[float, float, float]
    local_ai: tuple[float, float, float]

    @property
    def reeling(self) -> bool:
        """True once the pull path has claimed this bobber (``ai[0]`` non-zero)."""
        return self.ai[0] != 0

    @property
    def biting(self) -> bool:
        """True when a catch is on the line and can still be taken."""
        return not self.reeling and self.ai[1] < 0 and self.local_ai[1] != 0

    @property
    def catch(self) -> int:
        """The catch waiting on the line: an item type, or a negative NPC type. 0 if none.

        Only meaningful while ``biting``; outside the bite window this slot is the catch
        counter instead, and reading it as a catch would name a fish by its progress bar.
        """
        return int(self.local_ai[1]) if self.biting else 0

    @property
    def counter(self) -> float:
        """Progress toward the next catch roll, 0 to ``COUNTER_THRESHOLD``.

        Zero while a bite is on the line, because the game has already spent the counter
        and reused the slot for the catch.
        """
        return 0.0 if self.biting else self.local_ai[1]


def _float3(mem, obj: int, field_off: int) -> tuple[float, float, float] | None:
    """Follow a ``float[3]`` field reference and read its three elements."""
    ptr = mem.read_u32(obj + field_off)
    if not ptr:
        return None
    raw = mem.read(ptr + ARRAY_DATA_OFF, 12)
    return struct.unpack("<3f", raw) if len(raw) == 12 else None


def _is_projectile_array(mem, ptr: int) -> bool:
    """True if ``ptr`` looks like ``Projectile[1001]``: right length, one shared vtable.

    The length check alone matches the odd unrelated allocation, so the elements are
    sampled too — a real projectile array is fully populated with objects of one class,
    because the game allocates all 1001 up front and never leaves a hole.
    """
    if ptr < 0x10000:
        return False
    if mem.read_i32(ptr + ARRAY_LEN_OFF) != ARRAY_LEN:
        return False
    raw = mem.read(ptr + ARRAY_DATA_OFF, ARRAY_LEN * 4)
    if len(raw) < ARRAY_LEN * 4:
        return False
    elems = [e for e in struct.unpack(f"<{ARRAY_LEN}I", raw) if e > 0x10000]
    if len(elems) < ARRAY_LEN * 0.9:
        return False
    return len({mem.read_u32(e) for e in elems[:20]}) == 1


def projectile_array(mem, main_base: int) -> int | None:
    """Address of ``Main.projectile``, or None.

    Reads the known static offset first and validates what it finds, then falls back to
    scanning Main's static block for the array by shape. The fallback is what found the
    offset in the first place, and keeping it means a game update that moves the field
    costs a slower lookup rather than a broken cheat.
    """
    direct = mem.read_u32(main_base + MAIN_PROJECTILE_OFF)
    if direct and _is_projectile_array(mem, direct):
        return direct
    blk = mem.read(main_base, 0x4000)
    for off in range(0, len(blk) - 4, 4):
        ptr = struct.unpack_from("<I", blk, off)[0]
        if _is_projectile_array(mem, ptr):
            return ptr
    return None


def read_bobber(mem, arr: int, slot: int) -> Bobber | None:
    """Read one slot as a live bobber, or None.

    **Both flags matter, and ``active`` is the one that is easy to forget.** The array
    holds all 1001 objects forever; a finished projectile is marked inactive and its old
    fields are left exactly where they were, ``bobber`` included. Filtering on ``bobber``
    alone therefore reports a line still in the water minutes after it came out -- which
    is what made a reeled-in bobber look "stuck" for fifteen seconds, and would have made
    auto-catch refuse to cast because it believed the water was busy.

    The game itself checks ``active`` first, in ``ItemCheck_PullFishingBobbers``. So does
    this.
    """
    raw = mem.read(arr + ARRAY_DATA_OFF + slot * 4, 4)
    if len(raw) < 4:
        return None
    obj = struct.unpack("<I", raw)[0]
    if not obj or mem.read(obj + ACTIVE_OFF, 1) != b"\x01":
        return None
    if mem.read(obj + BOBBER_OFF, 1) != b"\x01":
        return None
    ai = _float3(mem, obj, AI_OFF)
    local_ai = _float3(mem, obj, LOCALAI_OFF)
    if ai is None or local_ai is None:
        return None
    return Bobber(slot=slot, addr=obj, ai=ai, local_ai=local_ai)


def find_bobbers(mem, arr: int) -> list[Bobber]:
    """Every bobber currently in the water, in slot order.

    Usually one. More than one is normal in multiplayer and possible alone, so callers
    that want "the fish on my line" should take the first that is ``biting`` rather than
    assume a single bobber.
    """
    raw = mem.read(arr + ARRAY_DATA_OFF, ARRAY_LEN * 4)
    if len(raw) < ARRAY_LEN * 4:
        return []
    out = []
    # One read for the whole element array rather than 1001 of them: this runs on a poll
    # loop tight enough to catch a bite window, and the per-slot version spent its time in
    # syscalls reading pointers that are almost all irrelevant.
    for slot, obj in enumerate(struct.unpack(f"<{ARRAY_LEN}I", raw)):
        if not obj or mem.read(obj + ACTIVE_OFF, 1) != b"\x01":
            continue
        if mem.read(obj + BOBBER_OFF, 1) != b"\x01":
            continue
        b = read_bobber(mem, arr, slot)
        if b is not None:
            out.append(b)
    return out


def find_bite(mem, arr: int) -> Bobber | None:
    """The first bobber with a fish on the line, or None."""
    for b in find_bobbers(mem, arr):
        if b.biting:
            return b
    return None
