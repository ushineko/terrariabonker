#!/bin/bash

# Configuration
APP_NAME="terrariabonker"
DESKTOP_FILE="$APP_NAME.desktop"
INSTALL_DIR="$HOME/.local/share/applications"
BIN_DIR="$HOME/.local/bin"

for arg in "$@"; do
    case "$arg" in
        -h|--help)
            cat <<USAGE
Usage: $0

Removes the $APP_NAME CLI symlink and desktop entry. The tool keeps no config
or state of its own, so there is nothing else to purge.
USAGE
            exit 0
            ;;
    esac
done

echo "Uninstalling $APP_NAME..."

if [ -L "$BIN_DIR/$APP_NAME" ]; then
    rm -f "$BIN_DIR/$APP_NAME"
    echo "Removed $BIN_DIR/$APP_NAME"
fi

if [ -f "$INSTALL_DIR/$DESKTOP_FILE" ]; then
    rm -f "$INSTALL_DIR/$DESKTOP_FILE"
    echo "Removed $INSTALL_DIR/$DESKTOP_FILE"
fi

if command -v update-desktop-database >/dev/null; then
    update-desktop-database "$INSTALL_DIR"
fi

echo "Done."
