"""Prefix (modifier) names, quality, and per-item-class applicability."""

from terrariabonker import prefixes


def test_name_lookup():
    assert prefixes.name(85) == "Fabled"       # summon
    assert prefixes.name(81) == "Legendary"    # melee
    assert prefixes.name(65) == "Warding"      # accessory
    assert prefixes.name(0) == ""              # none


def test_quality():
    assert prefixes.quality(0) == "none"
    assert prefixes.quality(39) == "bad"       # Broken
    assert prefixes.quality(59) == "good"      # Godly
    assert prefixes.quality(14) == "neutral"   # Heavy


def test_valid_prefixes_summon_includes_summon_pool_not_melee_size():
    ids = prefixes.valid_prefixes({"summon": True})
    assert 85 in ids                           # Fabled (summon)
    assert 51 in ids                           # universal weapon modifier
    assert 1 not in ids                        # Large (melee size) excluded
    assert 65 not in ids                       # Warding (accessory) excluded


def test_valid_prefixes_accessory_is_accessory_only():
    ids = prefixes.valid_prefixes({"accessory": True})
    assert 65 in ids and 72 in ids             # Warding, Menacing
    assert 85 not in ids                       # no weapon modifiers
    assert 1 not in ids


def test_valid_prefixes_melee_has_size_and_universal_not_magic():
    ids = prefixes.valid_prefixes({"melee": True})
    assert 1 in ids                            # Large (size)
    assert 81 in ids                           # Legendary
    assert 36 in ids                           # Keen (universal)
    assert 26 not in ids                       # Mystic (magic) excluded


def test_has_categories():
    assert prefixes.has_categories({"magic": True})
    assert not prefixes.has_categories({"melee": False})
    assert not prefixes.has_categories({})
