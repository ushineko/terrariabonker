"""Sprite cache: referenced-item set, cache paths, and content-dir persistence."""

from terrariabonker import recipes, sprites


def test_referenced_item_ids_union_of_outputs_and_ingredients(monkeypatch):
    fake = {"recipes": [
        {"out": 8, "n": 3, "ing": [[23, 1], [9, 1]]},
        {"out": 100, "n": 1, "ing": [[8, 5]]},
    ], "stations": {}}
    monkeypatch.setattr(recipes, "_CACHE", fake)
    ids = sprites.referenced_item_ids()
    assert ids == {8, 23, 9, 100}                 # outputs (8,100) + ingredients (23,9,8)


def test_cache_paths_are_version_scoped():
    assert sprites.cache_dir("1.4.5.7").endswith("/sprites/1.4.5.7")
    assert sprites.icon_path(42, "1.4.5.7").endswith("/sprites/1.4.5.7/Item_42.png")


def test_content_dir_persists_and_reloads(monkeypatch, tmp_path):
    paths_file = tmp_path / "paths.json"
    monkeypatch.setattr(sprites, "_PATHS_FILE", str(paths_file))
    images = tmp_path / "Content" / "Images"
    images.mkdir(parents=True)

    class FakeMem:
        def exe_path(self):
            return str(tmp_path / "Terraria.exe")

    # first call derives from the running game and persists
    assert sprites.content_images_dir(FakeMem()) == str(images)
    assert paths_file.exists()
    # second call resolves from the persisted path with no mem (game closed)
    assert sprites.content_images_dir(None) == str(images)


def test_content_dir_unknown_returns_none(monkeypatch, tmp_path):
    monkeypatch.setattr(sprites, "_PATHS_FILE", str(tmp_path / "none.json"))
    assert sprites.content_images_dir(None) is None
