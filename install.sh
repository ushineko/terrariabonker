#!/bin/bash

# Configuration
APP_NAME="terrariabonker"
DESKTOP_FILE="$APP_NAME.desktop"
INSTALL_DIR="$HOME/.local/share/applications"
BIN_DIR="$HOME/.local/bin"
APP_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT_PATH="$APP_DIR/terrariabonker.py"
ICON_PATH="$APP_DIR/assets/terrariabonker.svg"

# The Go window (spec 050). Its desktop entry is named for the Wayland app_id it
# sets, because that is what the compositor matches to find a window's icon: an
# entry under any other basename leaves the titlebar showing a placeholder.
GO_GUI="$APP_NAME-gui"
GO_APPID="io.ushineko.$APP_NAME"
GO_DESKTOP_FILE="$GO_APPID.desktop"
ICON_DIR="$HOME/.local/share/icons/hicolor/scalable/apps"

echo "Installing $APP_NAME..."

# 1. Check runtime dependencies before installing anything.
MISSING=""
for mod in numpy PyQt6 PIL; do
    if ! /usr/bin/python3 -c "import $mod" 2>/dev/null; then
        MISSING="$MISSING $mod"
    fi
done
if [ -n "$MISSING" ]; then
    echo "Error: missing Python modules:$MISSING"
    echo "On Arch/CachyOS install them with:"
    echo "  sudo pacman -S python-numpy python-pyqt6 python-pillow"
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

# 4. Install desktop file, resolving Exec/Icon to this install location so the
#    entry works regardless of where the repo is cloned.
if [ -f "$APP_DIR/$DESKTOP_FILE" ]; then
    mkdir -p "$INSTALL_DIR"
    sed -e "s|__SCRIPT__|$SCRIPT_PATH|g" \
        -e "s|__ICON__|$ICON_PATH|g" \
        "$APP_DIR/$DESKTOP_FILE" > "$INSTALL_DIR/$DESKTOP_FILE"
    chmod +x "$INSTALL_DIR/$DESKTOP_FILE"
    echo "Installed desktop file: $INSTALL_DIR/$DESKTOP_FILE"
fi

# 4b. The Go window, when it has been built. Optional on purpose: the Qt panel is
#     still the complete one, and a missing Go toolchain must not fail an install
#     of the trainer itself.
if [ -x "$APP_DIR/$GO_GUI" ] || command -v go >/dev/null; then
    if [ ! -x "$APP_DIR/$GO_GUI" ]; then
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
