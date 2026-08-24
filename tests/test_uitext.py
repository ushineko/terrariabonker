"""Tooltip text shaping.

Qt lays a plain-text tooltip out on a single line, so the longer cheat notes ran off the
edge of the screen instead of wrapping.
"""

from terrariabonker.gui import uitext


def test_a_long_note_is_wrapped():
    note = ("limits how far the SMART CURSOR searches for a target (holding Shift to "
            "auto-place). It scans this radius SQUARED every frame, and the reach cheats "
            "size it — at reach 75 that is 22,801 tiles per frame, which stutters. Does "
            "not affect manual placement, tool or interaction reach")
    out = uitext.wrap(note)
    assert "\n" in out
    assert max(len(line) for line in out.split("\n")) <= uitext.WIDTH


def test_every_cheat_note_wraps_within_the_width():
    from terrariabonker.patcher import PATCH_CATALOG
    for name, info in PATCH_CATALOG.items():
        for line in uitext.wrap(info.note).split("\n"):
            assert len(line) <= uitext.WIDTH, (name, line)


def test_blank_lines_between_paragraphs_survive():
    out = uitext.wrap("first paragraph\n\nsecond paragraph")
    assert out == "first paragraph\n\nsecond paragraph"


def test_source_line_breaks_are_reflowed_not_inherited():
    """Notes are written as flowing prose across source lines; they should not keep the
    source's own wrapping."""
    out = uitext.wrap("one two\nthree four", width=40)
    assert out == "one two three four"


def test_empty_text_is_safe():
    assert uitext.wrap("") == ""


def test_cheat_notes_stay_tooltip_sized():
    """A tooltip is a hint, not documentation. These once ran to 277 characters and
    rendered as a single line off the edge of the screen; the detail belongs in the README
    and in the code comment above each cheat."""
    from terrariabonker.patcher import PATCH_CATALOG
    for name, info in PATCH_CATALOG.items():
        assert len(info.note) <= 130, (name, len(info.note), info.note)


def test_cheat_notes_do_not_repeat_what_the_group_tooltip_says():
    """"A game restart clears them" is true of every code patch and is stated once, on the
    group, rather than in each of the twelve notes."""
    from terrariabonker.patcher import PATCH_CATALOG
    repeated = [n for n, i in PATCH_CATALOG.items() if "restart clears" in i.note.lower()]
    assert repeated == [], repeated
