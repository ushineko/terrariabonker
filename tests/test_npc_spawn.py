"""The spawn path and its boss gate (spec 035, phase 2).

Spawning a boss is the one destructive thing the Compendium can do, so the gate is what
these tests are really about: a misclick in a 6,958-row list must not be able to end a
character, and a cancel must actually cancel.
"""


import pytest

pytest.importorskip("PyQt6.QtWidgets")

from PyQt6.QtWidgets import QMessageBox, QPushButton                 # noqa: E402

from terrariabonker.gui import compendium                            # noqa: E402

BUNNY = {"id": 46, "name": "Bunny", "kind": "Critter", "npc": True,
         "stats": {"life": 5, "damage": 0}}
BOSS = {"id": 4, "name": "Eye of Cthulhu", "kind": "Boss", "npc": True,
        "stats": {"life": 2800, "damage": 15}}


@pytest.fixture
def tab(qt_app):
    spawned = []
    t = compendium.CompendiumTab(None, lambda cb, refresh=False: None, lambda _i: None,
                                 lambda _i: None, lambda _m: None,
                                 lambda nid, dist: spawned.append((nid, dist)))
    t.spawned = spawned
    return t


def test_a_harmless_npc_spawns_immediately(tab):
    tab._spawn(BUNNY, 25)
    assert tab.spawned == [(46, 25)]
    assert tab.gate.isHidden(), "no countdown for something that is not a boss"


def test_a_boss_declined_at_the_confirmation_never_spawns(tab, monkeypatch):
    monkeypatch.setattr(QMessageBox, "question",
                        staticmethod(lambda *a, **k: QMessageBox.StandardButton.Cancel))
    tab._spawn(BOSS, 25)
    assert tab.spawned == []
    assert tab._pending is None


def test_a_confirmed_boss_counts_down_before_spawning(tab, monkeypatch):
    monkeypatch.setattr(QMessageBox, "question",
                        staticmethod(lambda *a, **k: QMessageBox.StandardButton.Yes))
    tab._spawn(BOSS, 30)
    assert tab.spawned == [], "spawned instantly — the countdown did not gate it"
    assert not tab.gate.isHidden()
    for _ in range(compendium.BOSS_COUNTDOWN - 1):
        tab._tick()
        assert tab.spawned == []
    tab._tick()                                  # the last second elapses
    assert tab.spawned == [(4, 30)]
    assert tab.gate.isHidden()


def test_cancelling_mid_countdown_spawns_nothing(tab, monkeypatch):
    monkeypatch.setattr(QMessageBox, "question",
                        staticmethod(lambda *a, **k: QMessageBox.StandardButton.Yes))
    tab._spawn(BOSS, 25)
    tab._tick()
    tab.cancel_spawn()
    assert tab.spawned == []
    assert tab._pending is None and tab.gate.isHidden()


def test_a_second_boss_request_replaces_the_first_countdown(tab, monkeypatch):
    """Two pending countdowns would spawn two bosses from one visible Cancel button."""
    monkeypatch.setattr(QMessageBox, "question",
                        staticmethod(lambda *a, **k: QMessageBox.StandardButton.Yes))
    tab._spawn(BOSS, 25)
    first = tab._countdown
    tab._spawn(BOSS, 40)
    assert tab._countdown is not first
    assert not first.isActive(), "the first countdown was left running"
    for _ in range(compendium.BOSS_COUNTDOWN):
        tab._tick()
    assert tab.spawned == [(4, 40)], "only the most recent request should fire"


def test_the_spawn_button_is_offered_for_npcs_only(qt_app):
    def buttons(entry):
        dlg = compendium.EntryDialog(None, entry, None, lambda _i: None,
                                     lambda _e, _d: None)
        return {b.text() for b in dlg.findChildren(QPushButton)}

    assert "Spawn" in buttons(BUNNY)
    assert "Spawn boss…" in buttons(BOSS), "a boss must be visibly gated on the button"
    item = {"id": 3507, "name": "Zenith", "kind": "Weapon", "stats": {}}
    assert not {b for b in buttons(item) if b.startswith("Spawn")}
