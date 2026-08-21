"""Put the project root on sys.path and provide an in-memory fake process.

The FakeMem backs the same read/write API as proc.Mem with a plain bytearray at
a chosen base address, so the locator and player logic can be tested without a
running game or root.
"""

import os
import struct
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from terrariabonker.proc import Mem  # noqa: E402


class FakeMem(Mem):
    def __init__(self, base: int, size: int):
        super().__init__(pid=-1)
        self.base = base
        self.buf = bytearray(size)

    def regions(self):
        return [(self.base, self.base + len(self.buf))]

    def _slice(self, addr, size):
        lo = addr - self.base
        if lo < 0 or lo + size > len(self.buf):
            return None
        return lo

    def read(self, addr, size):
        lo = self._slice(addr, size)
        return bytes(self.buf[lo: lo + size]) if lo is not None else b""

    def write(self, addr, data):
        lo = self._slice(addr, len(data))
        if lo is None:
            return False
        self.buf[lo: lo + len(data)] = data
        return True

    # --- helpers for tests to plant structures --------------------------
    def poke_i32(self, addr, value):
        self.write(addr, struct.pack("<i", value))

    def poke_bytes(self, addr, data):
        self.write(addr, data)

    def plant_mono_string(self, addr, text):
        """Write a 32-bit mono String (vtable, sync, length, UTF-16) at addr."""
        chars = text.encode("utf-16-le")
        self.poke_bytes(addr, struct.pack("<II i", 0xDEADBEEF, 0, len(text)) + chars)

    def plant_player(self, life_addr, block, name_ptr):
        """Write a life/mana block at life_addr and a name pointer at -0x6C0."""
        for i, v in enumerate(block):
            self.poke_i32(life_addr - 0x08 + i * 4, v)
        self.write(life_addr - 0x6C0, struct.pack("<I", name_ptr))
