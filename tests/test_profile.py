"""Cross-session desired-config profile: round-trip of cheats and item edits.

Item edits are keyed by the item *type*, not the slot it happened to occupy, and hold only
the fields Terraria regenerates from the type on load. Type, stack and prefix are written
into the save by the game itself, so recording them produced restore failures about items
whose only change was a prefix that had survived on its own (spec 038).
"""

from terrariabonker import profile


def test_profile_roundtrip(monkeypatch, tmp_path):
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    profile.set_cheat("mining", True, 0.2)
    profile.set_cheat("teleport", True, None)     # valueless cheat
    profile.set_item_edit(100, {"damage": 50})
    profile.clear_item(6)

    assert profile.cheats() == {"mining": 0.2, "teleport": None}
    assert profile.item_edits() == {100: {"damage": 50}}
    assert profile.empty_slots() == [6]


def test_profile_disable_removes_cheat(monkeypatch, tmp_path):
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    profile.set_cheat("reach", True, 20)
    profile.set_cheat("reach", False)
    assert "reach" not in profile.cheats()


def test_profile_empty_when_absent(monkeypatch, tmp_path):
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "none.json"))
    assert profile.cheats() == {} and profile.item_edits() == {}


def test_only_the_regenerated_fields_are_kept(monkeypatch, tmp_path):
    """Type, stack and prefix persist in the save; storing them achieves nothing."""
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    profile.set_item_edit(100, {"damage": 50, "prefix": 65, "stack": 99, "type": 100})
    assert profile.item_edits() == {100: {"damage": 50}}


def test_an_edit_with_nothing_to_restore_is_not_stored(monkeypatch, tmp_path):
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    profile.set_item_edit(100, {"damage": 50})
    profile.set_item_edit(100, {"prefix": 65})     # nothing restorable left
    assert profile.item_edits() == {}


def test_a_slot_keyed_profile_is_migrated(monkeypatch, tmp_path):
    """The real profile that prompted this: six accessories whose only edit was a prefix,
    stored alongside every default the edit dialog happened to submit."""
    import json

    path = tmp_path / "profile.json"
    path.write_text(json.dumps({"cheats": {"mining": 0.2}, "items": {
        "0": {"type": 5688, "damage": 45, "use_time": 30, "prefix": 84, "stack": 1},
        "31": {"type": 708, "damage": -1, "use_time": 100, "prefix": 65},
        "46": {"type": 4758, "stack": 1},
        "12": {"type": 0},
    }}))
    monkeypatch.setattr(profile, "_PATH", str(path))

    edits = profile.item_edits()
    assert edits[5688] == {"damage": 45, "use_time": 30}, "prefix/stack/type carried over"
    assert 708 in edits, "the field values are kept; defaults are pruned at restore"
    assert 4758 not in edits, "a plain give has nothing to restore"
    assert profile.empty_slots() == [12]
    assert profile.cheats() == {"mining": 0.2}
