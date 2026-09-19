#!/bin/bash

# Configuration
APP_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_NAME="terrariabonker"
APP_ID="io.ushineko.$APP_NAME"
DESKTOP_FILE="$APP_ID.desktop"
INSTALL_DIR="$HOME/.local/share/applications"
BIN_DIR="$HOME/.local/bin"
ICON_DIR="$HOME/.local/share/icons/hicolor/scalable/apps"

for arg in "$@"; do
    case "$arg" in
        -h|--help)
            cat <<USAGE
Usage: $0

Removes the $APP_NAME binaries, the desktop entry and the icon from ~/.local.
Your profile, patch record and icon cache are left alone.
USAGE
            exit 0
            ;;
    esac
done

echo "Uninstalling $APP_NAME..."

for bin in "$APP_NAME" "$APP_NAME-gui"; do
    if [ -e "$BIN_DIR/$bin" ] || [ -L "$BIN_DIR/$bin" ]; then
        rm -f "$BIN_DIR/$bin"
        echo "Removed $BIN_DIR/$bin"
    fi
done

if [ -f "$INSTALL_DIR/$DESKTOP_FILE" ]; then
    rm -f "$INSTALL_DIR/$DESKTOP_FILE"
    echo "Removed $INSTALL_DIR/$DESKTOP_FILE"
fi

if [ -f "$ICON_DIR/$APP_NAME.svg" ]; then
    rm -f "$ICON_DIR/$APP_NAME.svg"
    echo "Removed $ICON_DIR/$APP_NAME.svg"
fi

if command -v update-desktop-database >/dev/null; then
    update-desktop-database "$INSTALL_DIR"
fi

# Take the KWin window-position rule back out (no-op if it was never added).
if [ -f "$APP_DIR/tools/kwin_rule.sh" ]; then
    "$APP_DIR/tools/kwin_rule.sh" --remove || true
fi

if command -v gtk-update-icon-cache >/dev/null; then
    gtk-update-icon-cache -q -t -f "$HOME/.local/share/icons/hicolor" 2>/dev/null || true
fi

echo "Done."
