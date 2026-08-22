"""Sprite cache: referenced-item set, cache paths, content-dir persistence, de-animation."""

import numpy as np
from PIL import Image

from terrariabonker import names, recipes, sprites


def _strip(width, frame_h, frames, content_h):
    """A synthetic vertical animation: ``frames`` blocks of ``content_h`` opaque rows,
    each in a ``frame_h``-tall slot (the remainder transparent, a separator)."""
    h = frame_h * frames
    a = np.zeros((h, width, 4), dtype=np.uint8)
    for k in range(frames):
        top = k * frame_h
        a[top:top + content_h, :, :] = 255           # opaque content
    return Image.fromarray(a, "RGBA")


def test_deanimate_crops_vertical_strip_to_first_frame():
    img = _strip(width=10, frame_h=12, frames=4, content_h=10)   # 10x48, 4 frames
    out = sprites._deanimate(img)
    assert out.size == (10, 12)                       # cropped to one frame


def test_deanimate_leaves_single_frame_tall_item():
    # one content block in a tall image (like a staff): not an animation -> unchanged
    a = np.zeros((30, 10, 4), dtype=np.uint8)
    a[5:25, :, :] = 255
    img = Image.fromarray(a, "RGBA")
    assert sprites._deanimate(img).size == (10, 30)


def test_deanimate_leaves_non_strip_unchanged():
    img = Image.new("RGBA", (32, 20), (255, 0, 0, 255))   # h < 2*w
    assert sprites._deanimate(img).size == (32, 20)


def test_composite_chest_assembles_four_tiles(monkeypatch):
    # a synthetic Containers sheet: style 1's four 16x16 tiles are R/G/B/white
    sheet = Image.new("RGBA", (72, 38), (0, 0, 0, 0))
    fx = 1 * 36
    for sx, sy, col in ((0, 0, (255, 0, 0, 255)), (18, 0, (0, 255, 0, 255)),
                        (0, 18, (0, 0, 255, 255)), (18, 18, (255, 255, 255, 255))):
        sheet.paste(Image.new("RGBA", (16, 16), col), (fx + sx, sy))
    out = sprites._composite_chest(sheet, 1)
    assert out.size == (32, 32)
    assert out.getpixel((0, 0)) == (255, 0, 0, 255)        # top-left tile
    assert out.getpixel((16, 0)) == (0, 255, 0, 255)       # top-right, no padding gap
    assert out.getpixel((0, 16)) == (0, 0, 255, 255)       # bottom-left
    assert out.getpixel((16, 16)) == (255, 255, 255, 255)  # bottom-right


def test_all_item_ids_superset_of_referenced(monkeypatch):
    monkeypatch.setattr(recipes, "_CACHE",
                        {"recipes": [{"out": 8, "n": 1, "ing": [[23, 1]]}], "stations": {}})
    ids = sprites.all_item_ids()
    assert set(names._NAMES).issubset(ids)            # every named item is covered
    assert {8, 23}.issubset(ids)                      # recipe items too


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
