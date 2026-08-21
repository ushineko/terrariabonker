"""terrariabonker control panel.

The window itself runs unprivileged. Every action that touches game memory is
run as a short-lived CLI invocation through QProcess with sudo, so the Qt app
never has to run as root (which is fraught on Wayland) and passwordless sudo
keeps it seamless. Freezes (godmode / infinite mana) are a single long-running
CLI process that is restarted whenever the toggles change.

QProcess is used rather than QThread per the repo convention: no worker object to
keep alive, no GC landmines, and the child is a real OS process we can signal.
"""

from __future__ import annotations

import json
import os
import sys

from PyQt6.QtCore import QProcess, Qt, QTimer
from PyQt6.QtWidgets import (QAbstractItemView, QApplication, QCheckBox, QCompleter,
                             QGridLayout, QGroupBox, QHBoxLayout, QHeaderView, QLabel,
                             QLineEdit, QPlainTextEdit, QPushButton, QSpinBox,
                             QTableWidget, QTableWidgetItem, QTabWidget, QVBoxLayout,
                             QWidget)

from terrariabonker import names

ENTRY = os.path.join(
    os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))),
    "terrariabonker.py",
)


def _cli_args(sub_args: list[str]) -> tuple[str, list[str]]:
    """Build the ('sudo', [...]) argv to run a CLI subcommand under sudo."""
    return "sudo", ["-E", sys.executable, ENTRY, *sub_args]


class MainWindow(QWidget):
    def __init__(self):
        super().__init__()
        self.setWindowTitle("terrariabonker")
        self._freeze: QProcess | None = None
        self._procs: set[QProcess] = set()   # keep refs so none is GC'd mid-run
        self._status_busy = False
        self._filling = False                # suppress cell-edit signals during fill
        self._rows: list[dict] = []          # rows currently shown in the table
        self._all_rows: list[dict] = []      # every slot (incl. empty), for slot-finding
        self._build()
        self._status_timer = QTimer(self)
        self._status_timer.timeout.connect(self.refresh_status)
        self._status_timer.start(2000)
        self.refresh_status()

    # --- layout ------------------------------------------------------------
    def _build(self):
        root = QVBoxLayout(self)

        self.status = QLabel("Locating Terraria…")
        self.status.setTextFormat(Qt.TextFormat.RichText)
        root.addWidget(self.status)

        tabs = QTabWidget()
        tabs.addTab(self._trainer_tab(), "Trainer")
        tabs.addTab(self._inventory_tab(), "Inventory")
        tabs.currentChanged.connect(
            lambda i: self.refresh_inventory() if i == 1 else None)
        root.addWidget(tabs, 1)

        self.log = QPlainTextEdit(readOnly=True)
        self.log.setMaximumBlockCount(500)
        self.log.setMaximumHeight(150)
        root.addWidget(self.log)

    def _trainer_tab(self) -> QWidget:
        w = QWidget()
        col = QVBoxLayout(w)

        freeze_box = QGroupBox("Freezes (held against the game)")
        fb = QHBoxLayout(freeze_box)
        self.cb_god = QCheckBox("Godmode (pin HP)")
        self.cb_mana = QCheckBox("Infinite mana")
        self.cb_god.toggled.connect(self._restart_freeze)
        self.cb_mana.toggled.connect(self._restart_freeze)
        fb.addWidget(self.cb_god)
        fb.addWidget(self.cb_mana)
        fb.addStretch()
        col.addWidget(freeze_box)

        stat_box = QGroupBox("Stats")
        sg = QGridLayout(stat_box)
        sg.addWidget(self._btn("Heal to full", ["set-hp", "max"]), 0, 0)
        sg.addWidget(self._btn("Refill mana", ["set-mana", "max"]), 0, 1)
        self.sp_maxhp = self._spin(100, 9999, 400)
        self.sp_maxmana = self._spin(20, 400, 200)
        sg.addWidget(QLabel("Max HP"), 1, 0)
        sg.addWidget(self.sp_maxhp, 1, 1)
        sg.addWidget(self._btn("Set", lambda: ["set-max-hp", str(self.sp_maxhp.value())]), 1, 2)
        sg.addWidget(QLabel("Max mana"), 2, 0)
        sg.addWidget(self.sp_maxmana, 2, 1)
        sg.addWidget(self._btn("Set", lambda: ["set-max-mana", str(self.sp_maxmana.value())]), 2, 2)
        col.addWidget(stat_box)

        tool_box = QGroupBox("Tools")
        tg = QHBoxLayout(tool_box)
        tg.addWidget(self._btn("Fast mining (all pickaxes)", ["fast-mining"]))
        self.sp_reach = self._spin(1, 100, 20)
        tg.addWidget(QLabel("Reach +"))
        tg.addWidget(self.sp_reach)
        tg.addWidget(self._btn("Long reach", lambda: ["long-reach", "--tiles", str(self.sp_reach.value())]))
        col.addWidget(tool_box)
        col.addStretch()
        return w

    INV_COLS = ["Slot", "ID", "Name", "Stack", "Dmg", "Auto", "useTime", "Pick"]

    def _inventory_tab(self) -> QWidget:
        w = QWidget()
        col = QVBoxLayout(w)

        # Give-item browser: type a name (autocompleted) or an ItemID number.
        give = QGroupBox("Give item")
        gg = QHBoxLayout(give)
        self.give_search = QLineEdit(placeholderText="item name or ItemID…")
        comp = QCompleter(sorted(n for n in names._NAMES.values()))
        comp.setCaseSensitivity(Qt.CaseSensitivity.CaseInsensitive)
        comp.setFilterMode(Qt.MatchFlag.MatchContains)
        self.give_search.setCompleter(comp)
        self.sp_gstack = self._spin(1, 9999, 999)
        gg.addWidget(self.give_search, 1)
        gg.addWidget(QLabel("×"))
        gg.addWidget(self.sp_gstack)
        gg.addWidget(QLabel("→ first empty slot"))
        give_btn = QPushButton("Give")
        give_btn.clicked.connect(self.give_item)
        self.give_search.returnPressed.connect(self.give_item)
        gg.addWidget(give_btn)
        col.addWidget(give)

        bar = QHBoxLayout()
        bar.addWidget(self._btn2("Refresh", self.refresh_inventory))
        self.cb_empty = QCheckBox("show empty slots")
        self.cb_empty.toggled.connect(self.refresh_inventory)
        bar.addWidget(self.cb_empty)
        bar.addStretch()
        col.addLayout(bar)

        self.table = QTableWidget(0, len(self.INV_COLS))
        self.table.setHorizontalHeaderLabels(self.INV_COLS)
        self.table.verticalHeader().setVisible(False)
        self.table.setEditTriggers(QAbstractItemView.EditTrigger.DoubleClicked)
        self.table.horizontalHeader().setSectionResizeMode(
            2, QHeaderView.ResizeMode.Stretch)   # Name column stretches
        self.table.itemChanged.connect(self._on_cell_edited)
        col.addWidget(self.table, 1)
        col.addWidget(QLabel("<i>Double-click Stack / ID / Dmg to edit. "
                             "Auto: 1 = auto-swing.</i>"))
        return w

    def _spin(self, lo, hi, val):
        s = QSpinBox()
        s.setRange(lo, hi)
        s.setValue(val)
        return s

    def _btn(self, label, args):
        b = QPushButton(label)
        b.clicked.connect(lambda: self._run(args() if callable(args) else args))
        return b

    def _btn2(self, label, slot):
        b = QPushButton(label)
        b.clicked.connect(slot)
        return b

    # --- process plumbing --------------------------------------------------
    def _spawn(self, sub_args: list[str], on_output=None) -> QProcess:
        """Start a CLI subcommand under sudo, tracking it so it can't be GC'd
        mid-run (the cause of one-shot actions silently doing nothing)."""
        proc = QProcess(self)
        self._procs.add(proc)
        proc.setProcessChannelMode(QProcess.ProcessChannelMode.MergedChannels)

        def _finished(*_):
            try:
                out = bytes(proc.readAllStandardOutput()).decode(errors="replace")
            except RuntimeError:
                return                       # object already torn down
            if on_output:
                on_output(out)
            self._procs.discard(proc)
            proc.deleteLater()

        proc.finished.connect(_finished)
        prog, argv = _cli_args(sub_args)
        proc.start(prog, argv)
        return proc

    def _run(self, sub_args: list[str]):
        """One-shot action: run it, log the output, then refresh status."""
        self.log.appendPlainText(f"$ terrariabonker {' '.join(sub_args)}")

        def done(out):
            if out.strip():
                self.log.appendPlainText(out.rstrip())
            self.refresh_status()

        self._spawn(sub_args, on_output=done)

    def _restart_freeze(self):
        if self._freeze is not None:
            self._freeze.terminate()
            if not self._freeze.waitForFinished(1000):
                self._freeze.kill()
                self._freeze.waitForFinished(500)
            self._procs.discard(self._freeze)
            self._freeze = None
        flags = []
        if self.cb_god.isChecked():
            flags.append("--godmode")
        if self.cb_mana.isChecked():
            flags.append("--mana")
        if not flags:
            self.log.appendPlainText("[freeze stopped]")
            return
        self._freeze = QProcess(self)
        self._procs.add(self._freeze)
        prog, argv = _cli_args(["freeze", *flags])
        self._freeze.start(prog, argv)
        self.log.appendPlainText(f"[freeze started: {' '.join(flags)}]")

    def refresh_status(self):
        if self._status_busy:
            return                           # don't stack overlapping status scans
        self._status_busy = True

        def done(out):
            self._status_busy = False
            self._render_status(out)

        self._spawn(["status", "--json"], on_output=done)

    def _render_status(self, raw: str):
        try:
            d = json.loads(raw.strip().splitlines()[-1])
        except (ValueError, IndexError):
            self.status.setText("<b>Terraria not found</b> — is it running under Proton?")
            return
        god = " · <span style='color:#d33'>GODMODE</span>" if self.cb_god.isChecked() else ""
        self.status.setText(
            f"<b>{d.get('name')}</b> — HP {d.get('hp')}/{d.get('max_hp')} · "
            f"Mana {d.get('mana')}/{d.get('max_mana')} · "
            f"PID {d.get('pid')} · Terraria {d.get('version')}{god}")

    # --- inventory tab -----------------------------------------------------
    def refresh_inventory(self):
        args = ["inventory", "--json"]
        self._spawn(args, on_output=self._fill_table)

    def _fill_table(self, raw: str):
        try:
            all_rows = json.loads(raw.strip().splitlines()[-1])
        except (ValueError, IndexError):
            return
        self._all_rows = all_rows
        rows = all_rows if self.cb_empty.isChecked() else [r for r in all_rows if r["type"] != 0]
        self._filling = True
        self._rows = rows
        self.table.setRowCount(len(rows))
        RO = Qt.ItemFlag.ItemIsSelectable | Qt.ItemFlag.ItemIsEnabled
        EDIT = RO | Qt.ItemFlag.ItemIsEditable
        for r, d in enumerate(rows):
            vals = [d["slot"], d["type"], names.label(d["type"]), d["stack"],
                    d["damage"], d["auto_reuse"], d["use_time"], d["pick"]]
            editable = {1, 3, 4, 5}      # ID, Stack, Dmg, Auto
            for c, v in enumerate(vals):
                it = QTableWidgetItem(str(v))
                it.setFlags(EDIT if c in editable else RO)
                self.table.setItem(r, c, it)
        self._filling = False

    def _on_cell_edited(self, item):
        if self._filling:
            return
        row, col = item.row(), item.column()
        d = self._rows[row]
        slot = d["slot"]
        try:
            val = int(item.text())
        except ValueError:
            self.refresh_inventory()
            return
        if col == 3:                                     # Stack
            self._run(["set-stack", str(slot), str(val)])
        elif col == 1:                                   # ItemID
            self._run(["set-item", str(slot), str(val), "--stack", str(d["stack"] or 1)])
        elif col == 4:                                   # Damage
            self._run(["set-item", str(slot), str(d["type"]), "--damage", str(val)])
        elif col == 5:                                   # autoReuse
            self._run(["set-item", str(slot), str(d["type"]), "--auto-reuse", "1" if val else "0"])
        QTimer.singleShot(600, self.refresh_inventory)

    # inventory slots the game treats as the main grid for "give" (0-49);
    # avoids dropping items into coin/ammo/equip slots.
    GIVE_RANGE = range(0, 50)

    def give_item(self):
        text = self.give_search.text().strip()
        if not text:
            return
        if text.isdigit():
            item_id = int(text)
        else:
            hits = names.search(text, limit=1)
            if not hits:
                self.log.appendPlainText(f"[no item matches '{text}']")
                return
            item_id, matched = hits[0]
            self.log.appendPlainText(f"['{text}' → {matched} (#{item_id})]")
        by_slot = {r["slot"]: r for r in self._all_rows}
        empty = next((s for s in self.GIVE_RANGE
                      if s in by_slot and by_slot[s]["type"] == 0), None)
        if empty is None:
            self.log.appendPlainText("[inventory full — no empty slot; "
                                     "double-click a slot's ID to overwrite instead]")
            return
        self._run(["set-item", str(empty), str(item_id),
                   "--stack", str(self.sp_gstack.value())])
        QTimer.singleShot(600, self.refresh_inventory)

    def closeEvent(self, event):
        self._status_timer.stop()
        for proc in list(self._procs):
            try:
                proc.terminate()
                if not proc.waitForFinished(800):
                    proc.kill()
                    proc.waitForFinished(400)
            except RuntimeError:
                pass
        self._procs.clear()
        event.accept()


def run() -> int:
    app = QApplication(sys.argv)
    app.setDesktopFileName("terrariabonker")
    w = MainWindow()
    w.resize(560, 620)
    w.show()
    return app.exec()
