"""The dialog shown when the game is a build we do not recognise (spec 036).

Terraria updated from 1.4.5.7 to 1.4.5.8 while the panel was running and the panel said
nothing, because an unrecognised build only ever produced a small amber banner. The AOB
patterns are derived against one exact build, so an update has three possible outcomes —
everything still matches, some of it does, or none of it — and the user could not tell
which. This asks.

The dialog is deliberately dumb: it is handed a finished check and returns a decision.
Everything privileged happens before it opens, which is what makes it testable.
"""

from __future__ import annotations

from PyQt6.QtCore import Qt
from PyQt6.QtWidgets import QDialog, QHBoxLayout, QLabel, QPushButton, QVBoxLayout

ACCEPT = "accepted"      # every cheat resolved; remember the build and carry on
CONTINUE = "degraded"    # some did not; carry on with those disabled
EXIT = "exit"


class BuildGateDialog(QDialog):
    """``result_decision`` is ACCEPT, CONTINUE or EXIT once this closes."""

    def __init__(self, parent, check: dict, known_build: str):
        super().__init__(parent)
        self.check = check
        self.result_decision = EXIT          # closing by the window button is not consent
        failed = list(check.get("failed") or ())
        total = len(check.get("cheats") or {})
        self.setWindowTitle("Terraria has updated")
        self.setModal(True)
        # Wide enough that a cheat's reason wraps to two lines rather than twenty, and
        # free to grow downwards: the list of dead cheats has no fixed length.
        self.setMinimumWidth(560)

        col = QVBoxLayout(self)
        head = QLabel(
            f"<b>This is not a build terrariabonker knows.</b>"
            f"<p>Running: <code>{check.get('build')}</code><br>"
            f"Known&nbsp;good: <code>{known_build}</code></p>")
        head.setTextFormat(Qt.TextFormat.RichText)
        head.setWordWrap(True)
        col.addWidget(head)

        if not failed:
            body = QLabel(
                f"All {total} cheats still match their patterns on this build, so the "
                "update did not touch the code they patch.<p>Accepting records this build "
                "so you are not asked again. Note this is weaker than the project having "
                "verified it: it means the patterns still match, not that anyone has "
                "confirmed each cheat in play.</p>")
        else:
            names = "<br>".join(
                f"&nbsp;&nbsp;• <b>{n}</b> — "
                f"{(check['cheats'][n].get('reason') or 'no match on this build')}"
                for n in failed)
            body = QLabel(
                f"<b>{len(failed)} of {total} cheats no longer match</b> on this build:"
                f"<p>{names}</p>"
                "<p>You can carry on without them — they will be disabled and greyed out "
                "— or exit and leave the game alone.</p>")
        body.setTextFormat(Qt.TextFormat.RichText)
        body.setWordWrap(True)
        col.addWidget(body)

        row = QHBoxLayout()
        row.addStretch(1)
        if not failed:
            self.btn_ok = QPushButton("Accept this build")
            self.btn_ok.setDefault(True)
            self.btn_ok.clicked.connect(lambda: self._finish(ACCEPT))
        else:
            self.btn_ok = QPushButton(f"Continue without {len(failed)}")
            self.btn_ok.setDefault(True)
            self.btn_ok.clicked.connect(lambda: self._finish(CONTINUE))
        row.addWidget(self.btn_ok)
        self.btn_exit = QPushButton("Exit")
        self.btn_exit.clicked.connect(lambda: self._finish(EXIT))
        row.addWidget(self.btn_exit)
        col.addLayout(row)

        col.addStretch(1)
        self.adjustSize()

    def _finish(self, decision: str) -> None:
        self.result_decision = decision
        self.accept() if decision != EXIT else self.reject()
