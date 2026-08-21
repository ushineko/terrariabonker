"""Modal per-item editor for the grid inventory.

Opened by clicking a cell. Prefilled from the slot's current values (or blank for
an empty slot, in which case picking an item places a fully-statted one there). All
mapped Item properties are editable here; the dialog only collects values — the main
window issues the actual ``set-item`` through the CLI client.
"""

from __future__ import annotations

from PyQt6.QtCore import Qt
from PyQt6.QtWidgets import (QCheckBox, QCompleter, QDialog, QDialogButtonBox,
                             QFormLayout, QLineEdit, QMessageBox, QPushButton,
                             QSpinBox)

from terrariabonker import names


class ItemEditDialog(QDialog):
    """Edit one inventory slot. ``resolved`` holds the outcome after exec():
    a dict of set-item kwargs (with ``type``), or None on cancel. ``cleared`` is
    True when the user chose to empty the slot."""

    def __init__(self, parent, row: dict, completer_names: list[str]):
        super().__init__(parent)
        self.slot = int(row.get("slot"))
        self._orig_type = int(row.get("type", 0))
        empty = self._orig_type == 0
        self.resolved: dict | None = None
        self.cleared = False

        self.setWindowTitle(
            f"Slot {self.slot} — "
            + ("place item" if empty else names.label(self._orig_type)))
        form = QFormLayout(self)

        self.item = QLineEdit()
        if not empty:
            self.item.setText(names.label(self._orig_type))
        self.item.setPlaceholderText("item name or ItemID…")
        comp = QCompleter(completer_names, self)
        comp.setCaseSensitivity(Qt.CaseSensitivity.CaseInsensitive)
        comp.setFilterMode(Qt.MatchFlag.MatchContains)
        self.item.setCompleter(comp)
        form.addRow("Item", self.item)

        self.stack = self._spin(0, 9999, int(row.get("stack") or 1))
        self.damage = self._spin(-1, 999999, int(row.get("damage", -1)))
        self.auto = QCheckBox("auto-swing while held")
        self.auto.setChecked(bool(row.get("auto_reuse")))
        ut = int(row.get("use_time") or 20)
        self.use_time = self._spin(1, 255, ut)
        self.use_anim = self._spin(1, 255, int(row.get("use_anim") or ut))
        self.pick = self._spin(0, 2000, int(row.get("pick", 0)))
        self.tile = self._spin(0, 100, int(row.get("tile_boost", 0)))
        self.defense = self._spin(0, 9999, int(row.get("defense", 0)))
        self.prefix = self._spin(0, 255, int(row.get("prefix", 0)))

        form.addRow("Stack", self.stack)
        form.addRow("Damage", self.damage)
        form.addRow("Defense", self.defense)
        form.addRow("Prefix (modifier tier)", self.prefix)
        form.addRow("Auto-reuse", self.auto)
        form.addRow("Use time (lower = faster)", self.use_time)
        form.addRow("Use animation", self.use_anim)
        form.addRow("Pickaxe power %", self.pick)
        form.addRow("Placement reach (+tiles)", self.tile)

        buttons = QDialogButtonBox(QDialogButtonBox.StandardButton.Ok
                                   | QDialogButtonBox.StandardButton.Cancel)
        buttons.accepted.connect(self._on_ok)
        buttons.rejected.connect(self.reject)
        if not empty:
            clear = QPushButton("Clear slot")
            clear.clicked.connect(self._on_clear)
            buttons.addButton(clear, QDialogButtonBox.ButtonRole.DestructiveRole)
        form.addRow(buttons)

    @staticmethod
    def _spin(lo: int, hi: int, val: int) -> QSpinBox:
        s = QSpinBox()
        s.setRange(lo, hi)
        s.setValue(max(lo, min(hi, val)))
        return s

    def _resolve_type(self) -> int | None:
        text = self.item.text().strip()
        if not text:
            return None
        if text.isdigit():
            return int(text)
        hits = names.search(text, limit=1)
        return hits[0][0] if hits else None

    def _on_ok(self):
        item_type = self._resolve_type()
        if item_type is None:
            QMessageBox.warning(self, "Unknown item",
                                "Enter a known item name or a numeric ItemID.")
            return
        self.resolved = {
            "type": item_type,
            "stack": self.stack.value(),
            "damage": self.damage.value(),
            "auto_reuse": 1 if self.auto.isChecked() else 0,
            "use_time": self.use_time.value(),
            "use_anim": self.use_anim.value(),
            "pick": self.pick.value(),
            "tile_boost": self.tile.value(),
            "defense": self.defense.value(),
            "prefix": self.prefix.value(),
        }
        self.accept()

    def _on_clear(self):
        self.cleared = True
        self.accept()
