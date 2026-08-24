"""Cropping NPC sheets to their first frame (spec 035, phase 3).

NPC sheets are vertical strips of equal frames, and the only exact way to know how many
is the game's own `Main.npcFrameCount`. The item de-animator guesses from the shape —
"height >= 2*width, split into evenly spaced content blocks" — which is right for tall
item strips and wrong for wide NPCs.
"""

import json

import pytest

from terrariabonker import sprites

Image = pytest.importorskip("PIL.Image")


def _sheet(w, h):
    return Image.new("RGBA", (w, h), (0, 0, 0, 0))


def test_a_strip_is_cropped_to_one_frame():
    assert sprites._first_frame(_sheet(48, 280), 7).size == (48, 40)
    assert sprites._first_frame(_sheet(40, 1456), 26).size == (40, 56)


def test_a_wide_two_frame_sheet_is_cropped_where_the_item_rule_would_not():
    """Blue Slime is 32x52: two frames of 26, but only 1.6x taller than it is wide, so
    the item de-animator leaves it whole."""
    sheet = _sheet(32, 52)
    assert sprites._first_frame(sheet, 2).size == (32, 26)
    assert sprites._deanimate(sheet).size == (32, 52), "premise of this test changed"


def test_a_genuinely_single_frame_sheet_is_left_alone():
    """Moon Lord is 573x804 and the game says one frame; slicing it would be wrong."""
    assert sprites._first_frame(_sheet(573, 804), 1).size == (573, 804)


def test_a_few_rows_of_padding_are_tolerated():
    """Duke Fishron is 1298 tall over 8 frames and Skeletron Prime 940 over 6. Refusing
    anything that did not divide exactly left both as whole strips on screen."""
    assert sprites._first_frame(_sheet(202, 1298), 8).size == (202, 162)
    assert sprites._first_frame(_sheet(140, 940), 6).size == (140, 156)


def test_a_count_that_implies_an_absurd_frame_is_ignored():
    """A frame a pixel or two tall means the count is not describing this sheet."""
    assert sprites._first_frame(_sheet(48, 10), 7).size == (48, 10)


def test_a_missing_count_leaves_the_sheet_alone():
    assert sprites._first_frame(_sheet(48, 280), 0).size == (48, 280)


def test_npc_and_item_icons_do_not_collide():
    """An NPC type and an ItemID are different things wearing the same small integers."""
    assert sprites.npc_icon_path(46) != sprites.icon_path(46)


def test_draw_data_round_trips(tmp_path, monkeypatch):
    path = tmp_path / "npcframes.json"
    monkeypatch.setattr(sprites, "_NPC_FRAMES_FILE", str(path))
    tints = {-3: {"type": 1, "color": [0, 220, 40, 100]}}
    sprites.save_npc_draw_data({1: 2, 46: 7}, tints)
    frames, got = sprites.load_npc_draw_data()
    assert frames == {1: 2, 46: 7}
    assert got == tints
    assert set(json.loads(path.read_text())) == {"frames", "tints"}


def test_a_missing_frame_file_is_not_an_error(tmp_path, monkeypatch):
    monkeypatch.setattr(sprites, "_NPC_FRAMES_FILE", str(tmp_path / "nope.json"))
    assert sprites.load_npc_draw_data() == ({}, {})
    assert sprites.load_npc_frame_counts() == {}


def test_a_tint_is_added_to_the_neutral_sheet_not_multiplied_in():
    """A Blue Slime's gel is neutral grey; multiplying comes out too dark to read at
    40px, so the game's additive shape is what is reproduced."""
    grey = Image.new("RGBA", (4, 4), (117, 117, 117, 255))
    out = sprites._tinted(grey, [0, 80, 255, 100]).getpixel((0, 0))
    assert out[2] > 200 and out[0] < 80, f"not recognisably blue: {out}"
    assert out[2] > 117 * 0.45, "a multiply would have darkened it"


def test_a_tinted_variant_has_its_own_icon_path():
    """Green, Purple and Blue Slime share type 1's sheet and differ only by netID."""
    assert sprites.npc_tinted_icon_path(-3) != sprites.npc_icon_path(1)
    assert sprites.npc_tinted_icon_path(-3) != sprites.npc_tinted_icon_path(-7)


def _fake_cache(tmp_path, monkeypatch, done: dict):
    import os
    monkeypatch.setattr(sprites, "_CACHE_ROOT", str(tmp_path))
    d = tmp_path / sprites.KNOWN_VERSION
    d.mkdir(parents=True, exist_ok=True)
    (d / ".done").write_text(json.dumps(done))
    assert os.path.isdir(d)


def test_a_cache_from_an_older_scope_is_not_trusted(tmp_path, monkeypatch):
    _fake_cache(tmp_path, monkeypatch, {"scope": "all-v2", "npcs": 697})
    assert sprites.is_cached() is False


def test_a_cache_with_no_npcs_is_incomplete_and_re_extracts(tmp_path, monkeypatch):
    """An extraction that beat the frame counts skipped the NPC sheets; the next run,
    once the catalog fetch has published them, finishes the job."""
    _fake_cache(tmp_path, monkeypatch, {"scope": sprites._SCOPE, "npcs": 0})
    assert sprites.is_cached() is False
    _fake_cache(tmp_path, monkeypatch, {"scope": sprites._SCOPE, "npcs": 697})
    assert sprites.is_cached() is True


def test_extraction_skips_npcs_when_no_frame_counts_are_known(tmp_path, monkeypatch):
    """Better a missing icon than 838 whole strips cached until the scope is bumped."""
    monkeypatch.setattr(sprites, "_NPC_FRAMES_FILE", str(tmp_path / "absent.json"))
    monkeypatch.setattr(sprites, "_CACHE_ROOT", str(tmp_path / "cache"))
    monkeypatch.setattr(sprites, "content_images_dir", lambda mem=None: str(tmp_path))
    monkeypatch.setattr(sprites, "all_item_ids", lambda: set())
    monkeypatch.setattr(sprites.recipes, "load", lambda: {})
    ok, failed, total = sprites.extract()
    assert (ok, failed, total) == (0, 0, 0), "an NPC sheet was extracted uncropped"
    assert sprites.is_cached() is False


def _blocks(w, h, cols, rows, gap=6):
    """A sheet laid out as a cols x rows grid of solid cells separated by clear gaps."""
    img = Image.new("RGBA", (w * cols + gap * (cols - 1), h * rows + gap * (rows - 1)),
                    (0, 0, 0, 0))
    cell = Image.new("RGBA", (w, h), (255, 0, 0, 255))
    for c in range(cols):
        for r in range(rows):
            img.paste(cell, (c * (w + gap), r * (h + gap)))
    return img


def test_a_grid_sheet_is_cropped_to_its_top_left_cell():
    """Queen Slime's 16 frames are 2 columns of 8; Deerclops uses 5; Moon Lord a 3x3.
    npcFrameCount counts frames, not rows, so the vertical crop alone leaves a row of
    little pictures."""
    assert sprites._first_grid_cell(_blocks(40, 30, cols=3, rows=3)).size == (40, 30)
    assert sprites._first_grid_cell(_blocks(50, 20, cols=2, rows=1)).size == (50, 20)


def test_a_single_sprite_with_a_detached_piece_is_not_sliced():
    """The dangerous failure: a sprite whose parts are separated by clear pixels. Uneven
    block sizes are what tell it apart from a grid."""
    img = Image.new("RGBA", (60, 40), (0, 0, 0, 0))
    img.paste(Image.new("RGBA", (40, 40), (255, 0, 0, 255)), (0, 0))   # body
    img.paste(Image.new("RGBA", (6, 40), (255, 0, 0, 255)), (52, 0))   # small detached bit
    assert sprites._first_grid_cell(img).size == (60, 40)


def test_a_plain_sprite_is_untouched():
    img = Image.new("RGBA", (32, 32), (0, 0, 255, 255))
    assert sprites._first_grid_cell(img).size == (32, 32)
