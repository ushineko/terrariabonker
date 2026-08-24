"""Caches written by the privileged side belong to the user (spec 035, phase 1).

``proc.elevate()`` re-execs with ``sudo -E``, so HOME still points at the user's home and
the compendium's template cache lands in *their* ``~/.cache`` — but owned by root, where
they cannot clear it without sudo, in a directory that stays user-writable.
"""

import os

import pytest

from terrariabonker import proc


@pytest.fixture
def chowns(monkeypatch):
    """Record every chown attempt instead of performing one."""
    calls = []
    monkeypatch.setattr(os, "chown", lambda p, u, g: calls.append((p, u, g)))
    return calls


def test_unprivileged_runs_never_chown(monkeypatch, chowns):
    monkeypatch.setattr(os, "geteuid", lambda: 1000)
    monkeypatch.setenv("SUDO_UID", "1000")
    monkeypatch.setenv("SUDO_GID", "1000")
    proc.give_back_to_user("/tmp/whatever")
    assert chowns == []


@pytest.fixture
def home_of(monkeypatch):
    """Point the SUDO_UID lookup at a chosen home directory."""
    import pwd

    def use(path):
        monkeypatch.setattr(pwd, "getpwuid",
                            lambda _uid: type("P", (), {"pw_dir": path})())
    return use


def test_root_under_sudo_hands_the_file_back(monkeypatch, chowns, home_of, tmp_path):
    home = tmp_path / "u"
    (home / ".cache" / "terrariabonker").mkdir(parents=True)
    target = home / ".cache" / "terrariabonker" / "templates-x.json"
    target.write_text("{}")
    home_of(str(home))
    monkeypatch.setattr(os, "geteuid", lambda: 0)
    monkeypatch.setenv("SUDO_UID", "1000")
    monkeypatch.setenv("SUDO_GID", "1001")
    proc.give_back_to_user(str(target))
    assert chowns == [(str(target), 1000, 1001)]


def test_a_path_outside_the_users_home_is_left_alone(monkeypatch, chowns, home_of,
                                                     tmp_path):
    """Without ``sudo -E`` the cache lands under /root; handing that over would widen
    write access to a path inside root's home rather than fix anything."""
    home = tmp_path / "u"
    home.mkdir()
    home_of(str(home))
    monkeypatch.setattr(os, "geteuid", lambda: 0)
    monkeypatch.setenv("SUDO_UID", "1000")
    monkeypatch.setenv("SUDO_GID", "1001")
    proc.give_back_to_user("/root/.cache/terrariabonker/templates-x.json")
    assert chowns == []


def test_a_real_root_shell_is_left_alone(monkeypatch, chowns):
    """No SUDO_UID means nobody to hand it back to; do not guess."""
    monkeypatch.setattr(os, "geteuid", lambda: 0)
    monkeypatch.delenv("SUDO_UID", raising=False)
    monkeypatch.delenv("SUDO_GID", raising=False)
    proc.give_back_to_user("/root/.cache/terrariabonker/templates-x.json")
    assert chowns == []


def test_a_junk_sudo_uid_is_ignored(monkeypatch, chowns):
    monkeypatch.setattr(os, "geteuid", lambda: 0)
    monkeypatch.setenv("SUDO_UID", "not-a-number")
    monkeypatch.setenv("SUDO_GID", "1001")
    proc.give_back_to_user("/tmp/whatever")
    assert chowns == []


def test_a_chown_failure_is_not_an_error(monkeypatch):
    """A cache we cannot chown is still a usable cache."""
    monkeypatch.setattr(os, "geteuid", lambda: 0)
    monkeypatch.setenv("SUDO_UID", "1000")
    monkeypatch.setenv("SUDO_GID", "1000")

    def boom(*_a):
        raise PermissionError("read-only")

    monkeypatch.setattr(os, "chown", boom)
    proc.give_back_to_user("/tmp/whatever")     # must not raise


def test_the_template_cache_write_hands_back_both_dir_and_file(monkeypatch, tmp_path):
    """The write path itself must call it — the helper is useless if nothing invokes it."""
    from terrariabonker import proc as proc_mod, service

    handed = []
    monkeypatch.setattr(proc_mod, "give_back_to_user", handed.append)
    monkeypatch.setenv("HOME", str(tmp_path))

    class FakeService:
        build_key = staticmethod(lambda: "1.4.5.7+24893155")

    got = service.Service._template_cache(FakeService(), "templates", lambda: {1: {"type": 1}})

    assert got == {1: {"type": 1}}
    cache_dir = tmp_path / ".cache" / "terrariabonker"
    expected = str(cache_dir / "templates-1.4.5.7-24893155.json")
    assert handed == [str(cache_dir), expected]
    assert os.path.exists(expected)


def test_items_and_npcs_cache_to_different_files(monkeypatch, tmp_path):
    """Both catalogs are keyed by build; only the kind keeps them apart."""
    from terrariabonker import proc as proc_mod, service

    monkeypatch.setattr(proc_mod, "give_back_to_user", lambda _p: None)
    monkeypatch.setenv("HOME", str(tmp_path))

    class FakeService:
        build_key = staticmethod(lambda: "1.4.5.7+24893155")

    service.Service._template_cache(FakeService(), "templates", lambda: {1: {"type": 1}})
    service.Service._template_cache(FakeService(), "npcs", lambda: {2: {"type": 2}})

    cache_dir = tmp_path / ".cache" / "terrariabonker"
    assert sorted(p.name for p in cache_dir.iterdir()) == [
        "npcs-1.4.5.7-24893155.json", "templates-1.4.5.7-24893155.json"]
    # and a second read comes back from the file, not the scan
    boom = lambda: (_ for _ in ()).throw(AssertionError("rescanned"))   # noqa: E731
    assert service.Service._template_cache(FakeService(), "npcs", boom) == {2: {"type": 2}}
