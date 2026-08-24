"""Deciding which build a running Terraria actually is (spec 030 follow-up).

This was wrong at the root. The version was decided by counting version-shaped strings in
the process and taking the most numerous, which assumes the game's own version is the one
that appears most. It is not:

- For ~21 seconds after launch the only candidates are a `1.4.5.8` constant baked in the
  exe and mono's own `v2.0.50727`, one occurrence each — a tie broken by scan order, which
  is how a startup misread could confidently report either.
- On the 1.4.5.8 build the heap holds four copies of a stale `"Version":"v1.4.5.7"` JSON
  against a single copy of the real literal, so the vote returns the *previous* version.

The authority is the version constant in the exe the process maps — after checking that
mapping still refers to the file on disk, because Steam replaces the file while the game
keeps running the code it already loaded.
"""

import pytest

from terrariabonker import version as ver


class FakeMem:
    """Just enough of Mem for detect_version."""

    pid = 1

    def __init__(self, blob: bytes = b"", exe: str | None = None):
        self._blob = blob
        self._exe = exe

    def exe_path(self):
        return self._exe

    def regions(self):
        return [(0, len(self._blob))]

    def read(self, addr, size):
        return self._blob[addr:addr + size]


def _u16(*strings):
    return b"".join(s.encode("utf-16le") for s in strings)


def _exe(tmp_path, *versions):
    p = tmp_path / "Terraria.exe"
    p.write_bytes(b"junk" + _u16(*(f"v{v}" for v in versions)) + b"junk")
    return str(p)


@pytest.fixture
def no_exe(monkeypatch):
    """Force the memory-scan fallback, so the old rules can be tested on their own."""
    monkeypatch.setattr(ver, "_mapped_exe", lambda m: (None, False))


# --- the exe is the authority ------------------------------------------------

def test_the_exe_literal_is_the_authority(tmp_path):
    assert ver._version_from_exe(_exe(tmp_path, "1.4.5.8", "1.4.5.8")) == "1.4.5.8"


def test_the_runtime_path_in_the_exe_is_ignored(tmp_path):
    assert ver._version_from_exe(_exe(tmp_path, "1.4.5.8", "2.0.50727")) == "1.4.5.8"


def test_an_exe_with_no_single_answer_gives_none(tmp_path):
    """Two different literals means this rule cannot decide; say so rather than pick."""
    assert ver._version_from_exe(_exe(tmp_path, "1.4.5.7", "1.4.5.8")) is None


def test_the_exe_beats_a_heap_full_of_stale_strings(tmp_path, monkeypatch):
    """The case that exposed all of this: four stale 1.4.5.7 JSON blobs in a 1.4.5.8
    process. A frequency vote over memory returns the previous version, confidently."""
    path = _exe(tmp_path, "1.4.5.8", "1.4.5.8")
    monkeypatch.setattr(ver, "_mapped_exe", lambda m: (path, True))
    mem = FakeMem(_u16(*(["v1.4.5.7"] * 4 + ["v2.0.50727"])), exe=path)
    assert ver.detect_version(mem) == "1.4.5.8"


def test_a_replaced_exe_falls_back_to_memory(tmp_path, monkeypatch):
    """Steam swaps the file while the game runs the code it already mapped. In that window
    the file describes a build the process is not executing, and the heap — full of the
    running build's own strings — is the better source. This really happened: the file
    became 1.4.5.8 at 12:19 while a 1.4.5.7 process ran on until 21:41."""
    path = _exe(tmp_path, "1.4.5.8", "1.4.5.8")
    monkeypatch.setattr(ver, "_mapped_exe", lambda m: (path, False))
    assert ver.detect_version(FakeMem(_u16(*(["v1.4.5.7"] * 4)), exe=path)) == "1.4.5.7"


# --- the memory-scan fallback ------------------------------------------------

def test_the_runtime_version_never_wins(no_exe):
    """Even outnumbering the game's string, "v2.0.50727" is not a game version."""
    assert ver.detect_version(
        FakeMem(_u16(*(["v2.0.50727"] * 10 + ["v1.4.5.7"] * 2)))) == "1.4.5.7"


def test_a_single_occurrence_is_not_evidence(no_exe):
    """~21 seconds of every launch look like this: two impostors, one occurrence each,
    and the tie between them decided by whichever the scan reached first."""
    assert ver.detect_version(FakeMem(_u16("v1.4.5.8", "v2.0.50727"))) is None


def test_only_the_runtime_version_present_reads_as_unknown(no_exe):
    assert ver.detect_version(FakeMem(_u16("v2.0.50727", "v4.0.30319"))) is None


def test_short_strings_are_ignored(no_exe):
    assert ver.detect_version(FakeMem(_u16("v1.0", "v2.3"))) is None


def test_a_real_hotfix_still_reads(no_exe):
    assert ver.detect_version(FakeMem(_u16("v1.4.5.8", "v1.4.5.8"))) == "1.4.5.8"


# --- classification ----------------------------------------------------------

def test_unknown_is_not_treated_as_incompatible():
    """'unknown' is recoverable and retried; 'incompatible' aborts the write."""
    assert ver.compatibility(None, "24893155")[0] == "unknown"
    assert ver.compatibility("2.0.50727", "24893155")[0] == "incompatible", \
        "the misread used to land here and abort auto-restore"
