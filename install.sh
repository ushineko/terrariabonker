#!/bin/bash

# Configuration
APP_NAME="terrariabonker"
DESKTOP_FILE="$APP_NAME.desktop"
INSTALL_DIR="$HOME/.local/share/applications"
BIN_DIR="$HOME/.local/bin"
SCRIPT_PATH="$(cd "$(dirname "$0")" && pwd)/terrariabonker.py"

echo "Installing $APP_NAME..."

# 1. Check runtime dependencies before installing anything.
MISSING=""
for mod in numpy PyQt6; do
    if ! /usr/bin/python3 -c "import $mod" 2>/dev/null; then
        MISSING="$MISSING $mod"
    fi
done
if [ -n "$MISSING" ]; then
    echo "Error: missing Python modules:$MISSING"
    echo "On Arch/CachyOS install them with:"
    echo "  sudo pacman -S python-numpy python-pyqt6"
    exit 1
fi

# 2. The trainer reads and writes another process's memory, which needs root on
#    this box (kernel.yama.ptrace_scope=1). Warn if passwordless sudo is absent;
#    the tool re-execs itself under sudo, so a NOPASSWD entry keeps it seamless.
if ! sudo -n true 2>/dev/null; then
    echo "Note: '$APP_NAME' needs sudo to access game memory. Without passwordless"
    echo "      sudo you will be prompted for a password each run."
fi

# 3. Install CLI symlink in ~/.local/bin (primary entry point).
mkdir -p "$BIN_DIR"
chmod +x "$SCRIPT_PATH"
ln -sfn "$SCRIPT_PATH" "$BIN_DIR/$APP_NAME"
echo "Installed CLI symlink: $BIN_DIR/$APP_NAME -> $SCRIPT_PATH"

# 4. Install desktop file (opens a terminal running godmode).
if [ -f "./$DESKTOP_FILE" ]; then
    mkdir -p "$INSTALL_DIR"
    cp "./$DESKTOP_FILE" "$INSTALL_DIR/"
    chmod +x "$INSTALL_DIR/$DESKTOP_FILE"
    echo "Installed desktop file: $INSTALL_DIR/$DESKTOP_FILE"
fi

if command -v update-desktop-database >/dev/null; then
    update-desktop-database "$INSTALL_DIR"
fi

echo "Done. Run '$APP_NAME status' (it will elevate via sudo)."
