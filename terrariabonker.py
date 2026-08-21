#!/usr/bin/env python3
"""terrariabonker - thin entry point.

All logic lives in the terrariabonker package. This file exists so .desktop
files, shell aliases and CLI invocations have a stable path to call, and so the
sudo re-exec in proc.elevate() has a concrete script path to hand to sudo.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from terrariabonker.cli import main

if __name__ == "__main__":
    sys.exit(main())
