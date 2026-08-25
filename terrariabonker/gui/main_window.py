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
import subprocess
import sys

from PyQt6.QtCore import QProcess, QSize, QSortFilterProxyModel, Qt, QTimer
from PyQt6.QtGui import (QColor, QFont, QIcon, QPainter, QPixmap, QStandardItem,
                         QStandardItemModel)
from PyQt6.QtWidgets import (QApplication, QCheckBox, QComboBox, QDialog,
                             QDoubleSpinBox, QGridLayout, QGroupBox, QHBoxLayout,
                             QLabel, QLineEdit, QListView, QMessageBox, QPlainTextEdit,
                             QProgressBar, QPushButton, QScrollArea, QSpinBox,
                             QTabWidget, QVBoxLayout,
                             QWidget)

from terrariabonker import __version__, builds, names, prefixes, profile, recipes
from terrariabonker import sprites
from terrariabonker import version as ver
from terrariabonker.gui import buildgate, client, invgrid
from terrariabonker.gui.compendium import CompendiumTab
from terrariabonker.gui.helper import Helper
from terrariabonker.gui import uitext
from terrariabonker.gui import single, uistate
from terrariabonker.gui.item_dialog import ItemEditDialog
from terrariabonker.patcher import PATCH_CATALOG

_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
ENTRY = os.path.join(_ROOT, "terrariabonker.py")
ICON = os.path.join(_ROOT, "assets", "terrariabonker.svg")
APPID = "105600"        # Terraria on Steam; launched via steam://rungameid/
CELL_W, CELL_H = 66, 46  # inventory grid cell size
CELL_PT = 8              # cell font point size — small enough that names don't clip


def _cli_args(sub_args: list[str]) -> tuple[str, list[str]]:
    """Build the ('sudo', [...]) argv to run a CLI subcommand under sudo. ``-n`` is
    non-interactive: without passwordless sudo the call fails FAST with a clear message
    (in a QProcess there is no TTY to prompt on, so it would otherwise just do nothing)."""
    return "sudo", ["-n", "-E", sys.executable, ENTRY, *sub_args]


def _passwordless_sudo() -> bool:
    """True if sudo runs without a password prompt (what the GUI's memory actions need)."""
    try:
        return subprocess.run(["sudo", "-n", "true"], capture_output=True,
                              timeout=5).returncode == 0
    except (OSError, subprocess.SubprocessError):
        return False


def _cli_args_user(sub_args: list[str]) -> tuple[str, list[str]]:
    """Argv to run a CLI subcommand WITHOUT sudo — for disk-only work (sprite extraction)
    so its output (the icon cache) is owned by the user, not root."""
    return sys.executable, [ENTRY, *sub_args]


# Grid rows built between repaints; see _rebuild_recipe_grid.
GRID_CHUNK = 500
# Auto-restore passes before giving up, whether it is making progress or erroring.
RESTORE_RETRIES = 8
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
        self.setWindowTitle(f"terrariabonker v{__version__}")
        if os.path.exists(ICON):
            self.setWindowIcon(QIcon(ICON))
        self._freeze: QProcess | None = None
        self._procs: set[QProcess] = set()   # keep refs so none is GC'd mid-run
        self._status_busy = False
        self._patch_filling = False          # suppress patch-checkbox signals during refresh
        self._patch_cbs: dict = {}           # cheat name -> checkbox
        self._patch_vals: dict = {}          # cheat name -> value spinbox (valued cheats)
        self._cheat_pending: dict = {}       # name -> (on, value): latest desired state
        self._cheat_busy = False             # one enable/disable at a time (no state race)
        self._cheat_inflight = None          # the cheat currently being applied (skip in render)
        self._cells: dict = {}               # inventory slot -> grid cell button
        self._all_rows: list[dict] = []      # every slot (incl. empty), for slot-finding
        self._item_names = sorted(names._NAMES.values())   # for the edit-dialog completer
        self._icon_cache: dict[int, QIcon] = {}    # ItemID -> QIcon (shared: inventory + recipes)
        self._pixmap_cache: dict[int, QPixmap] = {}  # ItemID -> raw sprite QPixmap (or null)
        self._placeholder_icon = None
        self._sprites_extracting = False           # guard against concurrent extraction
        self._rendered: dict = {}                  # slot -> row currently on screen
        self._inv_inflight = False                 # one outstanding sync request at a time
        self._dialog_open = False                  # pause syncing while the editor is up
        self._opening_slot = False                 # a pre-dialog slot read is outstanding
        self._restore_pid = None                   # last game pid we auto-restored to
        self._gated_builds: set[str] = set()       # builds already asked about (spec 036)
        self._gate_open = False
        self._unavailable: set[str] = set()        # cheats this build cannot run
        self._restore_attempts = 0                 # retry budget for lazily-JIT'd cheats
        self._restore_last_left = None             # last (pending, skipped): stop when unchanged
        self._build()
        # One long-lived privileged worker keeps the locate caches warm, so repeated
        # reads cost ~3 ms instead of ~2.7 s. Without it every call spawns its own CLI.
        self.helper = Helper(self, *_cli_args(["serve"]), on_note=self.log.appendPlainText)
        if _passwordless_sudo():
            self.helper.start()
        self._status_timer = QTimer(self)
        self._status_timer.timeout.connect(self.refresh_status)
        self._status_timer.start(2000)
        # Live inventory sync: the grid follows the game so an edit is never built on a
        # stale snapshot (the write-time guard catches what slips through the gap).
        self._inv_timer = QTimer(self)
        self._inv_timer.timeout.connect(self._sync_inventory)
        self._inv_timer.start(1000)
        # The ore extractor is the one cheat that is not just a patch: enabling it puts a
        # stub in the game, but something has to watch for the player breaking an ore and
        # hand the rest of the vein over. That loop cannot run here (it would block the
        # event loop), so it is driven a slice at a time from this timer and its state
        # lives in the warm worker. Started/stopped by _render_patches, which is the
        # authority on whether the cheat is actually on.
        self._vein_timer = QTimer(self)
        self._vein_timer.timeout.connect(self._tick_veins)
        self._vein_inflight = False
        self.refresh_status()
        self.refresh_patches()               # code patches live on the Trainer tab
        self._check_sudo()

    def _check_sudo(self):
        """Warn (don't block) when passwordless sudo is missing: memory features degrade,
        but recipe browsing / item icons (unprivileged) still work."""
        if _passwordless_sudo():
            self.sudo_warn.hide()
            return
        self.sudo_warn.setText(
            "<b>Memory features unavailable</b> — passwordless sudo is not configured. "
            "The trainer edits game memory via <code>sudo</code>, and the GUI can't prompt "
            "for a password, so trainer/inventory actions will do nothing. Add a NOPASSWD "
            "sudoers rule for it (see the README's Requirements) and restart. "
            "The recipe browser and item icons work without sudo.")
        self.sudo_warn.show()

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
        self.btn_about = QPushButton("About")
        self.btn_about.clicked.connect(self._about)
        top.addWidget(self.btn_about)
        root.addLayout(top)

        # Shown only when passwordless sudo is missing: memory actions need it, and in a
        # QProcess (no TTY) they would otherwise fail silently.
        self.sudo_warn = QLabel()
        self.sudo_warn.setWordWrap(True)
        self.sudo_warn.setTextFormat(Qt.TextFormat.RichText)
        self.sudo_warn.setStyleSheet(
            "background-color: #5a1e1e; color: #ffdddd; border: 1px solid #a33;"
            " border-radius: 4px; padding: 6px;")
        self.sudo_warn.hide()
        root.addWidget(self.sudo_warn)

        # Shown when a cheat cannot be applied on this build, or when the AOBs were never
        # verified on it. This is where that belongs — it used to be invisible except as a
        # repeated [auto-restore] log line.
        self.build_warn = QLabel()
        self.build_warn.setWordWrap(True)
        self.build_warn.setTextFormat(Qt.TextFormat.RichText)
        self.build_warn.setStyleSheet(
            "background-color: #4a3a12; color: #ffe9c0; border: 1px solid #a8842c;"
            " border-radius: 4px; padding: 6px;")
        self.build_warn.hide()
        root.addWidget(self.build_warn)

        tabs = QTabWidget()
        self.tabs = tabs
        tabs.addTab(self._trainer_tab(), "Trainer")
        tabs.addTab(self._inventory_tab(), "Inventory")
        tabs.addTab(self._recipes_tab(), "Recipes")
        # The log widget is built after the tabs, so bind it late rather than passing
        # self.log.appendPlainText here (which would resolve it now, and fail).
        self.compendium = CompendiumTab(self, self._fetch_compendium, self._give_item,
                                        self._icon_for,
                                        lambda msg: self.log.appendPlainText(msg),
                                        self._spawn_npc, self._icon_for_npc, self.busy)
        tabs.addTab(self.compendium, "Compendium")
        tabs.currentChanged.connect(self._on_tab_changed)
        root.addWidget(tabs, 1)

        # Sprite extraction takes ~25s and used to report itself into the Recipes tab's
        # status label, which is invisible when the Compendium tab triggered it. This sits
        # under the tabs, so it is visible whichever tab started the work.
        self.progress = QProgressBar()
        self.progress.setTextVisible(True)
        self.progress.setVisible(False)
        root.addWidget(self.progress)

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

    def _fetch_compendium(self, done, refresh: bool = False):
        """Hand the catalog to the tab when it arrives; None if the command failed."""
        def got(raw):
            done(client.parse_compendium(raw))

        self._call(client.compendium_argv(refresh), on_output=got)

    def _give_item(self, item_id: int):
        self._run(client.give_argv(item_id, 1))

    def busy(self, text: str | None, done: int = 0, total: int = 0) -> None:
        """Drive the shared progress bar. ``text=None`` hides it.

        ``total=0`` means "no idea how long", which Qt renders as a moving bar. Shared
        rather than per-tab because the work it covers — a privileged catalog read, a
        sprite extraction — is started by one tab and blocks all of them.
        """
        if text is None:
            self.progress.setVisible(False)
            self.progress.setRange(0, 100)
            return
        self.progress.setRange(0, max(total, 0))
        if total:
            self.progress.setValue(done)
        self.progress.setFormat(f"{text} %p%" if total else text)
        self.progress.setVisible(True)

    def _spawn_npc(self, net_id: int, distance: int):
        self._run(client.spawn_npc_argv(net_id, distance))

    def _on_tab_changed(self, i: int):
        if i == 3:
            self.compendium.ensure_loaded()
            if not sprites.is_cached():
                self._extract_sprites(after=self.compendium.ensure_loaded)
        if i == 1:
            self.refresh_inventory()
            if not sprites.is_cached():               # icons missing/stale: build them,
                self._extract_sprites(after=self.refresh_inventory)   # then redraw the grid
        elif i == 2:
            self._ensure_recipe_grid()

    def _patches_group(self) -> QGroupBox:
        """Code-patch cheats, embedded in the Trainer tab. No Cheat Engine at
        runtime — these are byte patches (and code-cave injections) applied through /proc;
        a game restart clears them.

        One tab per section rather than one long list: the flat list already needed
        scrolling at twelve patches, and it only grows. Tabs keep the Trainer tab a fixed
        height however many are added.
        """
        box = QGroupBox("Code patches")
        box.setToolTip(uitext.wrap("Cheats that patch the running game in memory. "
                                   "A game restart clears them."))
        outer = QVBoxLayout(box)
        pages = QTabWidget()
        outer.addWidget(pages)

        # The catalog is ordered by section, so a new page starts whenever it changes.
        current, grid, row = None, None, 0
        for name, info in PATCH_CATALOG.items():
            if info.section != current:
                current = info.section
                page = QWidget()
                grid = QGridLayout(page)
                grid.setColumnStretch(0, 1)
                row = 0
                pages.addTab(page, current)
            cb = QCheckBox(info.label)
            cb.setToolTip(uitext.wrap(info.note))
            cb.toggled.connect(lambda on, n=name: self._on_patch_toggled(n, on))
            self._patch_cbs[name] = cb
            grid.addWidget(cb, row, 0)
            if info.value is not None:
                if info.value.presets:
                    w = QComboBox()
                    for label, val in info.value.presets:
                        w.addItem(label, val)
                    w.currentIndexChanged.connect(lambda _i, n=name: self._on_patch_value(n))
                else:
                    w = self._value_spin(info.value)
                    w.valueChanged.connect(lambda _v, n=name: self._on_patch_value(n))
                self._patch_vals[name] = w
                grid.addWidget(w, row, 1)
                unit = QLabel(info.value.unit)
                unit.setStyleSheet("color: gray")
                grid.addWidget(unit, row, 2)
            row += 1
            grid.setRowStretch(row, 1)      # keep rows packed at the top of the page
        self.patch_pages = pages
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
        """Current value for a valued cheat (spinbox value or preset combo data), or None."""
        w = self._patch_vals.get(name)
        if w is None:
            return None
        return w.currentData() if isinstance(w, QComboBox) else w.value()

    def _on_patch_toggled(self, name: str, on: bool):
        if self._patch_filling:
            return
        self._cheat_pending[name] = (on, self._patch_value(name))
        self._pump_cheats()

    def _on_patch_value(self, name: str):
        """Re-apply a live patch when its value spinbox changes."""
        if self._patch_filling:
            return
        if self._patch_cbs[name].isChecked():
            self._cheat_pending[name] = (True, self._patch_value(name))
            self._pump_cheats()

    def _pump_cheats(self):
        """Run queued enable/disable ops ONE AT A TIME. Each op is a separate sudo CLI
        process that load-modify-saves the shared patch-state file; running them serially
        (rather than spawning one per checkbox click) avoids concurrent writes clobbering
        each other's records — the cause of the checkbox/state desync when toggling many
        cheats at once. Repeated toggles of the same cheat coalesce to the latest state;
        the checkboxes re-sync to real memory once the queue drains."""
        if self._cheat_busy or not self._cheat_pending:
            return
        self._cheat_busy = True
        name = next(iter(self._cheat_pending))
        on, value = self._cheat_pending.pop(name)
        self._cheat_inflight = name          # don't let a refresh override it mid-apply
        verb = "enable" if on else "disable"
        self.log.appendPlainText(f"$ terrariabonker patch {verb} {name}")

        def done(out):
            if out.strip():
                self.log.appendPlainText(out.rstrip())
            self._cheat_busy = False
            self._cheat_inflight = None
            if self._cheat_pending:
                self._pump_cheats()          # next queued op
            else:
                self.refresh_patches()       # settled: sync checkboxes to memory

        self._call(client.patch_set_argv(name, on, value=value), on_output=done)

    def refresh_patches(self):
        self._call(client.patch_status_argv(), on_output=self._render_patches)

    def _render_patches(self, raw: str):
        st = client.parse_patch_status(raw)
        if st is None:
            return
        on, vals = st["on"], st["values"]
        detail = st.get("detail") or {}
        text = client.build_banner(st, ver.KNOWN_BUILD_KEY)
        self.build_warn.setText(text)
        self.build_warn.setVisible(bool(text))
        # A cheat that's queued or mid-apply hasn't been confirmed yet — leave its checkbox
        # as the user set it, so an in-flight status refresh can't flicker it off/on.
        busy = set(self._cheat_pending)
        if self._cheat_inflight:
            busy.add(self._cheat_inflight)
        self._patch_filling = True
        for name, cb in self._patch_cbs.items():
            d = detail.get(name) or {}
            if detail:
                # A cheat the build gate found dead stays off even if a later scan
                # resolves it: the user chose to run without it (spec 036).
                gated = name in self._unavailable
                available = bool(d.get("available", True)) and not gated
                cb.setEnabled(available)
                if gated:
                    cb.setToolTip(uitext.wrap(
                        "Disabled for this build: it did not match when the game "
                        "updated, and you chose to continue without it. Re-check with "
                        "'terrariabonker build-check'."))
                elif not available:
                    cb.setToolTip(uitext.wrap(
                        f"Unavailable on this build: {d.get('reason', '')}"))
                elif not d.get("verified", True):
                    cb.setToolTip(uitext.wrap(
                        f"{PATCH_CATALOG[name].note}\n\n"
                        "(AOB unverified on this build — it resolves, but was confirmed "
                        "on a different one.)"))
            if name in busy:
                continue
            cb.setChecked(bool(on.get(name)))
            w = self._patch_vals.get(name)
            if w is not None and vals.get(name) is not None:
                v = vals[name]
                if isinstance(w, QComboBox):
                    idx = w.findData(int(v))
                    if idx >= 0:
                        w.setCurrentIndex(idx)
                elif isinstance(w, QDoubleSpinBox):
                    w.setValue(float(v))
                else:
                    w.setValue(int(v))
        self._patch_filling = False
        cb = self._patch_cbs.get("ore_extract")
        self._set_vein_watch(bool(cb is not None and cb.isChecked() and cb.isEnabled()))

    def _tick_veins(self):
        """One slice of vein watching. Skipped while a request is already out, so a slow
        round cannot pile overlapping ticks onto the worker."""
        if not self.helper.available or self._vein_inflight:
            return
        self._vein_inflight = True

        def done(raw: str):
            self._vein_inflight = False
            for line in raw.splitlines():
                line = line.strip()
                if not line.startswith("{"):
                    continue
                try:
                    got = json.loads(line)
                except ValueError:
                    continue
                for e in got.get("events", []):
                    at = e.get("at", ["?", "?"])
                    self.log.appendPlainText(
                        f"[extract] {e.get('mined', 0)} tiles at ({at[0]}, {at[1]})"
                        + (f" — {e['reason']}" if e.get("reason") else ""))

        argv = ["extract-tick", "--json"]
        if self._patch_value("ore_extract"):        # "Ores + gems"
            argv.append("--gems")
        if not self.helper.request(argv, done):
            self._vein_inflight = False

    def _set_vein_watch(self, on: bool):
        """Follow the cheat: watch while it is on, and disarm when it goes off.

        A queue left armed would be re-mined every frame, so switching the cheat off has
        to tell the worker to drop its watcher rather than just stopping the timer.
        """
        if on and not self._vein_timer.isActive():
            self._vein_timer.start(100)
        elif not on and self._vein_timer.isActive():
            self._vein_timer.stop()
            self._vein_inflight = False
            self.helper.request(["extract-stop", "--json"], lambda _out: None)

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
    def _call(self, sub_args: list[str], on_output=None):
        """Run a CLI subcommand: through the warm worker when it is up, else by
        spawning it one-shot (no passwordless sudo, worker died, command not served)."""
        if self.helper.request(sub_args, on_output or (lambda _out: None)):
            return None
        return self._spawn(sub_args, on_output=on_output)

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

        self._call(sub_args, on_output=done)

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

        self._call(client.status_argv(), on_output=done)

    def _render_status(self, raw: str):
        d = client.parse_status(raw)
        if d is None:
            self.status.setText("<b>Terraria not found</b> — is it running under Proton?")
            return
        god = " · <span style='color:#d33'>GODMODE</span>" if self.cb_god.isChecked() else ""
        # version+buildid is the identity an AOB is really pinned to, so show that key
        # rather than the version alone (see version.build_key / the anchor ledger).
        build = d.get("build") if d.get("buildid") else d.get("version")
        self.status.setText(
            f"<b>{d.get('name')}</b> — HP {d.get('hp')}/{d.get('max_hp')} · "
            f"Mana {d.get('mana')}/{d.get('max_mana')} · "
            f"PID {d.get('pid')} · Terraria {build}{god}")
        self._maybe_gate_build(build, d)
        self._maybe_restore(d)

    def _maybe_gate_build(self, build: str | None, d: dict | None = None):
        """Ask about a build we do not recognise, once per build (spec 036).

        The trigger is the build key rather than panel startup: the case this exists for
        is Terraria updating underneath a running panel, so the gate has to fire when the
        game is restarted into a new build too.

        Waits for a player to be in-world. Several cheats hook methods mono compiles
        lazily, so a scan at the main menu reports them as unmatched — and a dialog that
        says a cheat is dead when it is merely not compiled yet is worse than no dialog.
        """
        if not build or build in self._gated_builds or self._gate_open:
            return
        if d is not None and not d.get("name"):
            return                                  # no player yet: too early to judge
        self._gated_builds.add(build)
        self._gate_open = True
        self.busy("Checking the cheats against this build…")

        def got(raw):
            self.busy(None)
            report = client.parse_build_check(raw)
            if report is None:
                self._gate_open = False
                self._gated_builds.discard(build)      # unreadable: ask again next time
                return
            self._apply_build_decision(report)

        self._call(client.build_check_argv(), on_output=got)

    def _apply_build_decision(self, report: dict):
        """Act on a finished build check: nothing to do, a remembered decision, or ask."""
        self._gate_open = False
        failed = set(report.get("failed") or ())
        if report.get("recognised"):
            # Known good, or already decided here — honour any cheats recorded as dead.
            if report.get("decision") == buildgate.CONTINUE:
                self._unavailable = set(builds.failed_cheats(report["build"]))
                self.refresh_patches()
            return

        dlg = buildgate.BuildGateDialog(self, report, ver.KNOWN_BUILD_KEY)
        dlg.exec()
        choice = dlg.result_decision
        if choice == buildgate.EXIT:
            self.log.appendPlainText(
                f"[build] {report['build']} not accepted — exiting")
            QTimer.singleShot(0, self.close)
            return
        self._unavailable = failed if choice == buildgate.CONTINUE else set()
        self.log.appendPlainText(
            f"[build] {report['build']} recorded as {choice}"
            + (f"; disabled {', '.join(sorted(failed))}" if failed else ""))
        self._spawn(client.accept_build_argv(choice, failed))
        self.refresh_patches()

    def _maybe_restore(self, d: dict):
        """Auto-restore the saved profile when a fresh in-world game is detected (any new
        pid). Retries a few times for cheats whose method JITs lazily (e.g. fast-placement,
        which compiles only when an item is first used)."""
        pid = d.get("pid")
        if pid is None or not d.get("name"):        # need a located player (game in-world)
            return
        if not (profile.cheats() or profile.item_edits()):
            return                                   # nothing saved to restore
        if pid != self._restore_pid:
            self._restore_pid = pid
            self._restore_attempts = 0
            self._restore_last_left = None
            self._do_restore()

    def _do_restore(self):
        self._restore_attempts += 1

        def done(out):
            rep = client.parse_restore(out)
            if rep is None:
                # A refusal (e.g. require_compatible on a misread build) used to vanish
                # here, so auto-restore looked like it simply never ran.
                #
                # It is also worth retrying. The refusal that actually happens is a
                # startup race — the version string is scanned out of live memory, and
                # for a moment after launch the game's own has not been allocated — so
                # giving up on the first error killed auto-restore for the whole session
                # over a condition that clears itself within seconds.
                if self._restore_attempts < RESTORE_RETRIES:
                    QTimer.singleShot(2000, self._do_restore)
                    return
                if "[ERROR]" in out:
                    self.log.appendPlainText(f"[auto-restore FAILED] {out.strip()}")
                return
            if self._restore_attempts == 1 and (rep["cheats"] or rep["items"]
                                                or rep["pending"]):
                self.log.appendPlainText(
                    f"[auto-restore] cheats={rep['cheats']} items={rep['items']} "
                    f"pending={rep['pending']} skipped={rep['skipped']}")
            self.refresh_patches()
            # Retry only while retrying still achieves something: a cheat whose method has
            # not JIT-compiled yet resolves on a later pass, but one that cannot resolve on
            # this build never will. Two passes with the same leftovers means no progress,
            # so stop — the reason is on the build banner, not in a repeated log line.
            left = (sorted(rep["pending"]), sorted(rep["skipped"]))
            progressed = left != self._restore_last_left
            self._restore_last_left = left
            if any(left) and progressed and self._restore_attempts < RESTORE_RETRIES:
                QTimer.singleShot(2000, self._do_restore)
            elif any(left) and not progressed and self._restore_attempts > 1:
                for line in client.restore_summary(rep):
                    self.log.appendPlainText(line)

        self._call(client.restore_argv(), on_output=done)

    def _about(self):
        QMessageBox.about(
            self, "About terrariabonker",
            f"<h3>terrariabonker v{__version__}</h3>"
            "<p>A from-scratch live-memory trainer and item editor for "
            "<b>Terraria 1.4.5.7</b> (Windows build under Proton). It finds the player in "
            "<code>/proc/&lt;pid&gt;/mem</code> with no hardcoded addresses, then reads and "
            "edits player state, inventory, and code-patch cheats.</p>"
            "<p>Cheat sites are derived with Cheat Engine's mono dissector; nothing needs CE "
            "at runtime. Several code-patch cheats (pickup range, spawn rate, drop-chance "
            "floor, map-ping teleport) are ported from the FearLess Forums "
            "<b>“TerrariaReGrind”</b> Cheat Engine table — reverse-engineering "
            "credit for those hooks belongs to the ReGrind authors; the 1.4.5.7 sites were "
            "re-derived here.</p>"
            "<p><i>Edits your own single-player game in memory; writes nothing to disk.</i></p>")

    def _launch_terraria(self):
        """Start Terraria through Steam. Unprivileged and detached — Steam
        refuses to run as root, so this never goes through the sudo CLI wrapper."""
        url = f"steam://rungameid/{APPID}"
        ok = QProcess.startDetached("steam", [url]) or QProcess.startDetached("xdg-open", [url])
        note = "" if ok else " — FAILED (is steam installed?)"
        self.log.appendPlainText(f"[launch Terraria: {url}{note}]")

    # --- inventory tab (grid) ----------------------------------------------
    def refresh_inventory(self):
        self._call(client.inventory_argv(), on_output=self._fill_grid)

    def _inventory_visible(self) -> bool:
        return self.tabs.currentIndex() == self.tabs.indexOf(self.tabs.widget(1))

    def _sync_inventory(self):
        """The 1 Hz tick: keep the grid tracking the game.

        Only runs through the warm worker. A one-shot read costs ~2.7 s, so polling it
        at 1 Hz would just pile overlapping scans onto a core forever; without the
        worker the grid stays manual (Refresh, and the reload after an edit), and the
        write-time guard still makes a stale edit safe.

        Skipped while the tab is hidden (nothing to see), while the edit dialog is up
        (the row under it must not move), and while a request is already out.
        """
        if not self.helper.available or self._inv_inflight or self._dialog_open:
            return
        if not self._inventory_visible():
            return
        self._inv_inflight = True

        def done(raw):
            self._inv_inflight = False
            self._fill_grid(raw)

        if not self.helper.request(client.inventory_argv(), done):
            self._inv_inflight = False           # worker went away between checks

    def _fill_grid(self, raw: str):
        rows = client.parse_inventory(raw)
        if rows is None:
            return
        self._all_rows = rows
        by_slot = {r["slot"]: r for r in rows}
        full = [by_slot.get(slot, {"slot": slot, "type": 0}) for slot in self._cells]
        # Re-render only what moved: repainting an unchanged cell resets its icon and
        # tooltip, which would cancel a hover the user is reading at 1 Hz.
        for row in invgrid.changed_rows(self._rendered, full):
            self._render_cell(self._cells[row["slot"]], row)
            self._rendered[row["slot"]] = dict(row)

    def _invalidate_icons(self):
        """Drop the icon caches AND the rendered-cell snapshot.

        After a sprite extraction the inventory rows are byte-identical, so the
        no-flicker diff would skip every cell and the freshly decoded sprites would
        never appear — cells stay on their text placeholders until an item changes.
        """
        self._pixmap_cache.clear()
        self._icon_cache.clear()
        self._rendered.clear()

    def _render_cell(self, cell: QPushButton, row: dict):
        if invgrid.is_empty(row):
            cell.setText("")
            cell.setIcon(QIcon())
            cell.setToolTip(invgrid.tooltip(row, ""))
            cell.setStyleSheet("QPushButton { color: gray; }")
            return
        name = names.label(row["type"])
        pfx = prefixes.name(row.get("prefix", 0))
        full = f"{pfx} {name}" if pfx else name          # e.g. "Fabled Slime Staff"
        quality = prefixes.quality(row.get("prefix", 0))
        cell.setText("")
        pm = self._pixmap_for(row["type"])
        if pm.isNull():                                 # no sprite: fall back to abbrev text
            badge = invgrid.stack_badge(row.get("stack", 0))
            cell.setIcon(QIcon())
            cell.setText(invgrid.abbrev(name) + (f"\n×{badge}" if badge else ""))
        else:
            cell.setIcon(self._cell_icon(row["type"], row.get("stack", 0), quality))
        cell.setToolTip(invgrid.tooltip(row, full))
        bg, border = invgrid.cell_colors(row.get("rare", 0))
        cell.setStyleSheet(
            f"QPushButton {{ background-color: rgb{bg};"
            f" border: 1px solid rgb{border}; border-radius: 4px;"
            " color: #f0f0f0; }")

    def _row_for(self, slot: int) -> dict:
        return next((r for r in self._all_rows if r["slot"] == slot),
                    {"slot": slot, "type": 0})

    def _on_cell_clicked(self, slot: int):
        """Re-read the slot before opening the editor so the dialog starts from truth,
        not from an up-to-a-second-old row. Falls back to the cached row if the read
        fails or the worker is not up."""
        if self._dialog_open or self._opening_slot:
            return                      # Qt pumps events during a modal: no stacked dialogs
        if self.helper.available:
            self._opening_slot = True

            def fresh(raw):
                self._opening_slot = False
                if client.parse_inventory(raw) is not None:
                    self._fill_grid(raw)
                self._open_item_dialog(slot)

            if self.helper.request(client.inventory_argv(), fresh):
                return
            self._opening_slot = False
        self._open_item_dialog(slot)

    def _open_item_dialog(self, slot: int):
        row = self._row_for(slot)
        orig_type = int(row.get("type", 0))
        dlg = ItemEditDialog(self, row, self._item_names)
        self._dialog_open = True
        try:
            accepted = dlg.exec() == QDialog.DialogCode.Accepted
        finally:
            self._dialog_open = False
        if not accepted:
            return
        if dlg.cleared:
            self._write_slot(client.set_item_argv(slot, 0, expect_type=orig_type))
        elif dlg.resolved:
            self._apply_item_edit(slot, dlg.resolved, orig_type)
        QTimer.singleShot(600, self.refresh_inventory)

    def _apply_item_edit(self, slot: int, r: dict, orig_type: int):
        """Apply the dialog result. A type change (incl. placing into an empty
        slot) sends only type + stack so the ContentSamples template supplies real
        stats; a same-item edit sends the full field set. Either way the write
        carries ``--expect-type``: the slot must still hold what the dialog was
        opened on, or the CLI refuses it (see spec 029)."""
        t = r["type"]
        if t != orig_type:
            self._write_slot(client.set_item_argv(slot, t, stack=r["stack"],
                                                  expect_type=orig_type))
        else:
            self._write_slot(client.set_item_argv(
                slot, t, stack=r["stack"], damage=r["damage"],
                auto_reuse=r["auto_reuse"], use_time=r["use_time"],
                use_anim=r["use_anim"], pick=r["pick"], tile_boost=r["tile_boost"],
                defense=r["defense"], prefix=r["prefix"], expect_type=orig_type))

    def _write_slot(self, sub_args: list[str]):
        """A slot write, which the stale-snapshot guard may refuse. On refusal the
        grid is re-read at once so the next attempt edits the truth."""
        self.log.appendPlainText(f"$ terrariabonker {' '.join(sub_args)}")

        def done(out):
            if out.strip():
                self.log.appendPlainText(out.rstrip())
            if "[ERROR]" in out:
                msg = out.split("[ERROR]", 1)[1].strip() or "the write was refused"
                QMessageBox.warning(self, "Slot changed in-game", msg)
                self.refresh_inventory()
            self.refresh_status()

        self._call(sub_args, on_output=done)

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

    def _icon_for_npc(self, npc_type: int, net_id: int | None = None) -> QIcon:
        """NPC sprites live in the same cache under their own name, so they get their own
        cache key too — an NPC type and an item id are different things with the same
        small integers.

        A tinted variant is preferred where one exists: every coloured slime shares one
        neutral sheet and is told apart only by its netID's tint.
        """
        key = ("npc", npc_type, net_id)
        ic = self._icon_cache.get(key)
        if ic is not None:
            return ic
        pm = QPixmap()
        if net_id is not None:
            pm = QPixmap(sprites.npc_tinted_icon_path(net_id))
        if pm.isNull():
            pm = QPixmap(sprites.npc_icon_path(npc_type))
        if pm.isNull():
            ic = self._placeholder()
        else:
            if pm.width() > 40 or pm.height() > 40:
                pm = pm.scaled(40, 40, Qt.AspectRatioMode.KeepAspectRatio,
                               Qt.TransformationMode.SmoothTransformation)
            ic = QIcon(pm)
        self._icon_cache[key] = ic
        return ic

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

    _QUALITY_DOT = {"good": QColor(80, 220, 90), "bad": QColor(230, 70, 70),
                    "neutral": QColor(180, 180, 180)}

    def _cell_icon(self, item_id: int, stack: int, quality: str = "none") -> QIcon:
        """Build an inventory-cell icon: the sprite with the stack count composited into
        the bottom-right corner (Terraria-style), plus a top-left dot for a modifier
        (green=beneficial, red=detrimental, gray=neutral). Falls back to the placeholder."""
        base = self._pixmap_for(item_id)
        size = CELL_H - 12                              # leave room for the rarity border
        canvas = QPixmap(size, size)
        canvas.fill(Qt.GlobalColor.transparent)
        painter = QPainter(canvas)
        if not base.isNull():
            s = base.scaled(size, size, Qt.AspectRatioMode.KeepAspectRatio,
                            Qt.TransformationMode.FastTransformation)
            painter.drawPixmap((size - s.width()) // 2, (size - s.height()) // 2, s)
        dot = self._QUALITY_DOT.get(quality)
        if dot is not None:                             # modifier indicator, top-left
            painter.setPen(QColor(0, 0, 0))
            painter.setBrush(dot)
            painter.drawEllipse(1, 1, 6, 6)
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
        ids = list(self._recipe_item_ids(mode))
        root = self._recipe_src_model.invisibleRootItem()
        # Built in slices for the same reason the compendium is: a few thousand icons in
        # one go freezes the window, and a frozen window cannot paint a progress bar.
        for start in range(0, len(ids), GRID_CHUNK):
            rows = []
            for i in ids[start:start + GRID_CHUNK]:
                label = names.label(i)
                it = QStandardItem(self._icon_for(i), invgrid.abbrev(label))  # truncate;
                it.setEditable(False)                                         # full on hover
                it.setToolTip(f"{label}  (#{i})")
                it.setData(i, ROLE_ITEM_ID)
                it.setData(f"{label.lower()} #{i}", ROLE_SEARCH)      # filter on the full
                it.setTextAlignment(Qt.AlignmentFlag.AlignHCenter
                                    | Qt.AlignmentFlag.AlignTop)
                rows.append(it)
            root.appendRows(rows)                                    # batch insert a slice
            if len(ids) > GRID_CHUNK:
                self.busy("Building the recipe grid…",
                          min(start + GRID_CHUNK, len(ids)), len(ids))
                QApplication.processEvents()
        self.busy(None)
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
        self._invalidate_icons()
        self._recipe_grid_ready = True
        self._rebuild_recipe_grid()

    def _extract_sprites(self, after=None, force=False):
        if self._sprites_extracting:                 # one extraction at a time
            return
        self._sprites_extracting = True
        self.recipe_status.setText("Extracting sprites (one-time, ~25s)…")
        self.log.appendPlainText("$ terrariabonker extract-sprites")

        self.busy("Extracting sprites…")             # until the first count arrives

        def prog(line):
            if "/" not in line:
                return
            head, _, tail = line.partition("/")
            if not (head.isdigit() and tail.isdigit()):
                return
            self.busy("Extracting sprites…", int(head), int(tail))
            self.recipe_status.setText(f"Extracting sprites… {line}")

        def done(_out):
            self._sprites_extracting = False
            self.busy(None)
            self._invalidate_icons()                 # drop placeholders + force a full redraw
            self._recipe_status_hint()
            if after:
                after()

        self._spawn_user(client.extract_sprites_argv(force), on_output=done, on_progress=prog)

    def closeEvent(self, event):
        uistate.save_size(self.width(), self.height())
        self._status_timer.stop()
        self._inv_timer.stop()
        self.helper.stop()
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
    ok, other = single.acquire()
    if not ok:
        where = f" (pid {other})" if other else ""
        QMessageBox.warning(
            None, "terrariabonker is already running",
            f"<b>Another control panel is already open{where}.</b>"
            "<p>Running two would start two privileged workers and two auto-restore "
            "loops on the same game, which fight over the shared patch state. Use the "
            "existing window.</p>")
        return 1
    w = MainWindow()
    # Reopen at the size the user left it; otherwise the natural minimum (the inventory
    # grid drives it) so the full grid is visible immediately.
    saved = uistate.load_size()
    w.resize(QSize(*saved) if saved else w.sizeHint())
    w.show()
    return app.exec()
