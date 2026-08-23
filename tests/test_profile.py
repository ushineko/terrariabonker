"""Cross-session desired-config profile: round-trip of cheats and item edits."""

from terrariabonker import profile


def test_profile_roundtrip(monkeypatch, tmp_path):
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    profile.set_cheat("mining", True, 0.2)
    profile.set_cheat("teleport", True, None)     # valueless cheat
    profile.set_item(5, {"type": 100, "damage": 50})
    profile.clear_item(6)

    assert profile.cheats() == {"mining": 0.2, "teleport": None}
    assert profile.items()["5"] == {"type": 100, "damage": 50}
    assert profile.items()["6"] == {"type": 0}    # empty marker


def test_profile_disable_removes_cheat(monkeypatch, tmp_path):
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    profile.set_cheat("reach", True, 20)
    profile.set_cheat("reach", False)
    assert "reach" not in profile.cheats()


def test_profile_empty_when_absent(monkeypatch, tmp_path):
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "none.json"))
    assert profile.cheats() == {} and profile.items() == {}
