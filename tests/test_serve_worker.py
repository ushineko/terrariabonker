"""The long-lived JSON worker and the locate caches behind it (spec 029).

The worker exists for one reason: locating the player is ~99% of a read's cost, so
repeating it per request makes a live sync unaffordable. These cover the protocol
contract and the cache-validation rules, headless — no game, no root.
"""

import io
import json

import pytest

from terrariabonker import cli
from terrariabonker.locate import PlayerBlock
from terrariabonker.service import Service, ServiceError


class _Mem:
    """Just enough Mem for the cache tests."""

    def __init__(self, pid=1234):
        self.pid = pid


class _FakeService:
    """Stands in for the warm Service the worker holds."""

    def __init__(self):
        self.mem = _Mem()


def _block(life_addr, name="Bonker"):
    # life_max2, life_max, life, mana, mana_max, mana_max2
    return PlayerBlock(life_addr, 400, 400, 400, 200, 200, 200, name=name)


def _serve(lines, monkeypatch, exists=None):
    """Run the worker over a canned stdin and return the parsed replies.

    ``exists`` stands in for the /proc/<pid> liveness check the worker uses to notice
    that the game went away; the default keeps the warm Service alive.
    """
    out = io.StringIO()
    monkeypatch.setattr(cli.sys, "stdin", io.StringIO("".join(f"{ln}\n" for ln in lines)))
    monkeypatch.setattr(cli.sys, "stdout", out)
    monkeypatch.setattr(cli, "elevate", lambda: None)
    monkeypatch.setattr(cli.os.path, "exists", exists or (lambda _p: True))
    monkeypatch.setattr(cli, "_WARM", None)      # never leak a warm Service between tests
    rc = cli.cmd_serve(object())
    replies = [json.loads(ln) for ln in out.getvalue().splitlines() if ln.strip()]
    return rc, replies


# --- protocol ---------------------------------------------------------------

def test_worker_answers_each_request_with_its_id(monkeypatch):
    monkeypatch.setattr(cli.Service, "connect", classmethod(lambda cls: _FakeService()))
    monkeypatch.setattr(cli, "_serve_once", lambda parser, argv: (True, f"ran {argv[0]}"))
    rc, replies = _serve([json.dumps({"id": 7, "argv": ["status", "--json"]}),
                          json.dumps({"id": 9, "argv": ["inventory", "--all", "--json"]})],
                         monkeypatch)
    assert rc == 0
    assert [r["id"] for r in replies] == [7, 9]
    assert replies[0]["ok"] and replies[0]["out"] == "ran status"


def test_worker_exits_on_stdin_eof(monkeypatch):
    """EOF must end it, or a root process outlives the GUI that started it."""
    rc, replies = _serve([], monkeypatch)
    assert rc == 0 and replies == []


def test_unallowlisted_commands_are_refused_without_running(monkeypatch):
    ran = []
    monkeypatch.setattr(cli.Service, "connect", classmethod(lambda cls: _FakeService()))
    monkeypatch.setattr(cli, "_serve_once", lambda parser, argv: ran.append(argv) or (True, ""))
    _, replies = _serve([json.dumps({"id": 1, "argv": ["gui"]}),
                         json.dumps({"id": 2, "argv": ["freeze", "--godmode"]}),
                         json.dumps({"id": 3, "argv": []})], monkeypatch)
    assert ran == [], "a refused command must never reach the dispatcher"
    assert all(r["ok"] is False for r in replies)
    assert "not served here" in replies[0]["out"]


def test_malformed_requests_do_not_kill_the_worker(monkeypatch):
    monkeypatch.setattr(cli.Service, "connect", classmethod(lambda cls: _FakeService()))
    monkeypatch.setattr(cli, "_serve_once", lambda parser, argv: (True, "ok"))
    _, replies = _serve(["not json at all",
                         json.dumps({"id": 2, "argv": "notalist"}),
                         json.dumps({"id": 3, "argv": ["status", "--json"]})], monkeypatch)
    assert replies[0]["ok"] is False and replies[1]["ok"] is False
    assert replies[2]["ok"] is True, "the worker must keep serving after bad input"


def test_service_errors_come_back_as_error_output(monkeypatch):
    """The GUI keys off '[ERROR]' in the output, same as the merged-channel spawn."""
    monkeypatch.setattr(cli.Service, "connect", classmethod(lambda cls: _FakeService()))

    def boom(parser, argv):
        raise AssertionError("should not be reached")

    monkeypatch.setattr(cli, "_serve_once",
                        lambda parser, argv: (False, "[ERROR] no player found"))
    _, replies = _serve([json.dumps({"id": 1, "argv": ["inventory", "--json"]})], monkeypatch)
    assert replies[0]["ok"] is False and "[ERROR]" in replies[0]["out"]


def test_serve_ops_are_all_real_subcommands():
    """The allowlist must not drift from the parser it gates."""
    parser = cli.build_parser()
    known = set()
    for action in parser._subparsers._actions:
        if hasattr(action, "choices") and action.choices:
            known |= set(action.choices)
    assert cli.SERVE_OPS <= known, cli.SERVE_OPS - known


def test_blocking_and_privileged_extras_stay_off_the_allowlist():
    for op in ("gui", "freeze", "godmode", "read", "write", "serve"):
        assert op not in cli.SERVE_OPS


# --- locate caching ---------------------------------------------------------

def test_players_scans_once_while_the_cache_stays_valid(monkeypatch):
    calls = []
    blocks = [_block(0x1000)]
    monkeypatch.setattr("terrariabonker.service.find_players",
                        lambda mem: calls.append(1) or blocks)
    monkeypatch.setattr("terrariabonker.service.read_block", lambda mem, a: _block(a))
    svc = Service(_Mem())
    svc._resolve_live = lambda: _block(0x1000)
    assert svc.players() == blocks
    assert svc.players() == blocks
    assert len(calls) == 1, "a valid cache must not rescan"


def test_cache_rescans_when_an_address_stops_being_a_player(monkeypatch):
    """The managed heap is GC'd: a cached life_addr can stop being a player block."""
    calls = []
    monkeypatch.setattr("terrariabonker.service.find_players",
                        lambda mem: calls.append(1) or [_block(0x1000)])
    monkeypatch.setattr("terrariabonker.service.read_block", lambda mem, a: _block(a))
    svc = Service(_Mem())
    svc._resolve_live = lambda: _block(0x1000)
    svc.players()
    monkeypatch.setattr("terrariabonker.service.read_block", lambda mem, a: None)
    svc.players()
    assert len(calls) == 2, "an invalid cached address must force a rescan"


def test_cache_rescans_when_the_live_player_moved_to_a_new_object(monkeypatch):
    """World reload: writes must not keep landing on the copies we cached."""
    calls = []
    monkeypatch.setattr("terrariabonker.service.find_players",
                        lambda mem: calls.append(1) or [_block(0x1000)])
    monkeypatch.setattr("terrariabonker.service.read_block", lambda mem, a: _block(a))
    svc = Service(_Mem())
    svc._resolve_live = lambda: _block(0x1000)
    svc.players()
    svc._resolve_live = lambda: _block(0xBEEF)      # live player is elsewhere now
    svc.players()
    assert len(calls) == 2


def test_invalidate_forces_a_rescan(monkeypatch):
    calls = []
    monkeypatch.setattr("terrariabonker.service.find_players",
                        lambda mem: calls.append(1) or [_block(0x1000)])
    monkeypatch.setattr("terrariabonker.service.read_block", lambda mem, a: _block(a))
    svc = Service(_Mem())
    svc._resolve_live = lambda: _block(0x1000)
    svc.players()
    svc.invalidate()
    svc.players()
    assert len(calls) == 2


def test_no_player_raises_and_leaves_no_cache(monkeypatch):
    monkeypatch.setattr("terrariabonker.service.find_players", lambda mem: [])
    svc = Service(_Mem())
    with pytest.raises(ServiceError):
        svc.players()
    assert svc._blocks is None


def test_anchor_is_found_once_then_reused(monkeypatch):
    """The anchor is the expensive half; re-reading through it is what stays cheap."""
    finds = []
    monkeypatch.setattr("terrariabonker.service.find_localplayer_anchor",
                        lambda mem: finds.append(1) or 0xA000)
    monkeypatch.setattr("terrariabonker.service.local_player_at",
                        lambda mem, anchor: _block(0x1000))
    svc = Service(_Mem())
    assert svc._resolve_live().life_addr == 0x1000
    assert svc._resolve_live().life_addr == 0x1000
    assert len(finds) == 1


def test_anchor_is_refound_when_it_stops_resolving(monkeypatch):
    finds = []
    monkeypatch.setattr("terrariabonker.service.find_localplayer_anchor",
                        lambda mem: finds.append(1) or 0xA000)
    monkeypatch.setattr("terrariabonker.service.local_player_at", lambda mem, anchor: None)
    svc = Service(_Mem())
    svc._anchor = 0xDEAD
    assert svc._resolve_live() is None
    assert finds == [1], "a dead anchor must be re-found once, not spun on"


def test_worker_reconnects_when_the_game_pid_goes_away(monkeypatch):
    """Game restart: the warm Service points at a dead pid and must be replaced."""
    connects = []

    def connect(cls):
        connects.append(1)
        return _FakeService()

    monkeypatch.setattr(cli.Service, "connect", classmethod(connect))
    monkeypatch.setattr(cli, "_serve_once", lambda parser, argv: (True, "ok"))
    # the pid we warmed on is gone by the time the second request arrives
    _, replies = _serve([json.dumps({"id": 1, "argv": ["status", "--json"]}),
                         json.dumps({"id": 2, "argv": ["status", "--json"]})],
                        monkeypatch, exists=lambda _p: False)
    assert all(r["ok"] for r in replies)
    assert len(connects) == 2, "a dead pid must force a reconnect, not keep serving it"


def test_connect_failure_is_reported_and_the_worker_keeps_running(monkeypatch):
    def connect(cls):
        raise ServiceError("game not found")

    monkeypatch.setattr(cli.Service, "connect", classmethod(connect))
    _, replies = _serve([json.dumps({"id": 1, "argv": ["status", "--json"]}),
                         json.dumps({"id": 2, "argv": ["status", "--json"]})], monkeypatch)
    assert len(replies) == 2
    assert all(r["ok"] is False and "game not found" in r["out"] for r in replies)


def test_cache_rescans_when_ground_truth_is_unavailable(monkeypatch):
    """No ground truth => the cache cannot be confirmed, so it must not be trusted.

    A GC'd Player object can still read back as the same named player, so a name match
    alone is not evidence the copy is live (codex review, P1).
    """
    calls = []
    monkeypatch.setattr("terrariabonker.service.find_players",
                        lambda mem: calls.append(1) or [_block(0x1000)])
    monkeypatch.setattr("terrariabonker.service.read_block", lambda mem, a: _block(a))
    svc = Service(_Mem())
    svc._resolve_live = lambda: _block(0x1000)
    svc.players()
    svc._resolve_live = lambda: None                # anchor gone / mid world-load
    svc.players()
    assert len(calls) == 2, "an unconfirmable cache must be rescanned, not reused"


def _svc_with_versions(monkeypatch, versions):
    """A Service whose version detection yields the given readings in order."""
    seq = list(versions)
    monkeypatch.setattr("terrariabonker.service.ver.detect_version",
                        lambda mem: seq.pop(0) if seq else "1.4.5.7")
    monkeypatch.setattr("terrariabonker.service.ver.read_buildid", lambda p: None)
    svc = Service(_Mem())
    svc.mem.exe_path = lambda: None
    return svc


def test_a_startup_misread_build_is_not_cached(monkeypatch):
    """Regression: the CLR's own "2.0.50727" can win the memory scan while the game is
    still loading. Caching it pinned the build at "incompatible" for the life of the
    worker, so require_compatible() refused restore, patches and item edits forever."""
    svc = _svc_with_versions(monkeypatch, ["2.0.50727", "1.4.5.7"])
    assert svc.compatibility()[0] == "incompatible"
    assert svc.compatibility()[0] in ("exact", "hotfix"), "must re-detect, not cache the misread"


def test_a_good_build_reading_is_cached(monkeypatch):
    """The perf win still stands once the reading is trustworthy."""
    calls = []
    monkeypatch.setattr("terrariabonker.service.ver.detect_version",
                        lambda mem: calls.append(1) or "1.4.5.7")
    monkeypatch.setattr("terrariabonker.service.ver.read_buildid", lambda p: None)
    svc = Service(_Mem())
    svc.mem.exe_path = lambda: None
    svc.compatibility()
    svc.compatibility()
    assert len(calls) == 1


def test_invalidate_also_drops_the_build_reading(monkeypatch):
    svc = _svc_with_versions(monkeypatch, ["1.4.5.7", "1.4.5.7"])
    svc.compatibility()
    svc.invalidate()
    assert svc._compat is None
