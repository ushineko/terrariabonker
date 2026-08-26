"""The Compendium tab's interaction wiring.

A double-click made both `doubleClicked` and `activated` fire, so the entry dialog opened
twice and had to be dismissed twice. A re-entrancy flag does not fix it: the dialog runs a
nested event loop, so the second signal is delivered after the dialog closes, when the flag
has already been cleared. Only one signal may be connected.
"""

import os

import pytest

pytest.importorskip("PyQt6.QtWidgets")
os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

from PyQt6.QtGui import QIcon                              # noqa: E402
from PyQt6.QtWidgets import QApplication, QPushButton      # noqa: E402

from terrariabonker.gui import compendium                   # noqa: E402

CATALOG = {
    "items": [{"id": 54, "name": "Hermes Boots", "kind": "Accessory", "tooltip": "fast",
               "stats": {"rare": 1}, "wiki": "https://example.invalid/Hermes_Boots"}],
    "npcs": [{"id": 4, "name": "Eye of Cthulhu", "kind": "Boss", "npc": True,
              "stats": {"life": 2800, "damage": 15, "defense": 12}, "wiki": ""}],
}


@pytest.fixture
def app():
    yield QApplication.instance() or QApplication([])


@pytest.fixture
def tab(app, monkeypatch):
    opened = []
    monkeypatch.setattr(compendium, "EntryDialog",
                        lambda *a, **k: type("D", (), {"exec": lambda self: opened.append(1)})())
    t = compendium.CompendiumTab(None, lambda done, refresh=False: done(CATALOG), lambda i: None,
                                 lambda i: QIcon(), lambda m: None)
    t.ensure_loaded()
    t._opened = opened
    return t


def test_a_double_click_opens_exactly_one_dialog(tab):
    """Emitting both signals is what a real double-click does on this style."""
    idx = tab._proxy.index(0, 0)
    tab.view.doubleClicked.emit(idx)
    tab.view.activated.emit(idx)
    assert len(tab._opened) == 1


def test_rows_carry_name_kind_stats_and_id(tab):
    assert tab._model.columnCount() == len(compendium.COLUMNS)
    names = {tab._model.item(r, 0).text() for r in range(tab._model.rowCount())}
    assert names == {"Hermes Boots", "Eye of Cthulhu"}


def test_kind_filter_narrows_the_list(tab):
    tab._proxy.set_kind("Boss")          # a real NPC kind, not the phase 1 placeholder
    assert tab._proxy.rowCount() == 1
    tab._proxy.set_kind(compendium.ALL_KINDS)
    assert tab._proxy.rowCount() == 2


def test_search_matches_name_or_id(tab):
    tab._proxy.set_query("hermes")
    assert tab._proxy.rowCount() == 1
    tab._proxy.set_query("#4")
    assert tab._proxy.rowCount() == 1
    tab._proxy.set_query("nothing here")
    assert tab._proxy.rowCount() == 0


def test_the_id_column_sorts_numerically(tab):
    """As text, 54 would sort before 4."""
    from PyQt6.QtCore import Qt
    tab._proxy.sort(compendium.ID_COLUMN, Qt.SortOrder.AscendingOrder)
    first = tab._proxy.index(0, compendium.ID_COLUMN).data()
    assert first == "4", f"expected the numerically smallest id first, got {first}"


def test_stat_columns_are_blank_for_absent_values_but_still_sort(app, monkeypatch):
    """A weapon and a blockless NPC must not both read '-1'; blanks still need an order."""
    from PyQt6.QtCore import Qt
    monkeypatch.setattr(compendium, "EntryDialog", lambda *a, **k: None)
    catalog = {"items": [
        {"id": 1, "name": "Blade", "kind": "Weapon", "stats": {"damage": 50}, "wiki": ""},
        {"id": 2, "name": "Rock", "kind": "Material", "stats": {"damage": -1}, "wiki": ""},
    ], "npcs": []}
    t = compendium.CompendiumTab(None, lambda done, refresh=False: done(catalog), lambda i: None,
                                 lambda i: QIcon(), lambda m: None)
    t.ensure_loaded()
    dmg = compendium.COLUMNS.index("Damage")
    texts = {t._model.item(r, 0).text(): t._model.item(r, dmg).text()
             for r in range(t._model.rowCount())}
    assert texts == {"Blade": "50", "Rock": ""}
    t._proxy.sort(dmg, Qt.SortOrder.DescendingOrder)
    assert t._proxy.index(0, 0).data() == "Blade"


def test_the_last_column_does_not_swallow_the_slack(tab):
    """QHeaderView stretches the last section by default, which handed ID all the spare
    width and stranded its header away from its right-aligned values."""
    assert tab.view.header().stretchLastSection() is False


def test_numeric_headers_are_right_aligned_like_their_values(tab):
    from PyQt6.QtCore import Qt
    for col in range(2, len(compendium.COLUMNS)):
        align = tab._model.headerData(col, Qt.Orientation.Horizontal,
                                      Qt.ItemDataRole.TextAlignmentRole)
        assert align & Qt.AlignmentFlag.AlignRight, compendium.COLUMNS[col]


def test_give_is_offered_for_items_and_withheld_from_npcs(app):
    """The NPC test must not key on the kind string: real kinds are Boss/Monster/…"""
    from terrariabonker.gui.compendium import EntryDialog

    item = {"id": 3507, "name": "Zenith", "kind": "Weapon", "stats": {"damage": 190}}
    npc = {"id": 4, "name": "Eye of Cthulhu", "kind": "Boss", "npc": True,
           "stats": {"life": 2800}}

    def buttons(entry):
        dlg = EntryDialog(None, entry, None, lambda _i: None)
        return {b.text() for b in dlg.findChildren(QPushButton)}

    assert "Give" in buttons(item)
    assert "Give" not in buttons(npc), "an NPC cannot be put in the inventory"


def test_the_kind_dropdown_widens_for_kinds_added_after_it_is_shown(app):
    """The catalog loads lazily, so Qt's adjust-on-first-show left every kind truncated."""
    tab = compendium.CompendiumTab(None, lambda cb, refresh=False: None, lambda _i: None,
                                   lambda _i: QIcon(), lambda _m: None)
    narrow = tab.kind.sizeHint().width()
    tab._fill({"items": [], "npcs": [
        {"id": 1, "name": "x", "kind": "A Very Long Kind Label", "npc": True, "stats": {}}]})
    assert tab.kind.sizeHint().width() > narrow, "the dropdown did not grow to fit"


def test_an_npc_variant_takes_its_sprite_from_its_type_not_its_id(app):
    """Every coloured slime and every Hornet is a separate netID sharing one base type,
    and the game ships one sheet per type. Asking by id would request NPC_-65.xnb."""
    asked = []
    tab = compendium.CompendiumTab(None, lambda cb, refresh=False: None, lambda _i: None,
                                   lambda _i: QIcon(), lambda _m: None,
                                   None,
                                   lambda t, nid=None: (asked.append((t, nid)),
                                                        QIcon())[1])
    tab._fill({"items": [], "npcs": [
        {"id": -65, "name": "Big Hornet Stingy", "kind": "Monster", "npc": True,
         "stats": {"type": 235, "life": 45}}]})
    assert asked == [(235, -65)], "the base type must drive the sheet, the netID the tint"


def test_an_item_row_still_uses_the_item_icon(app):
    asked = []
    tab = compendium.CompendiumTab(None, lambda cb, refresh=False: None, lambda _i: None,
                                   lambda i: (asked.append(i), QIcon())[1],
                                   lambda _m: None, None, lambda _t, _n=None: QIcon())
    tab._fill({"items": [{"id": 3507, "name": "Zenith", "kind": "Weapon", "stats": {}}],
               "npcs": []})
    assert asked == [3507]


def test_loading_reports_progress_from_fetch_through_to_the_last_row(app):
    """The wait is the privileged catalog read plus ~7,000 rows of widgets, and neither
    was covered: the only progress bar tracked sprite extraction, which is usually
    already cached and so never appeared at all."""
    calls = []
    fetches = []
    tab = compendium.CompendiumTab(None,
                                   lambda cb, refresh=False: fetches.append(cb),
                                   lambda _i: None,
                                   lambda _i: QIcon(), lambda _m: None, None,
                                   lambda _t, _n=None: QIcon(),
                                   lambda text, done=0, total=0: calls.append(
                                       (text, done, total)))
    tab.ensure_loaded()
    assert calls and calls[0][0] and calls[0][2] == 0, "no indeterminate bar during fetch"

    npcs = [{"id": i, "name": f"n{i}", "kind": "Monster", "npc": True,
             "stats": {"type": 1}} for i in range(1200)]
    fetches[0]({"items": [], "npcs": npcs})

    counted = [c for c in calls if c[2] > 0]
    assert counted, "no per-row progress while building"
    assert counted[-1][1] == counted[-1][2] == 1200, "progress never reached the end"
    assert calls[-1][0] is None, "the bar was left on screen"
    assert tab._model.rowCount() == 1200


def test_a_failed_fetch_takes_the_progress_bar_down(app):
    calls = []
    fetches = []
    tab = compendium.CompendiumTab(None,
                                   lambda cb, refresh=False: fetches.append(cb),
                                   lambda _i: None,
                                   lambda _i: QIcon(), lambda _m: None, None,
                                   lambda _t, _n=None: QIcon(),
                                   lambda text, done=0, total=0: calls.append(
                                       (text, done, total)))
    tab.ensure_loaded()
    fetches[0](None)
    assert calls[-1][0] is None, "bar stuck on screen after the catalog failed to load"


def test_the_rescan_button_clears_the_catalog_and_asks_for_a_fresh_read(app):
    """Recipes has had 'Re-extract from game' since v0.9; the Compendium's catalog is
    cached per build the same way and needs the same escape hatch."""
    seen = []
    tab = compendium.CompendiumTab(None,
                                   lambda cb, refresh=False: seen.append((cb, refresh)),
                                   lambda _i: None, lambda _i: QIcon(), lambda _m: None,
                                   None, lambda _t, _n=None: QIcon())
    tab.ensure_loaded()
    seen[0][0]({"items": [], "npcs": [
        {"id": 1, "name": "n", "kind": "Monster", "npc": True, "stats": {"type": 1}}]})
    assert tab._model.rowCount() == 1
    assert seen[0][1] is False, "the first read should use the cache"

    tab.reload()
    assert tab._model.rowCount() == 0, "stale rows survived the re-scan"
    assert tab.kind.count() == 1, "the kind filter kept stale kinds"
    assert seen[1][1] is True, "re-scan did not ask the game to be re-read"
    seen[1][0]({"items": [], "npcs": [
        {"id": 2, "name": "m", "kind": "Boss", "npc": True, "stats": {"type": 2}}]})
    assert tab._model.rowCount() == 1


def test_the_entry_count_follows_the_kind_filter(tab):
    """Filtering to Boss while the label still read "6954 of 6954 entries" was visible in
    a README screenshot: the count was wired to the search box but not to the dropdown."""
    tab._fill(CATALOG)
    total = tab._model.rowCount()
    assert "%d of %d entries" % (total, total) in tab.status.text()

    idx = next(i for i in range(tab.kind.count()) if tab.kind.itemText(i) == "Boss")
    tab.kind.setCurrentIndex(idx)
    shown = tab._proxy.rowCount()
    assert shown < total, "the fixture no longer exercises a narrowing filter"
    assert tab.status.text().startswith("%d of %d" % (shown, total))
    assert "match" in tab.status.text()
