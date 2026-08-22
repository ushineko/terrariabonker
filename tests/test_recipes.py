"""Recipe browsing queries against a fixture (no game/memory)."""

from terrariabonker import recipes as R


FIXTURE = {
    "recipes": [
        {"out": 8, "n": 3, "ing": [[23, 1], [9, 1]]},          # Torch <= Gel + Wood (hand)
        {"out": 24, "n": 1, "ing": [[9, 7]], "tile": 18},       # Wooden Sword <= Wood @ WB
        {"out": 35, "n": 1, "ing": [[22, 5]], "tile": 18},      # Iron Anvil <= Iron Bar @ WB
    ],
    "stations": {"18": "Work Bench"},
}


def test_by_output_and_using(monkeypatch):
    monkeypatch.setattr(R, "_CACHE", FIXTURE)
    # by output ItemID
    hits = R.by_output("24")
    assert len(hits) == 1 and hits[0]["out"] == 24
    # by output name resolves through the names map (Torch is item 8)
    assert any(r["out"] == 8 for r in R.by_output("Torch"))
    # "uses" reverse lookup: Wood (9) is in Torch and Wooden Sword
    assert {r["out"] for r in R.using("9")} == {8, 24}


def test_station_name(monkeypatch):
    monkeypatch.setattr(R, "_CACHE", FIXTURE)
    assert R.station_name(18) == "Work Bench"          # from the cache
    assert R.station_name(26) == "Demon/Crimson Altar"  # not-crafted supplement
    assert R.station_name(9999) == "Tile #9999"        # unknown fallback
