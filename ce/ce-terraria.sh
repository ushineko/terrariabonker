#!/bin/bash
# Launch Cheat Engine inside Terraria's Proton/Wine prefix (appid 105600) so it
# shares the process space and can attach to Terraria.exe. CE runs its autorun/
# Lua on startup (no interaction). The 32-bit CE matches the 32-bit game and its
# 32-bit MonoDataCollector.
#
# This is the manual launcher for the spike; the PyQt manager will invoke the
# same wine command as a managed child, gated on Terraria being up.
set -euo pipefail
APPID=105600
CE_EXE='C:\Program Files\Cheat Engine\cheatengine-i386.exe'

command -v protontricks >/dev/null || { echo "protontricks not found"; exit 1; }
exec protontricks --no-bwrap -c "wine \"$CE_EXE\"" "$APPID"
