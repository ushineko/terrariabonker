#!/bin/bash

# Configuration
APP_NAME="terrariabonker"
INSTALL_DIR="$HOME/.local/share/applications"
BIN_DIR="$HOME/.local/bin"
APP_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT_PATH="$APP_DIR/terrariabonker.py"
ICON_PATH="$APP_DIR/assets/terrariabonker.svg"

# The window (spec 050). Its desktop entry is named for the Wayland app_id it
# sets, because that is what the compositor matches to find a window's icon: an
# entry under any other basename leaves the titlebar showing a placeholder.
GO_GUI="$APP_NAME-gui"
GO_APPID="io.ushineko.$APP_NAME"
GO_DESKTOP_FILE="$GO_APPID.desktop"
ICON_DIR="$HOME/.local/share/icons/hicolor/scalable/apps"

echo "Installing $APP_NAME..."

# 1. Check runtime dependencies before installing anything.
MISSING=""
for mod in numpy PIL; do
    if ! /usr/bin/python3 -c "import $mod" 2>/dev/null; then
        MISSING="$MISSING $mod"
    fi
done
if [ -n "$MISSING" ]; then
    echo "Error: missing Python modules:$MISSING"
    echo "On Arch/CachyOS install them with:"
    echo "  sudo pacman -S python-numpy python-pillow"
    echo "or, from this directory:  pip install -r requirements.txt"
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

# 4. The window. It is the only control panel now (the PyQt6 one is gone, spec
#    050), but a missing Go toolchain still must not fail an install of the CLI:
#    the trainer works from a terminal and a prebuilt binary may already be here.
if [ -x "$APP_DIR/$GO_GUI" ] || command -v go >/dev/null; then
    # Rebuilt on every install rather than only when missing: an install after a
    # change that left the old binary in place would report success and start
    # yesterday's window.
    if command -v go >/dev/null; then
        echo "Building $GO_GUI..."
        ( cd "$APP_DIR" && CGO_ENABLED=1 go build \
            -ldflags "-X main.version=$(sed -n 's/^__version__ = "\(.*\)"/\1/p' "$APP_DIR/terrariabonker/__init__.py")" \
            -o "$GO_GUI" ./cmd/terrariabonker-gui ) || echo "  skipped: the Go build failed"
    fi
    if [ -x "$APP_DIR/$GO_GUI" ]; then
        ln -sfn "$APP_DIR/$GO_GUI" "$BIN_DIR/$GO_GUI"
        echo "Installed GUI symlink: $BIN_DIR/$GO_GUI -> $APP_DIR/$GO_GUI"

        # The icon goes into the theme under the name the desktop entry asks for.
        mkdir -p "$ICON_DIR"
        cp -f "$ICON_PATH" "$ICON_DIR/$APP_NAME.svg"
        echo "Installed icon: $ICON_DIR/$APP_NAME.svg"

        if [ -f "$APP_DIR/$GO_DESKTOP_FILE" ]; then
            mkdir -p "$INSTALL_DIR"
            # Copied as-is: Exec is the bare command name, found on PATH via the
            # symlink above, as the sibling programs' entries do. An absolute
            # path here would bake this checkout's location into an installed
            # file.
            cp -f "$APP_DIR/$GO_DESKTOP_FILE" "$INSTALL_DIR/$GO_DESKTOP_FILE"
            chmod +x "$INSTALL_DIR/$GO_DESKTOP_FILE"
            echo "Installed desktop file: $INSTALL_DIR/$GO_DESKTOP_FILE"
        fi
    else
        echo "Note: no $GO_GUI binary and no Go toolchain -- the CLI is installed,"
        echo "      the control panel is not. Install Go and re-run this."
    fi
fi

if command -v update-desktop-database >/dev/null; then
    update-desktop-database "$INSTALL_DIR"
fi
if command -v gtk-update-icon-cache >/dev/null; then
    gtk-update-icon-cache -q -t -f "$HOME/.local/share/icons/hicolor" 2>/dev/null || true
fi

# 5. Ask KWin to remember the panel's position. Qt cannot do this itself under Wayland:
#    move() is a silent no-op and pos() reports the requested value rather than the real
#    one, so the compositor owns placement. Skips itself on non-KDE desktops.
if [ -f "$APP_DIR/tools/kwin_rule.py" ]; then
    python3 "$APP_DIR/tools/kwin_rule.py" || true
fi

echo "Done. Run '$APP_NAME status' (it will elevate via sudo)."
