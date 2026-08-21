"""terrariabonker - a from-scratch live-memory trainer for Terraria.

Re-exports the pieces callers normally want so that:
    from terrariabonker import Mem, find_players, Player, Freezer
works without reaching into submodules.
"""

from terrariabonker.locate import PlayerBlock, find_players, pick_live  # noqa: F401
from terrariabonker.player import Player  # noqa: F401
from terrariabonker.proc import Mem, ProcError, find_pid  # noqa: F401
from terrariabonker.trainer import Freezer  # noqa: F401

__version__ = "0.4.0"
