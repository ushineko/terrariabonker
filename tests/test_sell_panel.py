"""The panel side of auto-selling: the whitelist menu, the marking, the tick (spec 048).

The whitelist is edited from the inventory grid rather than a separate picker, so these
tests are about the grid: right-click toggles the *type* in the slot, the cell shows it,
and nothing about left-click (which opens the item editor) changes.
"""


from terrariabonker.gui import client


def _settled(w):
    """Mark the world as settled: the panel writes nothing to an unsettled world, and
    these tests are about the tick itself, not the gate (see test_world_change.py)."""
    from terrariabonker.gui.main_window import WORLD_SETTLE_POLLS
    w._world_settle = WORLD_SETTLE_POLLS
    return w


def test_sell_argv_never_watches():
    """The worker must not block: the panel owns the cadence, as it does everywhere else."""
    assert "--watch" not in client.sell_argv()
    assert client.sell_argv()[0] == "sell-tick"


def test_sell_list_argv_carries_the_toggle():
    assert client.sell_list_argv(add=12)[-2:] == ["--add", "12"]
    assert client.sell_list_argv(remove=12)[-2:] == ["--remove", "12"]
    assert "--add" not in client.sell_list_argv()


def test_parse_sell_list_reads_the_reply():
    raw = '[sell] whitelist: Iron Ore (12)\n{"whitelist": [12, 7]}'
    assert client.parse_sell_list(raw) == {12, 7}


def test_parse_sell_list_rejects_an_unparseable_reply():
    assert client.parse_sell_list("not json") is None


def test_right_click_on_an_empty_slot_does_nothing(gui_window):
    """No type, nothing to whitelist -- and a menu offering to sell nothing is worse
    than no menu."""
    w, calls = gui_window(record_calls=True)
    w._all_rows = [{"slot": 3, "type": 0}]
    calls.clear()
    w._on_cell_menu(3, w._cells[3].rect().center())
    assert calls == []


def test_whitelist_marking_follows_the_worker_not_the_click(gui_window):
    """A write that did not land must not leave the grid claiming an item is being sold.
    The reply is what repaints the cell."""
    w = gui_window()
    w._all_rows = [{"slot": 0, "type": 12, "stack": 5}]
    w._rendered = {0: {"slot": 0, "type": 12, "stack": 5}}
    assert 12 not in w._sell_whitelist
    w._apply_sell_whitelist({12})
    assert 12 in w._sell_whitelist
    assert "Auto-sell: on" in w._cells[0].toolTip()


def test_unmarking_repaints_the_cell(gui_window):
    w = gui_window()
    w._all_rows = [{"slot": 0, "type": 12, "stack": 5}]
    w._rendered = {0: {"slot": 0, "type": 12, "stack": 5}}
    w._apply_sell_whitelist({12})
    w._apply_sell_whitelist(set())
    assert "Auto-sell: on" not in w._cells[0].toolTip()


def test_the_tick_does_not_stack_up_requests(gui_window):
    """Two rounds in flight at once would sell the same slot twice."""
    w = _settled(gui_window())
    sent = []
    w.helper.available = True
    w.helper.request = lambda argv, done: (sent.append(argv), True)[1]
    w._tick_sell()
    w._tick_sell()
    assert len(sent) == 1, "the second tick must be skipped while one is in flight"


def test_turning_the_switch_off_stops_the_timer(gui_window):
    w = gui_window()
    w._set_sell_watch(True)
    assert w._sell_timer.isActive()
    w._set_sell_watch(False)
    assert not w._sell_timer.isActive()
    assert w._sell_inflight is False


def test_the_switch_is_saved_between_sessions(gui_window):
    w = gui_window()
    assert "cb_sell" in w._EFFECT_BOXES


def test_switching_on_says_so_when_the_helper_is_missing(gui_window):
    """A cheat that silently does nothing is indistinguishable from a broken one, and
    that is exactly what "I ticked it and nothing happened" looks like."""
    w = gui_window()
    w.helper.available = False
    w.log.clear()
    w._set_sell_watch(True)
    assert "helper is not running" in w.log.toPlainText()


def test_the_missing_helper_is_reported_once_not_every_tick(gui_window):
    """It ticks twice a second; saying it every time would bury the log."""
    w = gui_window()
    w.helper.available = False
    w._set_sell_watch(True)
    w.log.clear()
    for _ in range(10):
        w._tick_sell()
    assert w.log.toPlainText().strip() == ""


def test_switching_on_ticks_immediately(gui_window):
    """Half a second of nothing reads as "it isn't working"."""
    w = _settled(gui_window())
    sent = []
    w.helper.available = True
    w.helper.request = lambda argv, done: (sent.append(argv), True)[1]
    w._set_sell_watch(True)
    assert sent == [["sell-tick", "--json"]]


def test_an_empty_round_explains_itself_once(gui_window):
    w = _settled(gui_window())
    w.helper.available = True
    captured = {}
    w.helper.request = lambda argv, done: (captured.setdefault("done", done), True)[1]
    w._set_sell_watch(True)
    w.log.clear()
    captured["done"]('{"sold": [], "skipped": [], "copper": 0, '
                     '"destination": "bank", "reachable": {"reachable": true}}')
    assert "nothing on the sell list" in w.log.toPlainText()
    w.log.clear()
    captured["done"]('{"sold": [], "skipped": [], "copper": 0, '
                     '"destination": "bank", "reachable": {"reachable": true}}')
    assert w.log.toPlainText().strip() == "", "said once, not every round"


# --- the list has to be removable from outside the grid ---------------------
# Adding is a right-click in the inventory, but a whitelisted item is sold on the next
# tick and never sits in the grid long enough to right-click again. Without a second
# place to see the list, it is add-only and the only way out is the CLI.

def test_the_sell_list_widget_shows_what_is_whitelisted(gui_window):
    w = gui_window()
    w._apply_sell_whitelist({1309, 216})
    shown = [w.lst_sell.item(i).text() for i in range(w.lst_sell.count())]
    assert shown == ["Shackle", "Slime Staff"], "listed, and sorted by name"


def test_an_item_can_be_removed_without_ever_appearing_in_the_inventory(gui_window):
    """The whole point: nothing of this type is in the grid, and it must still come off."""
    w = gui_window()
    sent = []
    w.helper.available = True
    w.helper.request = lambda argv, done: (sent.append(argv), True)[1]
    w._all_rows = []                     # nothing of this type in the inventory at all
    w._apply_sell_whitelist({1309})
    w.lst_sell.itemDoubleClicked.emit(w.lst_sell.item(0))
    assert sent == [["sell-list", "--json", "--remove", "1309"]]


def test_the_list_empties_when_the_whitelist_does(gui_window):
    w = gui_window()
    w._apply_sell_whitelist({1309})
    w._apply_sell_whitelist(set())
    assert w.lst_sell.count() == 0
