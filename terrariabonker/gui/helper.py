"""The GUI's long-lived privileged worker (the other end of ``cli.cmd_serve``).

Locating the player is ~99% of a read's cost — a full memory scan — and a one-shot
CLI run pays it every time, so an ``inventory --all --json`` round trip costs ~2.7 s.
Keeping one ``terrariabonker serve`` process under sudo and sending it JSON lines
brings that to ~3 ms, which is what makes a 1 Hz inventory sync affordable.

Driven entirely from the Qt event loop: requests are queued by id, replies arrive in
``readyReadStandardOutput``, and callbacks fire from there. No threads. If the worker
cannot start, dies, or refuses a command, callers fall back to spawning the CLI
per action exactly as before.
"""

from __future__ import annotations

import json

from PyQt6.QtCore import QObject, QProcess


class Helper(QObject):
    """One ``serve`` process, or nothing. ``available`` says which."""

    def __init__(self, parent, prog: str, argv: list[str], on_note=None):
        super().__init__(parent)
        self._prog = prog
        self._argv = argv
        self._note = on_note or (lambda _msg: None)
        self._proc: QProcess | None = None
        self._pending: dict[int, object] = {}
        self._next_id = 1
        self._buf = ""
        self.available = False

    # --- lifecycle ---------------------------------------------------------
    def start(self) -> None:
        proc = QProcess(self)
        proc.setProcessChannelMode(QProcess.ProcessChannelMode.SeparateChannels)
        proc.readyReadStandardOutput.connect(self._on_stdout)
        proc.readyReadStandardError.connect(self._on_stderr)
        proc.finished.connect(self._on_finished)
        proc.errorOccurred.connect(lambda _e: self._fail("worker failed to start"))
        proc.start(self._prog, self._argv)
        self._proc = proc
        # Optimistic: the process is up. If sudo or the worker rejects us it exits
        # promptly and _on_finished flips this back, so callers fall back then.
        self.available = True

    def stop(self) -> None:
        """Close stdin so the worker sees EOF and exits; kill only if it lingers."""
        self.available = False
        proc, self._proc = self._proc, None
        if proc is None:
            return
        try:
            proc.closeWriteChannel()
            if not proc.waitForFinished(800):
                proc.kill()
                proc.waitForFinished(400)
        except RuntimeError:
            pass

    def _fail(self, why: str) -> None:
        if self.available:
            self._note(f"[helper unavailable: {why} — falling back to one-shot commands]")
        self.available = False
        for cb in list(self._pending.values()):
            cb(f"[ERROR] {why}")
        self._pending.clear()

    def _on_finished(self, *_args) -> None:
        self._fail("worker exited")

    # --- protocol ----------------------------------------------------------
    def request(self, sub_args: list[str], on_output) -> bool:
        """Queue one request. False means "not served" — the caller should spawn."""
        if not self.available or self._proc is None:
            return False
        rid = self._next_id
        self._next_id += 1
        line = json.dumps({"id": rid, "argv": list(sub_args)}) + "\n"
        self._pending[rid] = on_output
        if self._proc.write(line.encode()) <= 0:
            self._pending.pop(rid, None)
            return False
        return True

    def _on_stdout(self) -> None:
        try:
            self._buf += bytes(self._proc.readAllStandardOutput()).decode(errors="replace")
        except (RuntimeError, AttributeError):
            return
        *lines, self._buf = self._buf.split("\n")
        for ln in lines:
            if ln.strip():
                self._dispatch(ln)

    def _dispatch(self, line: str) -> None:
        try:
            resp = json.loads(line)
            rid = resp.get("id")
            out = resp.get("out", "")
        except ValueError:
            return                              # not our protocol; ignore the line
        cb = self._pending.pop(rid, None)
        if cb is not None:
            cb(out)

    def _on_stderr(self) -> None:
        """sudo's complaints land here; keep them out of the JSON stream."""
        try:
            msg = bytes(self._proc.readAllStandardError()).decode(errors="replace").strip()
        except (RuntimeError, AttributeError):
            return
        if msg:
            self._note(f"[helper] {msg}")
