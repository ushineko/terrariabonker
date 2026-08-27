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


# --- the worker reply contract (mid-project review §2.2) ----------------------

def test_replies_returns_every_json_object_in_order():
    from terrariabonker.gui import client

    raw = 'starting up\n{"a": 1}\n[extract] noise\n{"b": 2}\n'
    assert client.replies(raw) == [{"a": 1}, {"b": 2}]


def test_replies_skips_what_does_not_decode():
    """Half-written and non-JSON lines are normal: the worker prints human output too."""
    from terrariabonker.gui import client

    # A JSON array or bare string on its own line decodes fine but is not a reply; both
    # are skipped by the leading-brace filter before parsing, so a caller can always
    # `.get()` what comes back.
    assert client.replies('{"a": 1}\n{not json\n[1, 2]\n"a string"\n') == [{"a": 1}]


def test_replies_on_nothing_is_empty_not_an_error():
    from terrariabonker.gui import client

    assert client.replies("") == [] and client.replies("no json here") == []


def test_error_in_reads_the_prefix_the_worker_actually_sends():
    """`[ERROR] <msg>` is the contract (cli._serve_reply). A panel handler was written
    against a {"error": ...} reply that does not exist."""
    from terrariabonker.gui import client

    assert client.error_in("[ERROR] the auto-use cheat is not enabled") == \
        "the auto-use cheat is not enabled"
    assert client.error_in('out\n[ERROR] refused\n') == "refused"
    assert client.error_in("[ERROR]") == "the worker reported an error"
    assert client.error_in('{"ok": true}') is None
