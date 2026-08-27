"""The data each stub reads out of the arena, and the trainer's side of it.

A stub is a dozen instructions; the interesting half of a code-patch cheat is the words in
the arena it reads -- an arm flag, a queue of coordinates, a counter it writes back. That
is *per-cheat protocol*, and it had accumulated on ``Patcher`` as eight methods
(``auto_use_arm/disarm/armed/presses``, ``ore_queue/armed/arm/disarm``), so adding a cheat
meant editing the generic patch engine.

Each class here owns one cheat's slice of the arena: its offsets, its layout, and the
ordering rules that make a half-written state safe. ``Patcher`` exposes them as properties
and knows nothing about what is in them.

**The offsets must not overlap.** They are all offsets into one 64 KB block, and on
2026-08-26 auto-use's arm flag was placed inside the extractor's queue: mining a vein wrote
the tile count into the arm word and the stub pressed the player's use button for every
batch queued. ``tests/test_patcher.py`` computes the extents and fails on an overlap.
"""

from __future__ import annotations

import struct

# --- the ore extractor's queue ----------------------------------------------
ORE_MAX_BATCH = 32
ORE_QUEUE_OFF = 0x400            # a count, then MAX_BATCH (x, y) pairs: 0x400..0x504

# --- auto-use ---------------------------------------------------------------
AUTO_USE_ARMED_OFF = 0x600       # set by the trainer, cleared by the stub
AUTO_USE_COUNT_OFF = 0x604       # the stub's own tally of presses made
AUTO_USE_RELEASE_OFF = 0x608     # which second byte the stub sets, or 0 for none


class _ArenaView:
    """Base: a cheat's words in the arena, or nothing at all when there is no arena."""

    def __init__(self, mem, arena: int | None):
        self.mem = mem
        self.arena = arena

    def _at(self, off: int) -> int | None:
        return None if not self.arena else self.arena + off


class AutoUse(_ArenaView):
    """Arm a one-shot press. The stub consumes the flag and presses on the next frame."""

    def arm(self) -> bool:
        """Ask for one press on the next frame.

        Arming twice before a frame runs is one press, not two -- the stub consumes the
        flag rather than counting it. The flag means "press soon", so a caller that wants
        N presses must wait for each to land.
        """
        addr = self._at(AUTO_USE_ARMED_OFF)
        return False if addr is None else self.mem.write_i32(addr, 1)

    def disarm(self) -> bool:
        """Drop a press that has not landed yet.

        An arm is a promise to press on the *next frame*, and frames stop -- at the title
        screen, in a menu, on a world load. Left set, the flag waits and fires the moment
        the game updates again: a press the player did not ask for, arriving as their
        character appears. Found by arming 50 times at the menu and watching nothing
        consume any of them.
        """
        addr = self._at(AUTO_USE_ARMED_OFF)
        return False if addr is None else self.mem.write_i32(addr, 0)

    def armed(self) -> bool:
        """Is a press still waiting for a frame? Clears itself when the stub runs."""
        addr = self._at(AUTO_USE_ARMED_OFF)
        return bool(addr and self.mem.read_i32(addr))

    def presses(self) -> int:
        """How many presses the stub has made since the arena was allocated.

        The stub's own count, not ours, which is what makes it evidence: a caller that
        armed N times and reads back fewer knows the presses did not happen rather than
        assuming they did.
        """
        addr = self._at(AUTO_USE_COUNT_OFF)
        return (self.mem.read_i32(addr) or 0) if addr else 0


class OreQueue(_ArenaView):
    """The tiles the extractor stub will mine on its next frame."""

    @property
    def address(self) -> int | None:
        """Where the queue is, or None when there is no arena yet.

        A fixed offset in memory we allocated rather than the tail of a borrowed cave: an
        address, with no derivation from stub length and no risk of drifting away from
        what the stub reads.
        """
        return self._at(ORE_QUEUE_OFF)

    def armed(self) -> bool:
        """Is anything queued? Reports what *we* last set -- only this side writes it.

        Whether those tiles actually got mined is answered by looking at the tiles.
        """
        q = self.address
        return bool(q and self.mem.read_i32(q))

    def arm(self, tiles) -> int:
        """Queue up to :data:`ORE_MAX_BATCH` tiles. Returns how many were taken.

        The count is written **last**, so the game can never see a count covering
        coordinates that are only half written -- the stub would mine whatever garbage
        happened to be there, and mining the wrong tile cannot be undone.
        """
        q = self.address
        if q is None:
            return 0
        batch = list(tiles)[:ORE_MAX_BATCH]
        if not batch:
            return 0
        self.mem.write(q + 4, b"".join(struct.pack("<ii", int(x), int(y))
                                       for x, y in batch))
        self.mem.write(q, struct.pack("<i", len(batch)))
        return len(batch)

    def disarm(self) -> bool:
        """Stop the stub mining. A queue left armed is re-mined on every frame."""
        q = self.address
        if q is None:
            return False
        self.mem.write(q, struct.pack("<i", 0))
        return True
