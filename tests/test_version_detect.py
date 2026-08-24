"""Reading the game's version out of live memory (spec 030 follow-up).

The version is scanned, not read from a fixed place, so it competes with every other
version-shaped string in the process — including the mono runtime's own "v2.0.50727".
Right after launch that can be the only match, because Terraria's has not been allocated
yet, and a confident wrong answer aborts everything: "Terraria 2.0.50727 differs from
1.4.5.7 ... the offsets are almost certainly wrong".
"""

from terrariabonker import version as ver


class FakeMem:
    """Just enough of Mem for detect_version: regions() and read()."""

    def __init__(self, blob: bytes):
        self._blob = blob

    def regions(self):
        return [(0, len(self._blob))]

    def read(self, addr, size):
        return self._blob[addr:addr + size]


def _u16(*strings):
    return b"".join(s.encode("utf-16le") for s in strings)


def test_the_game_version_is_found():
    mem = FakeMem(_u16("v1.4.5.7", " ", "v1.4.5.7"))
    assert ver.detect_version(mem) == "1.4.5.7"


def test_the_runtime_version_never_wins():
    """Even outnumbering the game's string, "v2.0.50727" is not a game version."""
    mem = FakeMem(_u16(*(["v2.0.50727"] * 10 + ["v1.4.5.7"] * 2)))
    assert ver.detect_version(mem) == "1.4.5.7"


def test_only_the_runtime_version_present_reads_as_unknown():
    """The startup race: better "I cannot tell yet" than a confident wrong answer."""
    mem = FakeMem(_u16("v2.0.50727", "v4.0.30319"))
    assert ver.detect_version(mem) is None


def test_unknown_is_not_treated_as_incompatible():
    """'unknown' is recoverable and retried; 'incompatible' aborts the write."""
    level, _msg = ver.compatibility(None, "24893155")
    assert level == "unknown"
    level, _msg = ver.compatibility("2.0.50727", "24893155")
    assert level == "incompatible", "the misread used to land here and abort auto-restore"


def test_a_hotfix_is_still_recognised():
    mem = FakeMem(_u16("v1.4.5.8", "v1.4.5.8", "v2.0.50727"))
    assert ver.detect_version(mem) == "1.4.5.8"
    assert ver.compatibility("1.4.5.8", ver.KNOWN_BUILDID)[0] == "hotfix"


def test_short_strings_are_ignored():
    assert ver.detect_version(FakeMem(_u16("v1.0", "v2.3"))) is None


# The distributions below are not invented: they were sampled from a real launch, once a
# second, from the instant the process appeared.

def test_a_single_occurrence_is_not_evidence():
    """~21 seconds of every launch look like this: two impostors, one occurrence each,
    and the tie between them decided by whichever the scan reached first."""
    mem = FakeMem(_u16("v1.4.5.8", "v2.0.50727"))
    assert ver.detect_version(mem) is None


def test_the_live_version_wins_once_it_is_allocated():
    """At +21s the real string appears twice, and from then on it is unambiguous."""
    mem = FakeMem(_u16("v1.4.5.8", "v2.0.50727", "v1.4.5.7", "v1.4.5.7"))
    assert ver.detect_version(mem) == "1.4.5.7"


def test_a_real_hotfix_still_reads_once_it_is_live():
    """The threshold must not make a genuine 1.4.5.8 build unreadable."""
    mem = FakeMem(_u16("v1.4.5.8", "v1.4.5.8", "v2.0.50727"))
    assert ver.detect_version(mem) == "1.4.5.8"
