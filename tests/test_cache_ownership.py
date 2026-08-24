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


def test_root_under_sudo_hands_the_file_back(monkeypatch, chowns):
    monkeypatch.setattr(os, "geteuid", lambda: 0)
    monkeypatch.setenv("SUDO_UID", "1000")
    monkeypatch.setenv("SUDO_GID", "1001")
    proc.give_back_to_user("/home/u/.cache/terrariabonker/templates-x.json")
    assert chowns == [("/home/u/.cache/terrariabonker/templates-x.json", 1000, 1001)]


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
    from terrariabonker import content, proc as proc_mod, service

    handed = []
    monkeypatch.setattr(proc_mod, "give_back_to_user", handed.append)
    monkeypatch.setattr(content, "find_item_templates", lambda _mem, _vt: {1: {"type": 1}})
    monkeypatch.setenv("HOME", str(tmp_path))

    class FakeService:
        mem = None
        build_key = staticmethod(lambda: "1.4.5.7+24893155")
        _item_vtable = staticmethod(lambda: 0xABCD)

    got = service.Service._item_template_cache(FakeService())

    assert got == {1: {"type": 1}}
    cache_dir = tmp_path / ".cache" / "terrariabonker"
    expected = str(cache_dir / "templates-1.4.5.7-24893155.json")
    assert handed == [str(cache_dir), expected]
    assert os.path.exists(expected)
