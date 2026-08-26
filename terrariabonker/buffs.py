"""The player's active buffs.

A buff is nothing more than a ``(type, time)`` pair in two parallel arrays that the game
counts down once per frame. That is the whole mechanism, and it is why passive potions
(spec 041) need no code patch: writing the pair is what the game itself does, and a buff
whose time stops being renewed expires on its own.

The game already uses renewal this way. Stand next to a campfire and its buff sits at a
single tick, re-applied every frame, which is exactly why it lapses the moment you walk
out of range rather than needing anything to switch it off.

Offsets are for Terraria 1.4.5.8. There are 44 slots, not the 22 of older versions --
worth stating because a search for a 22-element array finds nothing at all.
"""

from __future__ import annotations

import struct

from terrariabonker.inventory import ARR_DATA_OFF, ARR_LEN_OFF

# Pointers in the Player object, relative to statLife, to two int[] of equal length.
# They sit just before the inventory pointer (-0x664), as sibling fields do.
BUFF_TYPE_PTR_OFF = -0x670
BUFF_TIME_PTR_OFF = -0x66C

# What "added" means when nothing says otherwise: long enough that a renewal loop running
# a few times a second cannot let it lapse between rounds, short enough that the buff is
# visibly gone a moment after the trainer stops renewing it.
DEFAULT_TICKS = 120             # 2 seconds at 60fps


class BuffError(Exception):
    """The buff arrays could not be read."""


class Buffs:
    """Read and renew the live player's buffs."""

    def __init__(self, mem, life_addr: int):
        self.mem = mem
        self.life = life_addr

    # --- reading ----------------------------------------------------------
    def _array(self, ptr_off: int) -> tuple[int, int]:
        """(address of element 0, count) for one of the two arrays."""
        try:
            ptr = struct.unpack("<I", self.mem.read(self.life + ptr_off, 4))[0]
            if not ptr:
                raise BuffError("buff array pointer is null — is a player loaded?")
            n = struct.unpack("<I", self.mem.read(ptr + ARR_LEN_OFF, 4))[0]
        except (OSError, struct.error) as e:
            raise BuffError(f"could not read the buff arrays: {e}") from e
        if not 0 < n <= 256:
            raise BuffError(f"buff array length {n} is not believable")
        return ptr + ARR_DATA_OFF, n

    def slots(self) -> int:
        return self._array(BUFF_TYPE_PTR_OFF)[1]

    def _read(self, ptr_off: int) -> list[int]:
        base, n = self._array(ptr_off)
        raw = self.mem.read(base, n * 4)
        return list(struct.unpack("<%di" % n, raw))

    def active(self) -> dict[int, tuple[int, int]]:
        """``{slot: (buff type, ticks remaining)}`` for the occupied slots."""
        types, times = self._read(BUFF_TYPE_PTR_OFF), self._read(BUFF_TIME_PTR_OFF)
        return {i: (types[i], times[i]) for i in range(len(types)) if types[i]}

    def time_of(self, buff_type: int) -> int:
        """Ticks left on ``buff_type``, or 0 if it is not active."""
        for t, ticks in self.active().values():
            if t == buff_type:
                return ticks
        return 0

    # --- writing ----------------------------------------------------------
    def renew(self, buff_type: int, ticks: int = DEFAULT_TICKS) -> str:
        """Give ``buff_type`` at least ``ticks`` of time left. Never takes time away.

        Returns what happened: ``kept`` (already running longer — nothing written),
        ``renewed``, ``added``, or ``full``.

        **The refusal to shorten is the point, not an optimisation.** A player who drank a
        potion has eight minutes of it; renewing that to two seconds would leave them with
        two seconds the moment they dropped the stack, and they would blame the potion
        rather than the trainer. So a slot already running longer is left completely
        alone — not rewritten with the larger of the two values, not touched at all.
        """
        if buff_type <= 0:
            raise ValueError("buff type must be positive")
        if ticks <= 0:
            raise ValueError("ticks must be positive")
        types, times = self._read(BUFF_TYPE_PTR_OFF), self._read(BUFF_TIME_PTR_OFF)
        time_base = self._array(BUFF_TIME_PTR_OFF)[0]
        type_base = self._array(BUFF_TYPE_PTR_OFF)[0]

        for i, t in enumerate(types):
            if t == buff_type:
                if times[i] >= ticks:
                    return "kept"
                self.mem.write(time_base + i * 4, struct.pack("<i", ticks))
                return "renewed"

        for i, t in enumerate(types):
            if t == 0:
                # Time first, type last. Until the type is set the game ignores the slot,
                # so a half-written slot is an empty slot rather than a buff with no time
                # on it -- the same ordering NPC spawning uses for `active`.
                self.mem.write(time_base + i * 4, struct.pack("<i", ticks))
                self.mem.write(type_base + i * 4, struct.pack("<i", buff_type))
                return "added"
        return "full"
