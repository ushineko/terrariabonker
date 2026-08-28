"""The panel must actually build.

The import smoke test catches a bad import line, but not a widget wired up before the
widget it depends on exists — which is how `self.log.appendPlainText` passed to a tab
constructed earlier in _build() slipped through and stopped the GUI starting. This builds
the real MainWindow with the privileged paths stubbed out, so nothing is spawned.
"""


import pytest

pytest.importorskip("PyQt6.QtWidgets")


def test_main_window_builds_with_every_tab(gui_window, monkeypatch):

    # no sudo probe, no worker, no subprocesses: this test is about widget wiring

    w = gui_window()
    try:
        titles = [w.tabs.tabText(i) for i in range(w.tabs.count())]
        # Projectiles sits beside Effects because it behaves like one: held by the
        # trainer, gone when it closes. Patches keep working until the game restarts.
        assert titles == ["Player", "Effects", "Projectiles", "Patches", "Inventory",
                          "Recipes", "Compendium"]
        assert w.log is not None, "the log widget the tabs log through must exist"
        # the controls that moved out of the old Trainer tab must still be wired
        for attr in ("cb_god", "cb_mana", "sp_maxhp", "sp_maxmana", "sp_reach",
                     "cb_potions", "sp_potion_stack",
                     "cb_projectiles", "cb_pj_weapon", "cb_pj_nocollide", "sp_pj_life"):
            assert getattr(w, attr, None) is not None, f"{attr} was lost in the split"
    finally:
        w.close()


def test_every_cheat_has_a_checkbox(gui_window, monkeypatch):
    """The patch list is generated from the catalog, so a new cheat must appear."""
    from terrariabonker.patcher import PATCH_CATALOG
    w = gui_window()
    try:
        assert set(w._patch_cbs) == set(PATCH_CATALOG)
    finally:
        w.close()


def test_code_patches_are_split_into_section_tabs(gui_window, monkeypatch):
    """One long list did not scale: tabs keep the Trainer tab a fixed height as patches
    are added."""
    from terrariabonker.patcher import SECTIONS
    w = gui_window()
    try:
        pages = [w.patch_pages.tabText(i) for i in range(w.patch_pages.count())]
        assert pages == [name for name, _members in SECTIONS]
    finally:
        w.close()


def test_extraction_reports_through_a_progress_bar(gui_window, monkeypatch):
    """The status text used to go to the Recipes tab's label, which is invisible when the
    Compendium tab is what triggered the extraction."""
    from terrariabonker.gui import main_window as mw

    w = gui_window()
    captured = {}
    # After the window is built, not before: the shared fixture stubs _spawn_user itself,
    # so a capture installed first would simply be overwritten by it.
    monkeypatch.setattr(
        mw.MainWindow, "_spawn_user",
        lambda self, argv, on_output=None, on_progress=None: captured.update(
            on_progress=on_progress, on_output=on_output))
    try:
        _check_progress(w, captured)
    finally:
        w.close()


def _check_progress(w, captured):
    assert w.progress.isHidden(), "progress bar showing before anything runs"
    w._extract_sprites()

    assert not w.progress.isHidden(), "no progress bar while extracting"
    captured["on_progress"]("120/6892")
    assert w.progress.maximum() == 6892 and w.progress.value() == 120
    captured["on_progress"]("not a count")          # must not throw or reset
    assert w.progress.value() == 120
    captured["on_output"]("[OK] done")
    assert w.progress.isHidden(), "progress bar left on screen after extraction"


def _window(gui_window):
    """The module plus a window, since several tests monkeypatch module-level names."""
    from terrariabonker.gui import main_window as mw

    return mw, gui_window()


def test_auto_restore_retries_a_refusal_instead_of_giving_up(gui_window, monkeypatch):
    """The refusal that actually happens is a startup race — the version is scanned out
    of live memory, and for a moment after launch the game's own string is not there yet.
    Giving up on the first error killed auto-restore for the whole session over a
    condition that clears itself in seconds."""
    mw, w = _window(gui_window)
    try:
        scheduled = []
        monkeypatch.setattr(mw.QTimer, "singleShot",
                            staticmethod(lambda ms, fn: scheduled.append(ms)))
        captured = {}
        monkeypatch.setattr(mw.MainWindow, "_call",
                            lambda self, argv, on_output=None: captured.update(
                                cb=on_output))
        refusal = ("[ERROR] Terraria 2.0.50727 differs from 1.4.5.7 in "
                   "major/minor/patch; the offsets are almost certainly wrong")

        w._restore_attempts = 0
        w._do_restore()
        captured["cb"](refusal)
        assert scheduled, "a refusal was not retried"

        # ...but not forever: a build that really is wrong must stop nagging.
        scheduled.clear()
        w._restore_attempts = mw.RESTORE_RETRIES
        w._do_restore()
        captured["cb"](refusal)
        assert not scheduled, "kept retrying past the limit"
        assert "auto-restore FAILED" in w.log.toPlainText()
    finally:
        w.close()


def test_the_vein_watcher_follows_the_extractor_checkbox(gui_window, monkeypatch):
    """The ore extractor is the one cheat that is not just a patch. Enabling it puts a
    stub in the game, but something has to notice the player breaking an ore and hand the
    rest of the vein over — and that loop cannot run on the Qt thread. It is driven from a
    timer, so the timer has to follow the cheat.

    Switching it off must also tell the worker to drop its watcher, not merely stop the
    timer: a queue left armed is re-mined every frame.
    """

    w = gui_window()
    try:
        sent = []
        w.helper.available = True
        w.helper.request = lambda argv, cb: (sent.append(argv), True)[1]

        assert not w._vein_timer.isActive(), "the watcher must not run unasked"
        w._set_vein_watch(True)
        assert w._vein_timer.isActive(), "the cheat is on but nothing is watching"
        assert w._vein_timer.interval() <= 250, "too slow to catch the first tile"

        w._set_vein_watch(False)
        assert not w._vein_timer.isActive()
        assert any(a[0] == "extract-stop" for a in sent), \
            "switching off must disarm the queue, not just stop the timer"

        # a tick asks the worker for one slice, and only one at a time
        sent.clear()
        w._set_vein_watch(True)
        w._tick_veins()
        assert sent and sent[0][0] == "extract-tick"
        sent.clear()
        w._tick_veins()                       # the first is still in flight
        assert not sent, "overlapping ticks would pile up on the worker"
    finally:
        w.close()


def test_the_gems_choice_actually_reaches_the_watcher(gui_window, monkeypatch):
    """`--gems` existed on the CLI from the start, but the panel never sent it — so for
    anyone not editing the source, "gems are opt-in" was untrue. The choice has to be
    reachable *and* has to arrive at the worker."""
    from PyQt6.QtWidgets import QComboBox
    from terrariabonker.gui import main_window as mw

    monkeypatch.setattr(mw, "_passwordless_sudo", lambda: False)
    for n in ("_call", "_spawn", "_spawn_user"):
        monkeypatch.setattr(mw.MainWindow, n, lambda self, *a, **k: None)
    w = gui_window()
    try:
        combo = w._patch_vals.get("ore_extract")
        assert isinstance(combo, QComboBox), "no way to choose from the panel"
        assert [combo.itemData(i) for i in range(combo.count())] == [0, 1]

        sent = []
        w.helper.available = True
        w.helper.request = lambda argv, cb: (sent.append(argv), True)[1]

        combo.setCurrentIndex(0)                      # ores only
        w._vein_inflight = False
        w._tick_veins()
        assert "--gems" not in sent[-1], "swept gems without being asked"

        combo.setCurrentIndex(1)                      # ores + gems
        w._vein_inflight = False
        w._tick_veins()
        assert "--gems" in sent[-1], "the choice never reached the worker"
    finally:
        w.close()


def test_selecting_a_tab_dispatches_by_widget_not_by_position(gui_window, monkeypatch):
    """Reordering the tab strip used to break this silently. The handler keyed off a
    hardcoded index, so promoting Patches to its own tab made the compendium load when
    Inventory was clicked and never when Compendium was -- no error, just a tab that
    stayed empty."""
    from terrariabonker.gui import main_window as mw

    monkeypatch.setattr(mw.sprites, "is_cached", lambda: True)   # no extraction here

    w = gui_window()
    try:
        fired = []
        monkeypatch.setattr(w.compendium, "ensure_loaded", lambda: fired.append("comp"))
        monkeypatch.setattr(w, "refresh_inventory", lambda: fired.append("inv"))
        monkeypatch.setattr(w, "_ensure_recipe_grid", lambda: fired.append("recipes"))

        by_title = {w.tabs.tabText(i): i for i in range(w.tabs.count())}
        for title, want in (("Compendium", "comp"), ("Inventory", "inv"),
                            ("Recipes", "recipes")):
            fired.clear()
            w.tabs.setCurrentIndex(by_title[title])
            assert fired == [want], f"selecting {title} fired {fired}"

        for title in ("Player", "Effects", "Patches"):
            fired.clear()
            w.tabs.setCurrentIndex(by_title[title])
            assert fired == [], f"selecting {title} fired {fired}"
    finally:
        w.close()


def test_the_potions_checkbox_drives_the_renewal_timer(gui_window, monkeypatch):
    """The checkbox is the whole interface to passive potions. Wired to nothing, it looks
    exactly like a working cheat that grants no buffs."""
    from terrariabonker import buffs
    w = gui_window()
    try:
        assert not w._potion_timer.isActive(), "renewing before being asked to"
        w.cb_potions.setChecked(True)
        assert w._potion_timer.isActive()
        buff_ms = buffs.DEFAULT_TICKS / 60 * 1000
        assert w._potion_timer.interval() < buff_ms, \
            "the timer is slower than the buff it writes — the buff would flicker"
        w.cb_potions.setChecked(False)
        assert not w._potion_timer.isActive()
    finally:
        w.close()


def test_the_inventory_grid_does_not_set_the_window_minimum_height(gui_window, monkeypatch):
    """A tab strip is as tall as its tallest page. Pinning the grid's full height as a
    minimum therefore set the floor for the whole window, and the short tabs sat above
    several hundred pixels of nothing. The width pin stays — that is what keeps all ten
    columns visible without a horizontal scrollbar."""
    w = gui_window()
    try:
        page = w.tab_inventory
        assert page.minimumSizeHint().height() < page.sizeHint().height() / 2, (
            "the inventory page still demands its whole natural height as a minimum, so "
            "every other tab inherits it")
        assert page.minimumSizeHint().width() >= page.sizeHint().width(), (
            "the width pin is gone — the grid would get a horizontal scrollbar")
    finally:
        w.close()


def test_the_grid_syncs_only_while_the_grid_is_showing(gui_window, monkeypatch):
    """The 1 Hz sync ran on the wrong tab entirely.

    `_inventory_visible` compared `currentIndex()` to `indexOf(widget(1))`, which is 1 by
    definition -- the Effects tab. So the grid refreshed while Effects was on screen and
    never while the user was looking at the grid. The sibling handler had already been
    fixed to dispatch on the widget; this one had not, which is why the tab strip carries
    a note saying never to key off an index.
    """
    mw, w = _window(gui_window)
    try:
        by_title = {w.tabs.tabText(i): i for i in range(w.tabs.count())}
        w.tabs.setCurrentIndex(by_title["Inventory"])
        assert w._inventory_visible() is True

        for other in ("Effects", "Player", "Patches", "Recipes"):
            w.tabs.setCurrentIndex(by_title[other])
            assert w._inventory_visible() is False, f"it thinks the grid shows on {other}"
    finally:
        w.close()


# --- the Effects panel is remembered between sessions (reported bug) ----------

def test_closing_records_every_effect_switch_and_number(gui_window, monkeypatch, tmp_path):
    """Reported: "effects aren't saved". Nothing wrote them anywhere -- only the window
    size was kept, and the profile they might have gone in is root-owned."""
    from terrariabonker.gui import main_window as mw
    from terrariabonker.gui import uistate

    monkeypatch.setattr(uistate, "_PATH", str(tmp_path / "window.json"))
    w = gui_window()
    w.cb_fishing.setChecked(True)
    w.cb_buff_sonar.setChecked(True)
    w.sp_bait.setValue(77)
    w.close()

    saved = uistate.load_effects()
    assert saved["cb_fishing"] is True and saved["cb_buff_sonar"] is True
    assert saved["sp_bait"] == 77
    assert saved["cb_potions"] is False, "an untouched switch must record as off, not vanish"
    assert set(mw.MainWindow._EFFECT_BOXES) <= set(saved), "a switch is not being saved"


def test_reopening_puts_the_switches_back(gui_window, monkeypatch, tmp_path):
    from terrariabonker.gui import uistate

    monkeypatch.setattr(uistate, "_PATH", str(tmp_path / "window.json"))
    uistate.save_effects({"cb_potions": True, "sp_potion_stack": 42, "cb_fishing": False})
    w = gui_window()
    assert not w.cb_potions.isChecked(), "premise: it starts off"
    w._restore_effects()
    assert w.cb_potions.isChecked()
    assert w.sp_potion_stack.value() == 42
    assert not w.cb_fishing.isChecked(), "an off switch must not come back on"


def test_the_number_is_restored_before_the_switch_that_reads_it(
        gui_window, monkeypatch, tmp_path):
    """A switch starts a watcher that reads the number beside it on its first round.

    Restoring the tick first would run one round at the default -- topping bait to 30 when
    the user had set 77, once, at every launch.
    """
    from terrariabonker.gui import uistate

    monkeypatch.setattr(uistate, "_PATH", str(tmp_path / "window.json"))
    uistate.save_effects({"cb_fishing": True, "sp_bait": 77})
    w = gui_window()
    seen = []
    w.cb_fishing.toggled.connect(lambda on: seen.append(w.sp_bait.value()))
    w._restore_effects()
    assert seen == [77], f"the switch fired while the number was still {seen}"


def test_nothing_is_restored_when_nothing_was_saved(gui_window, monkeypatch, tmp_path):
    """A first run must not tick anything on."""
    from terrariabonker.gui import uistate

    monkeypatch.setattr(uistate, "_PATH", str(tmp_path / "window.json"))
    w = gui_window()
    w._restore_effects()
    assert not any(getattr(w, n).isChecked() for n in w._EFFECT_BOXES)


# --- the recipe dialog fits its content (reported bug) ------------------------

def _recipe_dialog(qt_app, n_recipes=1, n_ingredients=1):
    from PyQt6.QtGui import QIcon

    from terrariabonker.gui.main_window import RecipeDialog

    recs = [{"out": 2, "n": 1, "tile": 18, "ing": [(9, 5)] * n_ingredients}
            for _ in range(n_recipes)]
    return RecipeDialog(2, recs, "made", lambda _i: QIcon())


def test_a_recipe_that_fits_on_screen_opens_without_a_scrollbar(qt_app):
    """Reported: the dialog "always opens too small".

    A QScrollArea reports a small fixed sizeHint whatever is inside it, so the dialog
    opened at its own minimum -- measured, content wanting 645px in a dialog that opened
    at 453 -- and every recipe with more than a couple of ingredients got a scrollbar over
    content that would have fitted.
    """
    from PyQt6.QtWidgets import QScrollArea

    d = _recipe_dialog(qt_app, n_recipes=2, n_ingredients=6)
    try:
        d.show()
        qt_app.processEvents()
        area = d.findChildren(QScrollArea)[0]
        assert area.viewport().height() >= area.widget().sizeHint().height(), \
            "the content is taller than the space given to it"
        assert area.verticalScrollBar().maximum() == 0, "it opened with a scrollbar"
    finally:
        d.close()


def test_a_bigger_recipe_opens_bigger(qt_app):
    """The size follows the content rather than being one fixed guess."""
    small = _recipe_dialog(qt_app, n_recipes=1, n_ingredients=1)
    big = _recipe_dialog(qt_app, n_recipes=3, n_ingredients=6)
    try:
        assert big.size().height() > small.size().height()
    finally:
        small.close()
        big.close()


def test_a_huge_recipe_stops_at_the_screen_and_scrolls_instead(qt_app):
    """Fitting to content cannot mean opening taller than the display: past the cap the
    scrollbar is the correct answer."""
    from terrariabonker.gui.main_window import RecipeDialog

    d = _recipe_dialog(qt_app, n_recipes=40, n_ingredients=8)
    try:
        avail = qt_app.primaryScreen().availableGeometry()
        assert d.size().height() <= int(avail.height() * RecipeDialog._MAX_SCREEN_FRACTION)
        assert d.size().width() <= int(avail.width() * RecipeDialog._MAX_SCREEN_FRACTION)
    finally:
        d.close()


def test_a_tiny_recipe_still_respects_the_minimum_width(qt_app):
    """Fitting to content must not make a one-ingredient recipe a sliver."""
    d = _recipe_dialog(qt_app, n_recipes=1, n_ingredients=1)
    try:
        assert d.size().width() >= d.minimumWidth()
    finally:
        d.close()
