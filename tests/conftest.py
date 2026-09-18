"""Put the project root on sys.path and provide an in-memory fake process.

The FakeMem backs the same read/write API as proc.Mem with a plain bytearray at
a chosen base address, so the locator and player logic can be tested without a
running game or root.
"""

import os
import struct
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

# Headless Qt, set before anything imports PyQt6. Four test modules used to set this for
# themselves and three others that build real widgets did not -- those passed only because
# another module's import happened to run first during collection, so running one of them
# alone on a headless box was a coin flip.
os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

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


# --- Qt ---------------------------------------------------------------------
# One application and one window builder for the whole suite. These were copied into
# seven and four files respectively, under two different fixture names, which is how the
# headless setting came to be missing from half of them.

@pytest.fixture
def qt_app():
    """The one QApplication. Qt allows a single instance per process."""
    from PyQt6.QtWidgets import QApplication

    yield QApplication.instance() or QApplication([])




# --- keep the suite out of the user's real state ------------------------------

@pytest.fixture(autouse=True)
def _isolate_user_state(tmp_path, monkeypatch):
    """Point every on-disk state file at a tmp copy, for every test.

    A test that wants to drive one of these still monkeypatches it itself; this only makes
    the default harmless. `~/.config` is worse than untidy: it can be root-owned from sudo
    memory commands, so a stray write there fails in a way that has nothing to do with the
    test that caused it (which is how one build-ledger test came to fail with
    PermissionError).

    The window's own state used to be here too, when the panel was PyQt6 and the suite
    could build one. The Go window keeps its settings through the shell's store and its
    tests sandbox the home directory themselves.
    """
    from terrariabonker import builds, profile

    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    monkeypatch.setattr(builds, "_PATH", str(tmp_path / "accepted-builds.json"))
