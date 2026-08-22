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

import os
import sys

from PyQt6.QtCore import QProcess, QSize, QSortFilterProxyModel, Qt, QTimer
from PyQt6.QtGui import (QColor, QFont, QIcon, QPainter, QPixmap, QStandardItem,
                         QStandardItemModel)
from PyQt6.QtWidgets import (QApplication, QCheckBox, QComboBox, QDialog,
                             QDoubleSpinBox, QGridLayout, QGroupBox, QHBoxLayout,
                             QLabel, QLineEdit, QListView, QPlainTextEdit,
                             QPushButton, QScrollArea, QSpinBox, QTabWidget, QVBoxLayout,
                             QWidget)

from terrariabonker import names, recipes, sprites
from terrariabonker.gui import client, invgrid
from terrariabonker.gui.item_dialog import ItemEditDialog
from terrariabonker.patcher import PATCH_CATALOG

_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
ENTRY = os.path.join(_ROOT, "terrariabonker.py")
ICON = os.path.join(_ROOT, "assets", "terrariabonker.svg")
APPID = "105600"        # Terraria on Steam; launched via steam://rungameid/
CELL_W, CELL_H = 66, 46  # inventory grid cell size
CELL_PT = 8              # cell font point size — small enough that names don't clip


def _cli_args(sub_args: list[str]) -> tuple[str, list[str]]:
    """Build the ('sudo', [...]) argv to run a CLI subcommand under sudo."""
    return "sudo", ["-E", sys.executable, ENTRY, *sub_args]


def _cli_args_user(sub_args: list[str]) -> tuple[str, list[str]]:
    """Argv to run a CLI subcommand WITHOUT sudo — for disk-only work (sprite extraction)
    so its output (the icon cache) is owned by the user, not root."""
    return sys.executable, [ENTRY, *sub_args]


ROLE_ITEM_ID = int(Qt.ItemDataRole.UserRole)
ROLE_SEARCH = int(Qt.ItemDataRole.UserRole) + 1


class _ItemFilterProxy(QSortFilterProxyModel):
    """Live filter over the icon grid: case-insensitive substring of "name #id"."""

    def __init__(self, parent=None):
        super().__init__(parent)
        self._q = ""

    def set_query(self, q: str) -> None:
        self._q = q
        self.invalidateFilter()

    def filterAcceptsRow(self, row, parent) -> bool:
        if not self._q:
            return True
        idx = self.sourceModel().index(row, 0, parent)
        return self._q in (idx.data(ROLE_SEARCH) or "")


class RecipeDialog(QDialog):
    """Popup detailing an item's recipe(s): output, each ingredient (icon + count), and
    the crafting station. ``icon_for`` maps an ItemID to a cached QIcon."""

    def __init__(self, item_id, recs, mode, icon_for, parent=None):
        super().__init__(parent)
        title = names.label(item_id)
        self.setWindowTitle(title)
        self.setMinimumWidth(360)
        self._icon_for = icon_for
        outer = QVBoxLayout(self)

        header = QHBoxLayout()
        h = QLabel()
        h.setPixmap(icon_for(item_id).pixmap(40, 40))
        header.addWidget(h)
        header.addWidget(QLabel(f"<b>{title}</b> &nbsp;"
                                f"<span style='color:gray'>#{item_id}</span>"))
        header.addStretch(1)
        outer.addLayout(header)

        area = QScrollArea()
        area.setWidgetResizable(True)
        body = QWidget()
        bl = QVBoxLayout(body)
        if not recs:
            bl.addWidget(QLabel("<i>No recipe found.</i>"))
        for r in recs:
            bl.addWidget(self._recipe_block(r))
        bl.addStretch(1)
        area.setWidget(body)
        outer.addWidget(area, 1)

    def _recipe_block(self, r) -> QGroupBox:
        station = recipes.station_name(r["tile"]) if "tile" in r else "by hand"
        box = QGroupBox()
        v = QVBoxLayout(box)
        v.addLayout(self._item_row(int(r["out"]), r.get("n", 1), bold=True,
                                   suffix=f" &nbsp;&nbsp;<span style='color:gray'>"
                                          f"[{station}]</span>"))
        v.addWidget(QLabel("<span style='color:gray'>Ingredients:</span>"))
        ing = r.get("ing", [])
        if not ing:
            v.addWidget(QLabel("<i>(none)</i>"))
        for t, c in ing:
            v.addLayout(self._item_row(int(t), c))
        return box

    def _item_row(self, item_id, count, bold=False, suffix="") -> QHBoxLayout:
        row = QHBoxLayout()
        ic = QLabel()
        ic.setPixmap(self._icon_for(item_id).pixmap(28, 28))
        row.addWidget(ic)
        text = f"{names.label(item_id)} ×{count}"
        if bold:
            text = f"<b>{text}</b>"
        row.addWidget(QLabel(text + suffix))
        row.addStretch(1)
        return row


class MainWindow(QWidget):
    def __init__(self):
        super().__init__()
        self.setWindowTitle("terrariabonker")
        if os.path.exists(ICON):
            self.setWindowIcon(QIcon(ICON))
        self._freeze: QProcess | None = None
        self._procs: set[QProcess] = set()   # keep refs so none is GC'd mid-run
        self._status_busy = False
        self._patch_filling = False          # suppress patch-checkbox signals during refresh
        self._patch_cbs: dict = {}           # cheat name -> checkbox
        self._patch_vals: dict = {}          # cheat name -> value spinbox (valued cheats)
        self._cells: dict = {}               # inventory slot -> grid cell button
        self._all_rows: list[dict] = []      # every slot (incl. empty), for slot-finding
        self._item_names = sorted(names._NAMES.values())   # for the edit-dialog completer
        self._icon_cache: dict[int, QIcon] = {}    # ItemID -> QIcon (shared: inventory + recipes)
        self._pixmap_cache: dict[int, QPixmap] = {}  # ItemID -> raw sprite QPixmap (or null)
        self._placeholder_icon = None
        self._sprites_extracting = False           # guard against concurrent extraction
        self._build()
        self._status_timer = QTimer(self)
        self._status_timer.timeout.connect(self.refresh_status)
        self._status_timer.start(2000)
        self.refresh_status()
        self.refresh_patches()               # code patches live on the Trainer tab

    # --- layout ------------------------------------------------------------
    def _build(self):
        root = QVBoxLayout(self)

        top = QHBoxLayout()
        self.status = QLabel("Locating Terraria…")
        self.status.setTextFormat(Qt.TextFormat.RichText)
        self.status.setWordWrap(True)
        top.addWidget(self.status, 1)
        self.btn_launch = QPushButton("Launch Terraria")
        self.btn_launch.setToolTip("Start Terraria through Steam (steam://rungameid/%s)" % APPID)
        self.btn_launch.clicked.connect(self._launch_terraria)
        top.addWidget(self.btn_launch)
        root.addLayout(top)

        tabs = QTabWidget()
        tabs.addTab(self._trainer_tab(), "Trainer")
        tabs.addTab(self._inventory_tab(), "Inventory")
        tabs.addTab(self._recipes_tab(), "Recipes")
        tabs.currentChanged.connect(self._on_tab_changed)
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
        sg.addWidget(self._btn("Heal to full", client.set_hp_argv("max")), 0, 0)
        sg.addWidget(self._btn("Refill mana", client.set_mana_argv("max")), 0, 1)
        self.sp_maxhp = self._spin(100, 9999, 400)
        self.sp_maxmana = self._spin(20, 400, 200)
        sg.addWidget(QLabel("Max HP"), 1, 0)
        sg.addWidget(self.sp_maxhp, 1, 1)
        sg.addWidget(self._btn("Set", lambda: client.set_max_hp_argv(self.sp_maxhp.value())), 1, 2)
        sg.addWidget(QLabel("Max mana"), 2, 0)
        sg.addWidget(self.sp_maxmana, 2, 1)
        sg.addWidget(self._btn("Set", lambda: client.set_max_mana_argv(self.sp_maxmana.value())), 2, 2)
        col.addWidget(stat_box)

        tool_box = QGroupBox("Tools")
        tg = QHBoxLayout(tool_box)
        tg.addWidget(self._btn("Fast mining (all pickaxes)", client.fast_mining_argv()))
        self.sp_reach = self._spin(1, 100, 20)
        tg.addWidget(QLabel("Reach +"))
        tg.addWidget(self.sp_reach)
        tg.addWidget(self._btn("Long reach", lambda: client.long_reach_argv(self.sp_reach.value())))
        col.addWidget(tool_box)

        col.addWidget(self._patches_group())
        col.addStretch()
        return w

    def _inventory_tab(self) -> QWidget:
        """A grid mirroring Terraria's inventory. Each cell is a slot; clicking one
        opens the edit dialog (place an item into an empty slot, or edit a filled
        one). This replaces the old sortable table."""
        outer = QWidget()
        ov = QVBoxLayout(outer)

        bar = QHBoxLayout()
        bar.addWidget(self._btn2("Refresh", self.refresh_inventory))
        hint = QLabel("<i>Click a slot to edit it, or an empty slot to place an item.</i>")
        hint.setWordWrap(True)
        bar.addWidget(hint, 1)
        ov.addLayout(bar)

        area = QScrollArea()
        area.setWidgetResizable(True)
        area.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOff)
        inner = QWidget()
        col = QVBoxLayout(inner)
        for title, rng, cols in invgrid.SECTIONS:
            box = QGroupBox(title)
            g = QGridLayout(box)
            g.setSpacing(3)
            for pos, slot in enumerate(rng):
                cell = self._make_cell(slot)
                self._cells[slot] = cell
                g.addWidget(cell, pos // cols, pos % cols)
            col.addWidget(box)
        col.addStretch()
        area.setWidget(inner)
        # The grid's natural size governs the window minimum, so the full 10-column
        # layout shows without a horizontal scrollbar and without hand-resizing.
        inner.adjustSize()
        sh = inner.sizeHint()
        area.setMinimumWidth(sh.width() + 6)
        area.setMinimumHeight(sh.height() + 6)
        ov.addWidget(area, 1)
        return outer

    def _make_cell(self, slot: int) -> QPushButton:
        b = QPushButton("")
        b.setFixedSize(CELL_W, CELL_H)
        f = QFont(b.font())
        f.setPointSize(CELL_PT)             # a QFont, not a stylesheet, so cell
        b.setFont(f)                        # recolouring in _render_cell keeps it
        b.setIconSize(QSize(CELL_H - 12, CELL_H - 12))   # sprite fills the cell (minus border)
        b.clicked.connect(lambda _=False, s=slot: self._on_cell_clicked(s))
        return b

    def _on_tab_changed(self, i: int):
        if i == 1:
            self.refresh_inventory()
            if not sprites.is_cached():               # icons missing/stale: build them,
                self._extract_sprites(after=self.refresh_inventory)   # then redraw the grid
        elif i == 2:
            self._ensure_recipe_grid()

    def _patches_group(self) -> QGroupBox:
        """Code-patch cheats, embedded in the Trainer tab. No Cheat Engine at
        runtime — these are byte patches (and one code-cave injection) applied through
        /proc; a game restart clears them. (The 'CE' tab is reserved for real CE
        instrumentation.)"""
        box = QGroupBox("Code patches")
        box.setToolTip("Cheats that patch the running game in memory. "
                       "A game restart clears them.")
        g = QGridLayout(box)
        for row, (name, info) in enumerate(PATCH_CATALOG.items()):
            cb = QCheckBox(info.label)
            cb.setToolTip(info.note)
            cb.toggled.connect(lambda on, n=name: self._on_patch_toggled(n, on))
            self._patch_cbs[name] = cb
            g.addWidget(cb, row, 0)
            if info.value is not None:
                spin = self._value_spin(info.value)
                spin.valueChanged.connect(lambda _v, n=name: self._on_patch_value(n))
                self._patch_vals[name] = spin
                g.addWidget(spin, row, 1)
                unit = QLabel(info.value.unit)
                unit.setStyleSheet("color: gray")
                g.addWidget(unit, row, 2)
        g.setColumnStretch(0, 1)
        return box

    def _value_spin(self, spec) -> QWidget:
        if spec.kind == "f32":
            s = QDoubleSpinBox()
            s.setRange(float(spec.lo), float(spec.hi))
            s.setSingleStep(0.05)
            s.setDecimals(2)
            s.setValue(float(spec.default))
        else:
            s = QSpinBox()
            s.setRange(int(spec.lo), int(spec.hi))
            s.setValue(int(spec.default))
        return s

    def _patch_value(self, name: str):
        """Current spinbox value for a valued cheat, or None if it carries none."""
        spin = self._patch_vals.get(name)
        return spin.value() if spin is not None else None

    def _on_patch_toggled(self, name: str, on: bool):
        if self._patch_filling:
            return
        self._run(client.patch_set_argv(name, on, value=self._patch_value(name)))
        QTimer.singleShot(500, self.refresh_patches)

    def _on_patch_value(self, name: str):
        """Re-apply a live patch when its value spinbox changes."""
        if self._patch_filling:
            return
        if self._patch_cbs[name].isChecked():
            self._run(client.patch_set_argv(name, True, value=self._patch_value(name)))

    def refresh_patches(self):
        self._spawn(client.patch_status_argv(), on_output=self._render_patches)

    def _render_patches(self, raw: str):
        st = client.parse_patch_status(raw)
        if st is None:
            return
        on, vals = st["on"], st["values"]
        self._patch_filling = True
        for name, cb in self._patch_cbs.items():
            cb.setChecked(bool(on.get(name)))
            spin = self._patch_vals.get(name)
            if spin is not None and vals.get(name) is not None:
                v = vals[name]
                spin.setValue(float(v) if isinstance(spin, QDoubleSpinBox) else int(v))
        self._patch_filling = False

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

    def _spawn_user(self, sub_args: list[str], on_output=None, on_progress=None) -> QProcess:
        """Like ``_spawn`` but WITHOUT sudo (disk-only work). ``on_progress(line)`` is
        called for each stdout line as it arrives (used for extraction progress)."""
        proc = QProcess(self)
        self._procs.add(proc)
        proc.setProcessChannelMode(QProcess.ProcessChannelMode.MergedChannels)
        buf = {"tail": ""}

        def _ready():
            try:
                chunk = bytes(proc.readAllStandardOutput()).decode(errors="replace")
            except RuntimeError:
                return
            buf["tail"] += chunk
            if on_progress:
                *lines, buf["tail"] = buf["tail"].split("\n")
                for ln in lines:
                    if ln.strip():
                        on_progress(ln.strip())

        def _finished(*_):
            _ready()
            if on_output:
                on_output(buf["tail"])
            self._procs.discard(proc)
            proc.deleteLater()

        proc.readyReadStandardOutput.connect(_ready)
        proc.finished.connect(_finished)
        prog, argv = _cli_args_user(sub_args)
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
        god, mana = self.cb_god.isChecked(), self.cb_mana.isChecked()
        if not (god or mana):
            self.log.appendPlainText("[freeze stopped]")
            return
        sub_args = client.freeze_argv(god, mana)
        self._freeze = QProcess(self)
        self._procs.add(self._freeze)
        prog, argv = _cli_args(sub_args)
        self._freeze.start(prog, argv)
        self.log.appendPlainText(f"[freeze started: {' '.join(sub_args[1:])}]")

    def refresh_status(self):
        if self._status_busy:
            return                           # don't stack overlapping status scans
        self._status_busy = True

        def done(out):
            self._status_busy = False
            self._render_status(out)

        self._spawn(client.status_argv(), on_output=done)

    def _render_status(self, raw: str):
        d = client.parse_status(raw)
        if d is None:
            self.status.setText("<b>Terraria not found</b> — is it running under Proton?")
            return
        god = " · <span style='color:#d33'>GODMODE</span>" if self.cb_god.isChecked() else ""
        self.status.setText(
            f"<b>{d.get('name')}</b> — HP {d.get('hp')}/{d.get('max_hp')} · "
            f"Mana {d.get('mana')}/{d.get('max_mana')} · "
            f"PID {d.get('pid')} · Terraria {d.get('version')}{god}")

    def _launch_terraria(self):
        """Start Terraria through Steam. Unprivileged and detached — Steam
        refuses to run as root, so this never goes through the sudo CLI wrapper."""
        url = f"steam://rungameid/{APPID}"
        ok = QProcess.startDetached("steam", [url]) or QProcess.startDetached("xdg-open", [url])
        note = "" if ok else " — FAILED (is steam installed?)"
        self.log.appendPlainText(f"[launch Terraria: {url}{note}]")

    # --- inventory tab (grid) ----------------------------------------------
    def refresh_inventory(self):
        self._spawn(client.inventory_argv(), on_output=self._fill_grid)

    def _fill_grid(self, raw: str):
        rows = client.parse_inventory(raw)
        if rows is None:
            return
        self._all_rows = rows
        by_slot = {r["slot"]: r for r in rows}
        for slot, cell in self._cells.items():
            self._render_cell(cell, by_slot.get(slot, {"slot": slot, "type": 0}))

    def _render_cell(self, cell: QPushButton, row: dict):
        if invgrid.is_empty(row):
            cell.setText("")
            cell.setIcon(QIcon())
            cell.setToolTip(invgrid.tooltip(row, ""))
            cell.setStyleSheet("QPushButton { color: gray; }")
            return
        name = names.label(row["type"])
        cell.setText("")
        pm = self._pixmap_for(row["type"])
        if pm.isNull():                                 # no sprite: fall back to abbrev text
            badge = invgrid.stack_badge(row.get("stack", 0))
            cell.setIcon(QIcon())
            cell.setText(invgrid.abbrev(name) + (f"\n×{badge}" if badge else ""))
        else:
            cell.setIcon(self._cell_icon(row["type"], row.get("stack", 0)))
        cell.setToolTip(invgrid.tooltip(row, name))
        bg, border = invgrid.cell_colors(row.get("rare", 0))
        cell.setStyleSheet(
            f"QPushButton {{ background-color: rgb{bg};"
            f" border: 1px solid rgb{border}; border-radius: 4px;"
            " color: #f0f0f0; }")

    def _row_for(self, slot: int) -> dict:
        return next((r for r in self._all_rows if r["slot"] == slot),
                    {"slot": slot, "type": 0})

    def _on_cell_clicked(self, slot: int):
        dlg = ItemEditDialog(self, self._row_for(slot), self._item_names)
        if dlg.exec() != QDialog.DialogCode.Accepted:
            return
        if dlg.cleared:
            self._run(client.set_item_argv(slot, 0))
        elif dlg.resolved:
            self._apply_item_edit(slot, dlg.resolved, dlg._orig_type)
        QTimer.singleShot(600, self.refresh_inventory)

    def _apply_item_edit(self, slot: int, r: dict, orig_type: int):
        """Apply the dialog result. A type change (incl. placing into an empty
        slot) sends only type + stack so the ContentSamples template supplies real
        stats; a same-item edit sends the full field set."""
        t = r["type"]
        if t != orig_type:
            self._run(client.set_item_argv(slot, t, stack=r["stack"]))
        else:
            self._run(client.set_item_argv(
                slot, t, stack=r["stack"], damage=r["damage"],
                auto_reuse=r["auto_reuse"], use_time=r["use_time"],
                use_anim=r["use_anim"], pick=r["pick"], tile_boost=r["tile_boost"],
                defense=r["defense"], prefix=r["prefix"]))

    # --- recipes tab -------------------------------------------------------
    def _recipes_tab(self) -> QWidget:
        w = QWidget()
        col = QVBoxLayout(w)
        bar = QHBoxLayout()
        self.recipe_mode = QComboBox()
        self.recipe_mode.addItems(["Makes", "Uses"])
        self.recipe_mode.setToolTip("Makes: pick a craftable item to see how it's made.\n"
                                    "Uses: pick an ingredient to see what it makes.")
        self.recipe_mode.currentTextChanged.connect(self._rebuild_recipe_grid)
        bar.addWidget(self.recipe_mode)
        self.recipe_search = QLineEdit(placeholderText="filter by item name or ItemID…")
        self.recipe_search.textChanged.connect(self._filter_recipe_grid)
        bar.addWidget(self.recipe_search, 1)
        col.addLayout(bar)

        self.recipe_view = QListView()
        self.recipe_view.setViewMode(QListView.ViewMode.IconMode)
        self.recipe_view.setResizeMode(QListView.ResizeMode.Adjust)
        self.recipe_view.setMovement(QListView.Movement.Static)
        self.recipe_view.setUniformItemSizes(True)
        self.recipe_view.setIconSize(QSize(40, 40))
        self.recipe_view.setGridSize(QSize(72, 74))
        rf = QFont(self.recipe_view.font())
        rf.setPointSize(8)                              # small enough that labels don't clip
        self.recipe_view.setFont(rf)
        self.recipe_view.setWordWrap(True)
        self.recipe_view.setSpacing(2)
        self.recipe_view.setEditTriggers(QListView.EditTrigger.NoEditTriggers)
        self.recipe_view.clicked.connect(self._open_recipe_popup)
        col.addWidget(self.recipe_view, 1)

        self._recipe_src_model = QStandardItemModel(self)
        self._recipe_proxy = _ItemFilterProxy(self)
        self._recipe_proxy.setSourceModel(self._recipe_src_model)
        self.recipe_view.setModel(self._recipe_proxy)

        self.recipe_status = QLabel()
        self.recipe_status.setStyleSheet("color: gray")
        col.addWidget(self.recipe_status)

        row = QHBoxLayout()
        row.addWidget(self._btn2("Re-extract from game", self.reextract_recipes))
        note = QLabel("<i>Recipes and item icons are read from the game once and cached "
                      "(offline after). Re-extract after a game update.</i>")
        note.setWordWrap(True)
        row.addWidget(note, 1)
        col.addLayout(row)

        self._recipe_grid_ready = False
        self._recipe_status_hint()
        return w

    def _recipe_status_hint(self):
        recipes._CACHE = None                       # reflect any fresh extraction
        n = len(recipes.load().get("recipes", []))
        if not n:
            self.recipe_status.setText(
                "No recipe cache yet — click 'Re-extract from game' with Terraria running.")
        elif not sprites.is_cached():
            self.recipe_status.setText(f"{n} recipes cached; item icons not yet extracted.")
        else:
            self.recipe_status.setText(f"{n} recipes cached.")

    def _ensure_recipe_grid(self):
        """Populate the grid the first time the tab is shown; extract icons first if the
        cache is missing/stale."""
        if self._recipe_grid_ready:
            return
        recipes._CACHE = None
        if not recipes.load().get("recipes"):
            return                                   # nothing until recipes are extracted
        if sprites.is_cached():
            self._recipe_grid_ready = True
            self._rebuild_recipe_grid()
        else:
            self._extract_sprites(after=self._after_reextract)

    def _placeholder(self) -> QIcon:
        if self._placeholder_icon is None:
            pm = QPixmap(40, 40)
            pm.fill(QColor(70, 70, 70))
            self._placeholder_icon = QIcon(pm)
        return self._placeholder_icon

    def _pixmap_for(self, item_id: int) -> QPixmap:
        """Raw sprite pixmap from the icon cache (may be null if not extracted/missing)."""
        pm = self._pixmap_cache.get(item_id)
        if pm is None:
            path = sprites.icon_path(item_id)
            pm = QPixmap(path) if os.path.exists(path) else QPixmap()
            self._pixmap_cache[item_id] = pm
        return pm

    def _icon_for(self, item_id: int) -> QIcon:
        ic = self._icon_cache.get(item_id)
        if ic is not None:
            return ic
        pm = self._pixmap_for(item_id)
        if pm.isNull():
            ic = self._placeholder()
        else:
            if pm.width() > 40 or pm.height() > 40:
                pm = pm.scaled(40, 40, Qt.AspectRatioMode.KeepAspectRatio,
                               Qt.TransformationMode.FastTransformation)
            ic = QIcon(pm)
        self._icon_cache[item_id] = ic
        return ic

    def _cell_icon(self, item_id: int, stack: int) -> QIcon:
        """Build an inventory-cell icon: the sprite with the stack count composited into
        the bottom-right corner (Terraria-style). Falls back to the placeholder."""
        base = self._pixmap_for(item_id)
        size = CELL_H - 12                              # leave room for the rarity border
        canvas = QPixmap(size, size)
        canvas.fill(Qt.GlobalColor.transparent)
        painter = QPainter(canvas)
        if not base.isNull():
            s = base.scaled(size, size, Qt.AspectRatioMode.KeepAspectRatio,
                            Qt.TransformationMode.FastTransformation)
            painter.drawPixmap((size - s.width()) // 2, (size - s.height()) // 2, s)
        if stack and stack > 1:
            f = QFont(self.font())
            f.setPointSize(8)
            f.setBold(True)
            painter.setFont(f)
            txt = str(stack)
            painter.setPen(QColor(0, 0, 0))            # 1px outline for readability
            for dx, dy in ((-1, 0), (1, 0), (0, -1), (0, 1)):
                painter.drawText(canvas.rect().adjusted(dx, dy, dx, dy),
                                 int(Qt.AlignmentFlag.AlignRight | Qt.AlignmentFlag.AlignBottom),
                                 txt)
            painter.setPen(QColor(255, 255, 255))
            painter.drawText(canvas.rect(),
                             int(Qt.AlignmentFlag.AlignRight | Qt.AlignmentFlag.AlignBottom),
                             txt)
        painter.end()
        return QIcon(canvas)

    def _recipe_item_ids(self, mode: str) -> list[int]:
        recs = recipes.load().get("recipes", [])
        ids: set[int] = set()
        if mode == "Makes":
            for r in recs:
                ids.add(int(r["out"]))
        else:                                        # Uses: items that are ingredients
            for r in recs:
                for t, _ in r.get("ing", []):
                    ids.add(int(t))
        return sorted(ids, key=lambda i: names.label(i).lower())

    def _rebuild_recipe_grid(self, *_):
        if not self._recipe_grid_ready:
            return
        mode = self.recipe_mode.currentText()
        self._recipe_src_model.clear()
        rows = []
        for i in self._recipe_item_ids(mode):
            label = names.label(i)
            it = QStandardItem(self._icon_for(i), invgrid.abbrev(label))   # truncate; full
            it.setEditable(False)                                          # name on hover
            it.setToolTip(f"{label}  (#{i})")
            it.setData(i, ROLE_ITEM_ID)
            it.setData(f"{label.lower()} #{i}", ROLE_SEARCH)               # filter on full
            it.setTextAlignment(Qt.AlignmentFlag.AlignHCenter | Qt.AlignmentFlag.AlignTop)
            rows.append(it)
        self._recipe_src_model.invisibleRootItem().appendRows(rows)   # one batch insert
        self._filter_recipe_grid()

    def _filter_recipe_grid(self, *_):
        if not self._recipe_grid_ready:
            return
        self._recipe_proxy.set_query(self.recipe_search.text().strip().lower())
        shown = self._recipe_proxy.rowCount()
        total = self._recipe_src_model.rowCount()
        noun = "craftable item" if self.recipe_mode.currentText() == "Makes" else "ingredient"
        self.recipe_status.setText(f"{shown} of {total} {noun}(s)"
                                   + ("" if shown == total else " match"))

    def _open_recipe_popup(self, proxy_index):
        src = self._recipe_proxy.mapToSource(proxy_index)
        item = self._recipe_src_model.itemFromIndex(src)
        if item is None:
            return
        item_id = item.data(ROLE_ITEM_ID)
        mode = self.recipe_mode.currentText()
        recs = (recipes.by_output(str(item_id)) if mode == "Makes"
                else recipes.using(str(item_id)))
        RecipeDialog(item_id, recs, mode, self._icon_for, self).exec()

    def reextract_recipes(self):
        self.log.appendPlainText("$ terrariabonker extract-recipes")

        def done(out):
            if out.strip():
                self.log.appendPlainText(out.rstrip())
            recipes._CACHE = None
            # recipes may reference new items -> refresh the icon cache, then rebuild
            self._extract_sprites(after=self._after_reextract)

        self._spawn(client.extract_recipes_argv(), on_output=done)

    def _after_reextract(self):
        self._icon_cache.clear()
        self._recipe_grid_ready = True
        self._rebuild_recipe_grid()

    def _extract_sprites(self, after=None, force=False):
        if self._sprites_extracting:                 # one extraction at a time
            return
        self._sprites_extracting = True
        self.recipe_status.setText("Extracting item icons (one-time, ~15s)…")
        self.log.appendPlainText("$ terrariabonker extract-sprites")

        def prog(line):
            if "/" in line and line.split("/", 1)[0].isdigit():
                self.recipe_status.setText(f"Extracting item icons… {line}")

        def done(_out):
            self._sprites_extracting = False
            self._pixmap_cache.clear()               # drop any placeholders cached pre-extract
            self._icon_cache.clear()
            self._recipe_status_hint()
            if after:
                after()

        self._spawn_user(client.extract_sprites_argv(force), on_output=done, on_progress=prog)

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
    if os.path.exists(ICON):
        app.setWindowIcon(QIcon(ICON))
    w = MainWindow()
    # Open at the natural minimum (the inventory grid drives it) so the full grid
    # is visible immediately; the Inventory tab is the widest/tallest.
    w.resize(w.sizeHint())
    w.show()
    return app.exec()
