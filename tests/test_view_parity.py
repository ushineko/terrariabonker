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


@pytest.mark.parametrize("argv", client.SAMPLE_ARGVS, ids=lambda a: a[0])
def test_client_argvs_parse_against_the_cli(argv):
    """Each argv the client emits must parse cleanly (right subcommand + flags)."""
    args = build_parser().parse_args(argv)   # SystemExit here = a contract drift
    assert getattr(args, "func", None) is not None


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
