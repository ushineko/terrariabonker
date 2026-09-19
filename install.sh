#!/bin/bash
#
# Build and install terrariabonker into ~/.local.
#
# Two binaries, and the split between them is the security boundary rather than
# a packaging choice: the CLI runs as root because reading another process's
# memory needs it, and the window does not -- it reaches memory only by running
# the CLI under sudo.

set -u

APP_NAME="terrariabonker"
GUI_NAME="$APP_NAME-gui"
APP_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$HOME/.local/bin"
INSTALL_DIR="$HOME/.local/share/applications"
ICON_DIR="$HOME/.local/share/icons/hicolor/scalable/apps"
ICON_PATH="$APP_DIR/assets/$APP_NAME.svg"

# The desktop entry is named for the Wayland app_id the window sets, because
# that is what the compositor matches to find a window's icon: an entry under
# any other basename leaves the titlebar showing a placeholder.
APP_ID="io.ushineko.$APP_NAME"
DESKTOP_FILE="$APP_ID.desktop"

echo "Installing $APP_NAME..."

if ! command -v go >/dev/null; then
    echo "Error: Go is needed to build $APP_NAME."
    echo "On Arch/CachyOS:  sudo pacman -S go"
    exit 1
fi

# The trainer reads and writes another process's memory, which needs root on a
# box with kernel.yama.ptrace_scope=1. The CLI re-execs itself under sudo, so a
# NOPASSWD sudoers entry keeps that seamless.
if ! sudo -n true 2>/dev/null; then
    echo "Note: '$APP_NAME' needs sudo to reach game memory. Without passwordless"
    echo "      sudo you will be prompted for a password each run."
fi

mkdir -p "$BIN_DIR"

# Rebuilt on every install rather than only when missing: an install after a
# change that left the old binary in place would report success and run
# yesterday's build.
echo "Building $APP_NAME..."
if ! ( cd "$APP_DIR" && make build ); then
    echo "Error: the CLI did not build."
    exit 1
fi
install -m 0755 "$APP_DIR/bin/$APP_NAME" "$BIN_DIR/$APP_NAME"
echo "Installed $BIN_DIR/$APP_NAME"

# The window needs CGO, OpenGL and X11/Wayland headers. A machine without them
# must still get a working CLI: the trainer works from a terminal.
echo "Building $GUI_NAME..."
if ( cd "$APP_DIR" && make build-gui ); then
    install -m 0755 "$APP_DIR/bin/$GUI_NAME" "$BIN_DIR/$GUI_NAME"
    echo "Installed $BIN_DIR/$GUI_NAME"

    mkdir -p "$ICON_DIR"
    install -m 0644 "$ICON_PATH" "$ICON_DIR/$APP_NAME.svg"
    echo "Installed $ICON_DIR/$APP_NAME.svg"

    if [ -f "$APP_DIR/$DESKTOP_FILE" ]; then
        mkdir -p "$INSTALL_DIR"
        # Copied as-is: Exec is the bare command name, found on PATH through the
        # binary above. An absolute path here would bake this checkout's
        # location into an installed file.
        install -m 0755 "$APP_DIR/$DESKTOP_FILE" "$INSTALL_DIR/$DESKTOP_FILE"
        echo "Installed $INSTALL_DIR/$DESKTOP_FILE"
    fi
else
    echo "Note: the window did not build -- the CLI is installed and works."
    echo "      It needs a C toolchain and the OpenGL and X11/Wayland headers:"
    echo "        sudo pacman -S base-devel mesa libx11 libxcursor libxrandr \\"
    echo "                       libxinerama libxi libxkbcommon wayland"
fi

if command -v update-desktop-database >/dev/null; then
    update-desktop-database "$INSTALL_DIR" 2>/dev/null || true
fi
if command -v gtk-update-icon-cache >/dev/null; then
    gtk-update-icon-cache -q -t -f "$HOME/.local/share/icons/hicolor" 2>/dev/null || true
fi

# Ask KWin to remember the window's position. A Wayland client cannot place
# itself -- the compositor owns placement -- so the rule is the only way to have
# it come back where it was left. Skips itself on other desktops.
if [ -f "$APP_DIR/tools/kwin_rule.sh" ]; then
    "$APP_DIR/tools/kwin_rule.sh" || true
fi

case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) echo "Note: $BIN_DIR is not on your PATH." ;;
esac

echo "Done. Run '$APP_NAME status' (it will elevate via sudo)."
