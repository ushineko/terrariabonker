"""Build-keyed anchor ledger and multi-site patching (spec 030).

Mono can JIT one method into more than one arena — that is what silently broke
mining / reach / max_minions: their anchors matched twice and resolution demanded a
unique hit. A byte patch now applies to every copy, and each anchor records the builds
its AOB was actually verified on so the UI can say which cheats are unproven here.
"""

import pytest

from terrariabonker import patcher as P
from terrariabonker import version as ver
from terrariabonker.gui import client
from terrariabonker.patcher import ANCHORS, CHEATS, Patcher

BASE = 0x40000000
CODE = BASE + 0x2000
LIFE = BASE + 0x5000
NAME_AT = BASE + 0x40
COPY_A = 0x100          # where the two identical JIT copies are planted
COPY_B = 0x600


@pytest.fixture
def twin_game(tmp_path, monkeypatch):
    """A game whose ResetEffects anchor is present TWICE, as the live one was."""
    from conftest import FakeMem

    from terrariabonker import profile
    monkeypatch.setattr(P, "_STATE", str(tmp_path / "patches.json"))
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    m = FakeMem(BASE, 0x8000)
    m.plant_mono_string(NAME_AT, "hero")
    m.plant_player(LIFE, [100, 100, 80, 20, 20, 20], NAME_AT)
    for off in (COPY_A, COPY_B):
        m.write(CODE + off, ANCHORS["reset_block"].pattern.raw)
    p = Patcher(m)
    p._exec_regions = lambda writable=False: [(CODE, CODE + 0x1000)]
    return m, p


# --- build keys and the ledger ---------------------------------------------

def test_build_key_pins_version_and_buildid():
    assert ver.build_key("1.4.5.7", "24893155") == "1.4.5.7+24893155"
    assert ver.KNOWN_BUILD_KEY == f"{ver.KNOWN_VERSION}+{ver.KNOWN_BUILDID}"


def test_build_key_survives_missing_pieces():
    assert ver.build_key(None, None) == "?+?"


# Anchors derived after the original build, so they were never seen on it.
LATER_ANCHORS = {"equip_apply", "equip_benefits", "inventory_scan", "smart_cursor"}
# Derived but never yet watched working in-game, so they claim no build at all. The panel
# reports these as unproven, which is the truth — an empty set is a statement, not a gap.
UNPROVEN_ANCHORS: set[str] = set()
# Derived on 1.4.5.8 and confirmed only there — they never existed on the older builds,
# so they claim neither the derivation build nor the 2026-08-23 rebuild.
NEWEST_ANCHORS = {"pylon_place", "pick_tile", "grabitems_call"}
# Derived against the original build and re-confirmed on every rebuild since, so they
# legitimately inherit the default verification set. Listed so that adding an anchor
# fails the classification test rather than silently joining them.
ORIGINAL_ANCHORS = {
    "getranges", "grabitems", "get_spawn_rate", "trydrop", "trigger_ping",
    "player_teleport", "place", "reset_block", "reset_minions",
}

# The build the original AOBs were derived against. Distinct from KNOWN_BUILD_KEY, which
# names whatever the project currently targets and moves when the game updates.
DERIVATION_BUILD = "1.4.5.7+24825745"
# Every build any anchor is allowed to claim, and why. The ledger records proof: each of
# these was watched working in-game on the build it names.
CONFIRMED_BUILDS = {
    DERIVATION_BUILD,           # where the AOBs came from
    "1.4.5.7+24893155",         # a mixed key: 1.4.5.7 running, 1.4.5.8 already downloaded
    "1.4.5.8+24893155",         # the update, all twelve cheats confirmed after it loaded
}


def test_the_original_anchors_are_verified_on_the_derivation_build():
    for key, anchor in ANCHORS.items():
        if key in LATER_ANCHORS or key in UNPROVEN_ANCHORS:
            continue
        assert ver.KNOWN_BUILD_KEY in anchor.verified, key


def test_the_original_anchors_record_the_rebuild_they_were_reconfirmed_on():
    """2026-08-23 rebuild: those nine cheats were confirmed in-game on 24893155."""
    for key, anchor in ANCHORS.items():
        if key in LATER_ANCHORS or key in UNPROVEN_ANCHORS or key in NEWEST_ANCHORS:
            continue
        assert "1.4.5.7+24893155" in anchor.verified, key


def test_a_later_anchor_never_inherits_the_default_verification():
    """Honesty is the point of the ledger: anchors derived after the original build were
    never seen on it, so they must not pick up the default set.

    Asserted against the derivation build rather than KNOWN_BUILD_KEY, which moves when
    the game updates — it now names 1.4.5.8, a build these anchors *were* confirmed on.
    """
    for key in LATER_ANCHORS:
        assert DERIVATION_BUILD not in ANCHORS[key].verified, key


def test_confirmed_later_anchors_claim_only_the_builds_they_were_proven_on():
    for key in LATER_ANCHORS:
        assert ANCHORS[key].verified == frozenset(
            {"1.4.5.7+24893155", "1.4.5.8+24893155"}), key


def test_an_anchor_claims_a_build_only_once_it_is_confirmed_in_game():
    """The ledger records proof, not derivation: every entry here was watched working
    in-game on the build it names. Adding a build to this set is a claim about reality,
    so the allowlist has to be edited deliberately alongside it."""
    for key, anchor in ANCHORS.items():
        for build in anchor.verified:
            assert build in CONFIRMED_BUILDS, (key, build)


def test_the_current_target_is_a_confirmed_build():
    """KNOWN_BUILD_KEY is what the panel compares against; it must not name a build
    nobody has confirmed."""
    assert ver.KNOWN_BUILD_KEY in CONFIRMED_BUILDS


def test_per_anchor_divergence_is_supported():
    """A future build may break only some anchors, so verification is per anchor."""
    only_here = P.Anchor(ANCHORS["reset_block"].pattern, verified=frozenset({"1.4.5.7+1"}))
    assert "1.4.5.7+1" in only_here.verified
    assert ver.KNOWN_BUILD_KEY not in only_here.verified


# --- resolution -------------------------------------------------------------

def test_two_identical_copies_resolve_to_both_sites(twin_game):
    _, p = twin_game
    res = p.resolution("reset_block")
    assert res.available and len(res.sites) == 2
    assert res.reason == ""


def test_resolution_reports_verification_against_the_running_build(twin_game):
    _, p = twin_game
    assert p.resolution("reset_block", ver.KNOWN_BUILD_KEY).verified is True
    assert p.resolution("reset_block", "1.4.5.7+99999999").verified is False


def test_a_unique_anchor_still_refuses_multiple_sites(twin_game, monkeypatch):
    """Kept for sites where patching a twin would be harmful."""
    _, p = twin_game
    monkeypatch.setitem(ANCHORS, "reset_block",
                        P.Anchor(ANCHORS["reset_block"].pattern,
                                 verified=ANCHORS["reset_block"].verified, unique=True))
    res = p.resolution("reset_block")
    assert not res.available and "must be unique" in res.reason


def test_missing_anchor_reason_states_what_was_observed(tmp_path, monkeypatch):
    from conftest import FakeMem

    from terrariabonker import profile
    monkeypatch.setattr(P, "_STATE", str(tmp_path / "patches.json"))
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    m = FakeMem(BASE, 0x8000)
    p = Patcher(m)
    p._exec_regions = lambda writable=False: [(CODE, CODE + 0x1000)]
    res = p.resolution("reset_block")
    assert not res.available
    assert "matched nothing" in res.reason
    # it must not assert a cause it has not checked (the old text blamed a game update)
    assert "re-derive" not in res.reason


# --- multi-site patching ----------------------------------------------------

def test_enable_patches_every_copy(twin_game):
    m, p = twin_game
    p.enable("reach")
    cheat = CHEATS["reach"]
    for off in (COPY_A, COPY_B):
        site = CODE + off + cheat.patch_off
        assert m.read(site, len(cheat.patched)) == cheat.patched, hex(off)


def test_disable_reverts_every_copy(twin_game):
    m, p = twin_game
    p.enable("reach")
    p.disable("reach")
    cheat = CHEATS["reach"]
    for off in (COPY_A, COPY_B):
        site = CODE + off + cheat.patch_off
        assert m.read(site, len(cheat.orig)) == cheat.orig, hex(off)
    assert p.is_enabled("reach") is False


def test_is_enabled_is_true_when_any_copy_is_patched(twin_game):
    """We cannot tell which copy executes, so a single patched copy counts as on."""
    m, p = twin_game
    cheat = CHEATS["reach"]
    m.write(CODE + COPY_B + cheat.patch_off, cheat.patched)
    assert p.is_enabled("reach") is True


def test_state_records_both_sites(twin_game):
    _, p = twin_game
    p.enable("reach")
    assert len(p._sites["reset_block"]) == 2


def test_older_single_address_state_still_loads(tmp_path, monkeypatch):
    """State written before this change stored one address per anchor."""
    import json

    from conftest import FakeMem

    from terrariabonker import profile
    state = tmp_path / "patches.json"
    monkeypatch.setattr(P, "_STATE", str(state))
    monkeypatch.setattr(profile, "_PATH", str(tmp_path / "profile.json"))
    m = FakeMem(BASE, 0x8000)
    state.write_text(json.dumps({"pid": m.pid, "sites": {"reset_block": CODE + COPY_A},
                                 "enabled": [], "inj": {}, "values": {}}))
    p = Patcher(m)
    assert p._sites["reset_block"] == [CODE + COPY_A]


# --- what the UI is told ----------------------------------------------------

def test_details_carry_availability_and_verification(twin_game):
    _, p = twin_game
    d = p.details(ver.KNOWN_BUILD_KEY)["reach"]
    assert d["available"] is True and d["verified"] is True and d["sites"] == 2
    assert d["on"] is False and d["reason"] == ""


def test_details_mark_an_unverified_build(twin_game):
    _, p = twin_game
    assert p.details("1.4.5.7+99999999")["reach"]["verified"] is False


def test_banner_is_silent_when_everything_resolves_and_is_verified():
    st = {"build": "1.4.5.7+24825745",
          "detail": {"reach": {"available": True, "verified": True}}}
    assert client.build_banner(st, "1.4.5.7+24825745") == ""


def test_banner_names_unavailable_cheats_and_their_reasons():
    st = {"build": "1.4.5.7+24893155",
          "detail": {"reach": {"available": False, "verified": False,
                               "reason": "anchor 'reset_block' matched nothing"},
                     "pickup": {"available": True, "verified": True}}}
    text = client.build_banner(st, "1.4.5.7+24825745")
    assert "reach" in text and "matched nothing" in text and "1 of 2" in text


def test_banner_flags_an_unverified_build_even_when_all_cheats_resolve():
    st = {"build": "1.4.5.7+24893155",
          "detail": {"reach": {"available": True, "verified": False}}}
    text = client.build_banner(st, "1.4.5.7+24825745")
    assert "24893155" in text and "unproven" in text


def test_banner_handles_a_missing_status():
    assert client.build_banner(None, "1.4.5.7+24825745") == ""
    assert client.build_banner({}, "1.4.5.7+24825745") == ""


# --- what auto-restore tells the user --------------------------------------

def test_restore_summary_is_silent_when_everything_landed():
    assert client.restore_summary({"cheats": ["mining"], "items": [1],
                                   "pending": [], "skipped": [], "absent": []}) == []


def test_restore_summary_separates_cheats_from_item_edits():
    """They fail for unrelated reasons; reporting them as one lump is what made the old
    message point at a build notice that was not on screen."""
    lines = client.restore_summary({"pending": ["reach"], "absent": [708, 54]})
    assert len(lines) == 2
    cheat_line = next(ln for ln in lines if "cheat" in ln)
    item_line = next(ln for ln in lines if "item edit" in ln)
    assert "reach" in cheat_line and "notice above" in cheat_line
    assert "notice above" not in item_line, "item edits have nothing to do with the build"


def test_an_absent_item_is_worded_as_waiting_not_as_a_failure():
    """The old line — "no longer hold the item they were saved for; left alone rather
    than overwritten" — read as a warning about seven items, when not carrying something
    is entirely ordinary (spec 038)."""
    lines = client.restore_summary({"absent": [54]})
    assert len(lines) == 1
    assert "Hermes Boots" in lines[0], "the item should be named, not the slot"
    for alarming in ("no longer hold", "overwritten", "not re-applied", "failed"):
        assert alarming not in lines[0], alarming


def test_restore_summary_says_nothing_about_slots():
    """Slots are no longer the identity, so they have no place in the message."""
    lines = client.restore_summary({"absent": [3507]})
    assert "slot" not in lines[0].lower()


def test_restore_summary_reports_refused_cheats_separately():
    lines = client.restore_summary({"skipped": ["cheat:teleport"]})
    assert len(lines) == 1 and "teleport" in lines[0] and "refused" in lines[0]


# --- how the panel groups the cheats ---------------------------------------

def test_every_cheat_lands_in_a_section():
    from terrariabonker.patcher import PATCH_CATALOG, SECTIONS
    known = {s for s, _ in SECTIONS}
    for name, info in PATCH_CATALOG.items():
        assert info.section in known, name


def test_a_cheat_missing_from_the_section_map_is_not_hidden():
    """Adding a cheat and forgetting the map must not drop it out of the panel."""
    from terrariabonker.patcher import SECTIONS, _section_of
    assert _section_of("not_a_real_cheat") == SECTIONS[-1][0]


def test_the_catalog_is_ordered_by_section():
    """The panel emits a heading when the section changes, so the catalog has to be
    grouped or headings would repeat."""
    from terrariabonker.patcher import PATCH_CATALOG
    seen, order = set(), []
    for info in PATCH_CATALOG.values():
        if not order or order[-1] != info.section:
            assert info.section not in seen, f"{info.section} appears in two runs"
            seen.add(info.section)
            order.append(info.section)


def test_an_applied_injection_reports_the_sites_it_installed(monkeypatch):
    """It used to report the anchor-scan cache, which is empty when the cheat was applied
    by an earlier process — so `loot`, patched at four sites, read as "0 sites"."""
    from terrariabonker import patcher

    name = next(iter(patcher.INJECTIONS))
    p = patcher.Patcher.__new__(patcher.Patcher)
    p._sites = {}                                  # this process never scanned
    p._inj = {name: {"sites": [{"inject": 1, "cave": 2}] * 4, "stub_len": 8}}
    monkeypatch.setattr(patcher.Patcher, "is_enabled", lambda self, n: n == name)
    # every other cheat is off, and resolving those would want a live process
    monkeypatch.setattr(patcher.Patcher, "resolution",
                        lambda self, key, build=None: patcher.Resolution(
                            (), False, "not scanned", False))

    detail = patcher.Patcher.details(p, build=None)[name]
    assert detail["on"] is True
    assert detail["sites"] == 4, "reported the empty scan cache, not what is installed"


def test_an_unproven_anchor_claims_nothing():
    """An anchor that resolves but has never been watched working must claim no build.

    Empty is the honest state and the panel renders it as unproven. Filling it in to
    quiet the banner would be the exact dishonesty this ledger exists to prevent. The set
    is empty at present — `pylon_place` sat in it until it was confirmed in-game, and
    `pick_tile` until the extractor was watched taking whole veins — and it is kept so the
    next derived-but-unproven anchor has somewhere honest to live.
    """
    for key in UNPROVEN_ANCHORS:
        assert ANCHORS[key].verified == frozenset(), key


def test_the_newest_anchors_claim_only_the_build_they_were_seen_on():
    """They never existed on 1.4.5.7, so they must claim neither of those keys."""
    for key in NEWEST_ANCHORS:
        assert ANCHORS[key].verified == frozenset({"1.4.5.8+24893155"}), key


def test_every_anchor_is_classified_so_a_new_one_cannot_slip_through():
    """The ledger is only worth anything if it does not silently over-claim.

    An anchor absent from `_VERIFIED_INSTEAD` inherits `_VERIFIED_BUILDS` — every build we
    have ever confirmed anything on. That is right for the originals and wrong for every
    later one: `grabitems_call` was added for the ore extractor, derived and tested only
    on 1.4.5.8, and silently claimed two 1.4.5.7 builds it had never run on.

    Classifying every anchor makes a new one fail here, which forces the question the
    ledger exists to ask: where has this actually been seen working?
    """
    from terrariabonker import patcher as P

    known = LATER_ANCHORS | UNPROVEN_ANCHORS | NEWEST_ANCHORS | ORIGINAL_ANCHORS
    unclassified = set(P._RAW_ANCHORS) - known
    assert not unclassified, (
        f"{sorted(unclassified)} are not classified. If derived on the original build, "
        "add them to ORIGINAL_ANCHORS; otherwise give them an entry in _VERIFIED_INSTEAD "
        "naming the builds they were actually confirmed on, and list them here.")
    stale = known - set(P._RAW_ANCHORS)
    assert not stale, f"{sorted(stale)} no longer exist — update these sets"


def test_a_cheat_re_derived_for_a_new_build_keeps_the_old_one_working():
    """Support accumulates: re-deriving a cheat for a new release must not drop the
    previous one.

    When the game moves the code a cheat patches, the new bytes go in as a *variant*
    alongside the old pattern rather than replacing it. Replacing would silently break the
    older build while `verified` still claimed it — the ledger would be lying, which is the
    one thing it exists not to do.
    """
    from terrariabonker.patcher import Anchor, _pat

    old_bytes = _pat("11 22 33 44")
    new_bytes = _pat("55 66 77 88")
    a = Anchor(old_bytes, verified=frozenset({"1.4.5.8+1"}),
               variants=(("1.4.6+2", new_bytes),))

    # on the build the variant was derived for, its own pattern leads
    assert a.candidates("1.4.6+2")[0] is new_bytes
    # but the old pattern is still tried, so the older build keeps working
    assert old_bytes in a.candidates("1.4.6+2")
    # on the original build, the original leads and the variant is still available
    assert a.candidates("1.4.5.8+1")[0] is old_bytes
    assert new_bytes in a.candidates("1.4.5.8+1")
    # on a build nobody has seen, every pattern is tried — a ledger, not a gate
    assert set(a.candidates("2.0.0+9")) == {old_bytes, new_bytes}
    assert set(a.candidates(None)) == {old_bytes, new_bytes}


def test_the_scan_falls_through_variants_until_one_matches(monkeypatch):
    """A build with no variant of its own still resolves if any known pattern matches —
    that is what keeps an unverified release usable instead of dead."""
    from terrariabonker import patcher as P

    old_bytes, new_bytes = P._pat("11 22 33 44"), P._pat("55 66 77 88")
    monkeypatch.setitem(
        P.ANCHORS, "fake",
        P.Anchor(old_bytes, verified=frozenset(), variants=(("1.4.6+2", new_bytes),)))

    CODE = 0x40000000
    blob = b"\x00\x00" + bytes.fromhex("55667788") + b"\x00\x00"

    class mem:
        pid = 1

        @staticmethod
        def read(a, n):
            return blob if a == CODE else b""

    p = P.Patcher.__new__(P.Patcher)
    p.mem = mem()
    p._sites = {}
    p._exec_regions = lambda writable=False: [(CODE, CODE + len(blob))]

    # only the variant's bytes are present, and the build has no variant registered
    assert p._scan("fake", "9.9.9+0") == [CODE + 2], "fell through to no pattern at all"
    assert p._scan("fake", "1.4.6+2") == [CODE + 2]
    assert p._scan("fake", None) == [CODE + 2]


def test_the_runtime_is_tracked_beside_the_build_not_inside_it():
    """A build key names the game. It says nothing about the .NET runtime executing it —
    and the patterns match machine code that runtime's JIT emitted, so a Proton update can
    break a cheat with Terraria untouched.

    Tracked beside the key rather than folded into it, deliberately: every existing
    verification predates the tracking, and rewriting the key would either invalidate all
    of them at a stroke or require backfilling a runtime nobody recorded.
    """
    import inspect
    from terrariabonker import builds, version as ver

    # detection reads the module paths, which carry the version
    maps = ("7f00-7f01 r-xp 0 0:0 0 /files/share/wine/mono/wine-mono-11.2.0/bin/"
            "libmono-2.0-x86.dll\n")

    class mem:
        pid = 4242

    import builtins
    real_open = builtins.open

    def fake_open(path, *a, **k):
        if str(path) == "/proc/4242/maps":
            import io
            return io.StringIO(maps)
        return real_open(path, *a, **k)

    builtins.open = fake_open
    try:
        assert ver.detect_runtime(mem()) == "wine-mono-11.2.0"
    finally:
        builtins.open = real_open

    # a key is still just the game
    assert ver.build_key("1.4.5.8", "24893155") == "1.4.5.8+24893155"
    assert "mono" not in ver.KNOWN_BUILD_KEY

    # and remember() carries the runtime as its own field, omitted when unknown
    src = inspect.getsource(builds.remember)
    assert 'entry["runtime"] = runtime' in src, "the runtime is not recorded"
    assert "if runtime:" in src, \
        "an unknown runtime must be omitted, not stored as a guess"
