"""The GUI modules must at least import.

Nothing else in the suite imports them (they need PyQt6), so a bad import line in
main_window only showed up when launching the app by hand. Skipped when PyQt6 is
absent so the rest of the suite still runs headless anywhere.
"""

import pytest

pytest.importorskip("PyQt6.QtWidgets")


def test_gui_modules_import():
    from terrariabonker.gui import client, helper, invgrid, item_dialog, main_window
    assert main_window.MainWindow is not None
    assert helper.Helper is not None
    assert client.inventory_argv() and invgrid.SECTIONS and item_dialog.ItemEditDialog
