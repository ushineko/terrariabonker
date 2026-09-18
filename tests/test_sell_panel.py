"""The panel side of auto-selling: the whitelist menu, the marking, the tick (spec 048).

The whitelist is edited from the inventory grid rather than a separate picker, so these
tests are about the grid: right-click toggles the *type* in the slot, the cell shows it,
and nothing about left-click (which opens the item editor) changes.
"""


from terrariabonker import argv as client




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






















# --- the list has to be removable from outside the grid ---------------------
# Adding is a right-click in the inventory, but a whitelisted item is sold on the next
# tick and never sits in the grid long enough to right-click again. Without a second
# place to see the list, it is add-only and the only way out is the CLI.





