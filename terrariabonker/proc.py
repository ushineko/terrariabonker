"""Live process memory access for the Terraria trainer.

Terraria runs as the 32-bit Windows build under Proton (wine-mono). On this box
``ptrace_scope=1`` blocks reading another process's ``/proc/<pid>/mem`` directly,
so anything that touches game memory has to run as root. ``elevate()`` re-execs
the whole program under ``sudo`` once, transparently.

Nothing here knows about Terraria's data structures; that lives in ``locate`` and
``player``. This module is just: find the game, read bytes, write bytes.
"""

from __future__ import annotations

import os
import struct
import sys

GAME_EXE = "Terraria.exe"


class ProcError(RuntimeError):
    """Raised when the game process cannot be found or its memory is unreadable."""


def elevate() -> None:
    """Re-exec this process under sudo if not already root.

    ptrace_scope=1 means a non-root process cannot open another process's
    ``/proc/<pid>/mem``. Re-running under sudo is the whole trick. This replaces
    the current process, so it never returns when elevation happens.
    """
    if os.geteuid() == 0:
        return
    entry = os.path.abspath(sys.argv[0])
    # -E keeps the environment so an interactive sudo prompt (if any) behaves,
    # and a NOPASSWD sudoers entry makes this seamless.
    os.execvp("sudo", ["sudo", "-E", sys.executable, entry, *sys.argv[1:]])


def find_pid() -> int:
    """Return the PID of the running Terraria game process.

    The game is the single process that maps ``Terraria.exe`` as executable
    (``r-xp``). The Proton wrapper scripts reference the same path in their
    command line but never map it executable, so this does not confuse them.
    """
    candidates = []
    for entry in os.listdir("/proc"):
        if not entry.isdigit():
            continue
        pid = int(entry)
        try:
            with open(f"/proc/{pid}/maps") as f:
                for line in f:
                    if GAME_EXE in line and "r-xp" in line and line.rstrip().endswith(GAME_EXE):
                        candidates.append(pid)
                        break
        except OSError:
            continue
    if not candidates:
        raise ProcError(
            f"no running {GAME_EXE} found. Is Terraria launched (Windows build under Proton)?"
        )
    if len(candidates) > 1:
        # Extremely unlikely, but be explicit rather than pick blindly.
        raise ProcError(f"multiple {GAME_EXE} processes found: {candidates}")
    return candidates[0]


class Mem:
    """Read/write access to one process's memory via ``/proc/<pid>/mem``."""

    def __init__(self, pid: int):
        self.pid = pid

    def exe_path(self) -> str | None:
        """Filesystem path of the mapped ``Terraria.exe``, or None."""
        try:
            with open(f"/proc/{self.pid}/maps") as f:
                for line in f:
                    if line.rstrip().endswith(GAME_EXE):
                        return line.split(None, 5)[5].rstrip()
        except OSError:
            return None
        return None

    def regions(self) -> list[tuple[int, int]]:
        """Writable, non-device memory regions as ``(start, end)`` pairs.

        The managed heap where Terraria's objects live is anonymous writable
        memory; device mappings (``/dev/nvidia*`` etc.) are skipped because they
        are not scannable RAM and reading them can stall or error.
        """
        out = []
        with open(f"/proc/{self.pid}/maps") as f:
            for line in f:
                parts = line.split()
                if "w" not in parts[1]:
                    continue
                path = parts[5] if len(parts) > 5 else ""
                if path.startswith("/dev/"):
                    continue
                a, b = parts[0].split("-")
                out.append((int(a, 16), int(b, 16)))
        return out

    def read(self, addr: int, size: int) -> bytes:
        try:
            with open(f"/proc/{self.pid}/mem", "rb", 0) as m:
                m.seek(addr)
                return m.read(size)
        except (OSError, ValueError):
            return b""

    def write(self, addr: int, data: bytes) -> bool:
        try:
            with open(f"/proc/{self.pid}/mem", "wb", 0) as m:
                m.seek(addr)
                m.write(data)
            return True
        except (OSError, ValueError):
            return False

    def read_i32(self, addr: int) -> int | None:
        raw = self.read(addr, 4)
        return struct.unpack("<i", raw)[0] if len(raw) == 4 else None

    def read_u32(self, addr: int) -> int | None:
        raw = self.read(addr, 4)
        return struct.unpack("<I", raw)[0] if len(raw) == 4 else None

    def read_f32(self, addr: int) -> float | None:
        raw = self.read(addr, 4)
        return struct.unpack("<f", raw)[0] if len(raw) == 4 else None

    def write_i32(self, addr: int, value: int) -> bool:
        return self.write(addr, struct.pack("<i", value))

    def write_f32(self, addr: int, value: float) -> bool:
        return self.write(addr, struct.pack("<f", value))
