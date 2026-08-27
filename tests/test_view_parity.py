"""The GUI's subprocess contract must match the CLI, and the core stays toolkit-free.

These are the guardrails for the shared-layer design: the drift that let the GUI
call ``inventory --json`` before the CLI supported it is now a test failure, not a
runtime surprise.
"""

import argparse
import ast
from pathlib import Path

import pytest

from terrariabonker.cli import build_parser
from terrariabonker.gui import client

SRC = Path(__file__).resolve().parent.parent / "terrariabonker"


def _subcommands(parser: argparse.ArgumentParser) -> set[str]:
    for action in parser._actions:
        if isinstance(action, argparse._SubParsersAction):
            return set(action.choices)
    return set()


def test_every_client_command_exists_in_cli():
    cmds = _subcommands(build_parser())
    missing = client.COMMANDS - cmds
    assert not missing, f"GUI client uses CLI subcommands that do not exist: {sorted(missing)}"


@pytest.mark.parametrize("name,cmd,argv", client.SAMPLE_ARGVS,
                         ids=[f"{n}:{c}" for n, c, _a in client.SAMPLE_ARGVS])
def test_client_argvs_parse_against_the_cli(name, cmd, argv):
    """Each argv must parse, and must emit the subcommand it is declared to emit.

    Two earlier versions of this assertion were weaker than they looked. `args.func is
    not None` says nothing about which command was reached. Deriving the expected command
    from `argv[0]` only proves `argv[0]` equals itself -- a builder switching to another
    valid subcommand passed it. The expectation is written beside the sample instead.
    """
    assert argv[0] == cmd, f"{name} emits {argv[0]!r}, declared as {cmd!r}"
    args = build_parser().parse_args(argv)   # SystemExit here = a contract drift
    assert getattr(args, "func", None) is not None, f"{name}: no CLI handler for {cmd!r}"


def test_every_argv_builder_has_a_sample():
    """A new builder must be added to SAMPLE_ARGVS, or it is not covered by anything.

    COMMANDS was a hand-written set of names and had gone stale by seven commands --
    catch-tick, catch-stop, fishing-buffs, build-check, accept-build, restore and
    extract-sprites -- so this file silently stopped guarding two releases of new work
    while still reading like coverage. COMMANDS is now derived from these samples, and
    this test is what makes the samples exhaustive.
    """
    import inspect

    builders = {n for n, f in vars(client).items()
                if n.endswith("_argv") and inspect.isfunction(f)}
    covered = {name for name, _cmd, _argv in client.SAMPLE_ARGVS}
    assert not builders - covered, \
        f"argv builders with no sample: {sorted(builders - covered)}"
    assert not covered - builders, \
        f"samples naming builders that no longer exist: {sorted(covered - builders)}"


def _imported_roots(path: Path) -> set[str]:
    tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    roots: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            roots.update(a.name.split(".")[0] for a in node.names)
        elif isinstance(node, ast.ImportFrom) and node.level == 0 and node.module:
            roots.add(node.module.split(".")[0])
    return roots


def test_service_layer_is_toolkit_and_argparse_free():
    """The core must not import a GUI toolkit or argparse, so both shells share it."""
    forbidden = {"PyQt6", "PyQt5", "PySide6", "textual", "curses", "argparse"}
    leaked = _imported_roots(SRC / "service.py") & forbidden
    assert not leaked, f"service.py imports {sorted(leaked)}; the core must stay neutral"


def test_gui_client_is_toolkit_free():
    """The client is pure argv/JSON so the parity test can exercise it without Qt."""
    forbidden = {"PyQt6", "PyQt5", "PySide6"}
    leaked = _imported_roots(SRC / "gui" / "client.py") & forbidden
    assert not leaked, f"gui/client.py imports {sorted(leaked)}; keep it toolkit-free"
