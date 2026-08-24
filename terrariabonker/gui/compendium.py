"""The Compendium tab: browse every item and NPC in the game.

Reuses what the recipe browser already established — an icon grid over a
`QStandardItemModel` behind a filter proxy, and the shared sprite cache — rather than
growing a second grid implementation. The catalog itself comes from the `compendium` CLI
command, which reads the game's own item templates (see `terrariabonker.content`).

The tab owns no privileged access: it is handed callbacks for fetching the catalog, giving
an item and logging, so everything that touches memory still goes through the CLI boundary.
"""

from __future__ import annotations

from PyQt6.QtCore import QProcess, QSize, QSortFilterProxyModel, Qt, QTimer
from PyQt6.QtGui import QKeySequence, QShortcut, QStandardItem, QStandardItemModel
from PyQt6.QtWidgets import (QAbstractItemView, QComboBox, QDialog, QHBoxLayout, QHeaderView,
                             QLabel, QLineEdit, QMessageBox, QPushButton, QSpinBox,
                             QTreeView, QVBoxLayout, QWidget)

from terrariabonker.gui import uitext

ROLE_ID = int(Qt.ItemDataRole.UserRole)
ROLE_SEARCH = int(Qt.ItemDataRole.UserRole) + 1
ROLE_KIND = int(Qt.ItemDataRole.UserRole) + 2
ROLE_ENTRY = int(Qt.ItemDataRole.UserRole) + 3
ROLE_SORT = int(Qt.ItemDataRole.UserRole) + 4     # sort ids numerically, names by text

ALL_KINDS = "All kinds"

# Spawning a boss is the one destructive thing this tab can do, so it is gated twice: a
# confirmation naming it, then a countdown the user can still cancel. A misclick in a
# 6,958-row list must not be able to end a character.
BOSS_KIND = "Boss"
BOSS_COUNTDOWN = 5              # seconds
DEFAULT_DISTANCE = 25           # tiles behind the player

# Sortable stat columns, so the catalog can be ranked by what it is rather than only named.
# Each is (header, stats key); a value of 0 or less shows blank but still sorts by its real
# number, so "no damage" groups together instead of scattering.
STAT_COLUMNS: tuple[tuple[str, str], ...] = (
    ("Damage", "damage"),
    ("Defense", "defense"),
    ("Life", "life"),           # NPCs only; items carry no life and show blank
    ("Rarity", "rare"),         # items only
)
COLUMNS = ["Name", "Kind"] + [h for h, _k in STAT_COLUMNS] + ["ID"]
ID_COLUMN = len(COLUMNS) - 1


class _Filter(QSortFilterProxyModel):
    """Substring over "name #id", optionally narrowed to one kind."""

    def __init__(self, parent=None):
        super().__init__(parent)
        self._q = ""
        self._kind = ALL_KINDS

    def set_query(self, q: str) -> None:
        self._q = q.strip().lower()
        self.invalidateFilter()

    def set_kind(self, kind: str) -> None:
        self._kind = kind
        self.invalidateFilter()

    def filterAcceptsRow(self, row, parent) -> bool:
        idx = self.sourceModel().index(row, 0, parent)
        if self._kind != ALL_KINDS and idx.data(ROLE_KIND) != self._kind:
            return False
        return not self._q or self._q in (idx.data(ROLE_SEARCH) or "")


class EntryDialog(QDialog):
    """One entry in detail: what it is, what the game says about it, and what we can do."""

    def __init__(self, parent, entry: dict, icon, on_give, on_spawn=None):
        super().__init__(parent)
        self.entry = entry
        self.spawn_distance = None
        name = entry.get("name", "?")
        self.setWindowTitle(name)
        col = QVBoxLayout(self)

        head = QHBoxLayout()
        if icon is not None:
            pic = QLabel()
            pic.setPixmap(icon.pixmap(QSize(40, 40)))
            head.addWidget(pic)
        title = QLabel(f"<b>{name}</b>  <span style='color:gray'>#{entry.get('id')} · "
                       f"{entry.get('kind')}</span>")
        title.setTextFormat(Qt.TextFormat.RichText)
        head.addWidget(title, 1)
        col.addLayout(head)

        tip = entry.get("tooltip") or ""
        if tip:
            lab = QLabel(tip)
            lab.setWordWrap(True)
            lab.setStyleSheet("color: #cfc9a8;")
            col.addWidget(lab)

        stats = entry.get("stats") or {}
        shown = [("Life", stats.get("life")), ("Damage", stats.get("damage")),
                 ("Defense", stats.get("defense")), ("Rarity", stats.get("rare")),
                 ("Use time", stats.get("use_time")), ("Pickaxe", stats.get("pick"))]
        line = "  ·  ".join(f"{k} {v}" for k, v in shown if isinstance(v, int) and v > 0)
        if line:
            s = QLabel(line)
            s.setStyleSheet("color: gray;")
            col.addWidget(s)

        row = QHBoxLayout()
        wiki = QPushButton("Wiki ↗")
        wiki.setToolTip(entry.get("wiki", ""))
        wiki.clicked.connect(lambda: self._open(entry.get("wiki", "")))
        row.addWidget(wiki)
        if not entry.get("npc"):
            give = QPushButton("Give")
            give.setToolTip("Put one in the first empty inventory slot")
            give.clicked.connect(lambda: (on_give(int(entry["id"])), self.accept()))
            row.addWidget(give)
        elif on_spawn is not None:
            row.addWidget(QLabel("Distance"))
            self.spawn_distance = QSpinBox()
            self.spawn_distance.setRange(0, 200)
            self.spawn_distance.setValue(DEFAULT_DISTANCE)
            self.spawn_distance.setSuffix(" tiles")
            self.spawn_distance.setToolTip(
                "How far behind you it appears. Around 50 clears the screen.")
            row.addWidget(self.spawn_distance)
            spawn = QPushButton("Spawn")
            if entry.get("kind") == BOSS_KIND:
                spawn.setText("Spawn boss…")
                spawn.setToolTip("Asks for confirmation, then counts down before spawning")
            spawn.clicked.connect(
                lambda: (on_spawn(entry, self.spawn_distance.value()), self.accept()))
            row.addWidget(spawn)
        close = QPushButton("Close")
        close.clicked.connect(self.reject)
        row.addWidget(close)
        col.addLayout(row)

    @staticmethod
    def _open(url: str) -> None:
        """Hand the URL to the browser. The app never fetches anything itself."""
        if url:
            QProcess.startDetached("xdg-open", [url])


class CompendiumTab(QWidget):
    """Browse every item and NPC. ``fetch`` is called once, lazily, with a callback."""

    def __init__(self, parent, fetch, on_give, icon_for, log, on_spawn=None):
        super().__init__(parent)
        self._fetch = fetch
        self._on_give = on_give
        self._on_spawn = on_spawn
        self._icon_for = icon_for
        self._log = log
        self._loaded = False
        self._countdown = None          # QTimer while a boss spawn is pending
        self._pending = None            # (entry, distance) it will fire with
        self._build()

    def _build(self) -> None:
        col = QVBoxLayout(self)
        bar = QHBoxLayout()
        self.kind = QComboBox()
        # The catalog arrives after the tab is first shown, and Qt's default policy sizes
        # a combo to its contents *on first show only* — so it stayed as wide as
        # "All kinds" and truncated every real kind that turned up later.
        self.kind.setSizeAdjustPolicy(QComboBox.SizeAdjustPolicy.AdjustToContents)
        self.kind.addItem(ALL_KINDS)
        self.kind.currentTextChanged.connect(lambda k: self._proxy.set_kind(k))
        bar.addWidget(self.kind)
        self.search = QLineEdit(placeholderText="filter by name or ID…")
        self.search.textChanged.connect(lambda t: (self._proxy.set_query(t), self._count()))
        bar.addWidget(self.search, 1)
        col.addLayout(bar)

        # A list with columns, not an icon grid: 6,958 entries are browsed by name, and a
        # grid cell cannot show a full name plus its kind without clipping one of them.
        self.view = QTreeView()
        self.view.setRootIsDecorated(False)
        self.view.setAlternatingRowColors(True)
        self.view.setUniformRowHeights(True)
        self.view.setSortingEnabled(True)
        self.view.setIconSize(QSize(28, 28))
        self.view.setSelectionBehavior(QAbstractItemView.SelectionBehavior.SelectRows)
        self.view.setEditTriggers(QAbstractItemView.EditTrigger.NoEditTriggers)
        # ONLY doubleClicked. `activated` also fires for a double-click on this style, and
        # a re-entrancy guard does not help: the dialog runs a nested event loop, so the
        # second signal is delivered *after* it closes and opens another one. Enter is
        # wired separately below so keyboard use still works.
        self.view.doubleClicked.connect(self._open_entry)
        self._opening = False
        self._model = QStandardItemModel(self)
        self._model.setHorizontalHeaderLabels(COLUMNS)
        self._proxy = _Filter(self)
        self._proxy.setSourceModel(self._model)
        self._proxy.setSortRole(ROLE_SORT)
        self.view.setModel(self._proxy)
        head = self.view.header()
        head.setSectionResizeMode(0, QHeaderView.ResizeMode.Stretch)
        for c in range(1, len(COLUMNS)):
            head.setSectionResizeMode(c, QHeaderView.ResizeMode.ResizeToContents)
        # Off by default it is not: the last section stretches, which gave ID the whole
        # slack and left its header stranded far from its right-aligned values. Name is the
        # stretching column instead.
        head.setStretchLastSection(False)
        # Numeric headers sit over right-aligned values, so align them the same way.
        for c in range(2, len(COLUMNS)):
            self._model.setHeaderData(c, Qt.Orientation.Horizontal,
                                      Qt.AlignmentFlag.AlignRight
                                      | Qt.AlignmentFlag.AlignVCenter,
                                      Qt.ItemDataRole.TextAlignmentRole)
        self.view.sortByColumn(0, Qt.SortOrder.AscendingOrder)
        for seq in ("Return", "Enter"):
            sc = QShortcut(QKeySequence(seq), self.view)
            sc.setContext(Qt.ShortcutContext.WidgetShortcut)
            sc.activated.connect(lambda: self._open_entry(self.view.currentIndex()))
        col.addWidget(self.view, 1)

        # The boss countdown lives here rather than in a modal: a modal the user has to
        # keep open to cancel is worse than a line they can cancel from at any moment.
        self.gate = QWidget()
        gate_row = QHBoxLayout(self.gate)
        gate_row.setContentsMargins(0, 0, 0, 0)
        self.gate_label = QLabel()
        gate_row.addWidget(self.gate_label, 1)
        self.gate_cancel = QPushButton("Cancel")
        self.gate_cancel.clicked.connect(self.cancel_spawn)
        gate_row.addWidget(self.gate_cancel)
        self.gate.setVisible(False)
        col.addWidget(self.gate)

        self.status = QLabel("Open this tab to load the catalog…")
        col.addWidget(self.status)

    # --- data ---------------------------------------------------------------
    def ensure_loaded(self) -> None:
        """Called when the tab is first shown; the scan behind it takes a second or two."""
        if self._loaded:
            return
        self._loaded = True
        self.status.setText("Reading the game's item templates…")
        self._fetch(self._fill)

    def _fill(self, catalog: dict | None) -> None:
        if not catalog:
            self._loaded = False        # let a later visit retry
            self.status.setText("Could not read the catalog — is the game running?")
            return
        rows = []
        kinds = set()
        for entry in catalog.get("items", []):
            rows.append(self._row(entry, icon=True))
            kinds.add(entry.get("kind", "Unknown"))
        for entry in catalog.get("npcs", []):
            rows.append(self._row(entry, icon=False))
            kinds.add(entry.get("kind", "NPC"))
        for row in rows:
            self._model.invisibleRootItem().appendRow(row)
        for k in sorted(kinds):
            self.kind.addItem(k)
        self._count()

    def _row(self, entry: dict, icon: bool) -> list:
        """One row: name (with icon), kind, id. Every cell carries the entry so a click
        anywhere in the row opens it."""
        name = entry.get("name", "?")
        kind = entry.get("kind", "")
        tip = entry.get("tooltip") or ""
        hover = uitext.wrap(f"{name}  (#{entry.get('id')})\n{kind}"
                            + (f"\n\n{tip}" if tip else ""))

        first = QStandardItem(name)
        if icon:
            first.setIcon(self._icon_for(int(entry["id"])))
        first.setData(name.lower(), ROLE_SORT)
        second = QStandardItem(kind)
        second.setData(kind, ROLE_SORT)

        cells = [first, second]
        stats = entry.get("stats") or {}
        for _header, key in STAT_COLUMNS:
            val = stats.get(key)
            cell = QStandardItem("" if not isinstance(val, int) or val <= 0 else str(val))
            # Sort on the number even where the cell is blank, so "none" groups together.
            cell.setData(val if isinstance(val, int) else -999, ROLE_SORT)
            cell.setTextAlignment(Qt.AlignmentFlag.AlignRight | Qt.AlignmentFlag.AlignVCenter)
            cells.append(cell)

        last = QStandardItem(str(entry.get("id")))
        last.setData(int(entry.get("id", 0)), ROLE_SORT)
        last.setTextAlignment(Qt.AlignmentFlag.AlignRight | Qt.AlignmentFlag.AlignVCenter)
        cells.append(last)

        for cell in cells:
            cell.setEditable(False)
            cell.setToolTip(hover)
            cell.setData(entry.get("id"), ROLE_ID)
            cell.setData(kind, ROLE_KIND)
            cell.setData(entry, ROLE_ENTRY)
            cell.setData(f"{name.lower()} #{entry.get('id')}", ROLE_SEARCH)
        return cells

    def _count(self) -> None:
        shown, total = self._proxy.rowCount(), self._model.rowCount()
        self.status.setText(f"{shown} of {total} entries"
                            + ("" if shown == total else " match"))

    def _open_entry(self, proxy_index) -> None:
        if self._opening:
            return                      # doubleClicked + activated both fired
        src = self._proxy.mapToSource(proxy_index)
        item = self._model.itemFromIndex(src.siblingAtColumn(0))
        if item is None:
            return
        entry = item.data(ROLE_ENTRY)
        icon = None if entry.get("npc") else item.icon()
        self._opening = True
        try:
            EntryDialog(self, entry, icon, self._give, self._spawn).exec()
        finally:
            self._opening = False

    def _give(self, item_id: int) -> None:
        self._log(f"[compendium] give #{item_id}")
        self._on_give(item_id)

    # --- spawning ------------------------------------------------------------
    def _spawn(self, entry: dict, distance: int) -> None:
        """Spawn now, unless it is a boss — those are confirmed, then counted down."""
        if entry.get("kind") != BOSS_KIND:
            self._fire(entry, distance)
            return
        name = entry.get("name", "?")
        ok = QMessageBox.question(
            self, "Spawn a boss?",
            f"Spawn <b>{name}</b> {distance} tiles behind you?<br><br>"
            f"It arrives at full health and hostile. You will have "
            f"{BOSS_COUNTDOWN} seconds to cancel.",
            QMessageBox.StandardButton.Yes | QMessageBox.StandardButton.Cancel,
            QMessageBox.StandardButton.Cancel)
        if ok != QMessageBox.StandardButton.Yes:
            self._log(f"[compendium] boss spawn of {name} declined")
            return
        self._start_countdown(entry, distance)

    def _start_countdown(self, entry: dict, distance: int) -> None:
        self._teardown()                          # never run two at once
        self._pending = (entry, distance, BOSS_COUNTDOWN)
        self._countdown = QTimer(self)
        self._countdown.setInterval(1000)
        self._countdown.timeout.connect(self._tick)
        self.gate.setVisible(True)
        self._show_remaining()
        self._countdown.start()

    def _tick(self) -> None:
        entry, distance, left = self._pending
        left -= 1
        if left <= 0:
            self._teardown()            # not cancel_spawn: this one is not a cancellation
            self._fire(entry, distance)
            return
        self._pending = (entry, distance, left)
        self._show_remaining()

    def _show_remaining(self) -> None:
        entry, distance, left = self._pending
        self.gate_label.setText(
            f"Spawning <b>{entry.get('name', '?')}</b> in {left}s "
            f"({distance} tiles behind you)…")

    def _teardown(self) -> None:
        """Stop the timer and hide the row, saying nothing about why."""
        if self._countdown is not None:
            self._countdown.stop()
            self._countdown = None
        self._pending = None
        self.gate.setVisible(False)

    def cancel_spawn(self) -> None:
        """Cancel a pending boss spawn. Safe to call when nothing is pending."""
        pending = self._pending
        self._teardown()
        if pending is not None:
            self._log(f"[compendium] spawn of {pending[0].get('name', '?')} cancelled")

    def _fire(self, entry: dict, distance: int) -> None:
        self._log(f"[compendium] spawn {entry.get('name', '?')} "
                  f"(#{entry.get('id')}) at {distance} tiles")
        if self._on_spawn is not None:
            self._on_spawn(int(entry["id"]), distance)
