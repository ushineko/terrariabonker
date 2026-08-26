#!/usr/bin/env bash
# Capture the terrariabonker panel for the README, on KDE/Wayland.
#
# Two things make this less trivial than "take a screenshot":
#
#   1. The active window is almost never the panel. Refreshing these usually means an
#      agent or a terminal driving the capture, so whatever has focus is the terminal.
#      The panel is therefore raised first, found by window *class* -- searching by name
#      also matches a browser sitting on the project's GitHub page.
#   2. When a dialog is open (a recipe, an NPC) the dialog *is* the active window, so an
#      active-window grab returns the dialog alone on a transparent background. For those
#      shots pass --with-dialog: it captures the whole desktop and crops to the panel's
#      geometry, keeping the dialog where it actually sits.
#
# Requires kdotool (Wayland's xdotool) and spectacle, both KDE.
set -euo pipefail

usage() {
    cat <<'USAGE'
usage: tools/screenshot.sh [--with-dialog] <output.png>

  --with-dialog   the panel has a dialog open: capture the desktop and crop, rather than
                  grabbing the active window (which would be the dialog on its own)

Refreshing the README set (switch tabs by hand between runs):
  tools/screenshot.sh                assets/screenshot-effects.png
  tools/screenshot.sh                assets/screenshot-patches.png
  tools/screenshot.sh                assets/screenshot-inventory.png
  tools/screenshot.sh                assets/screenshot-recipes.png
  tools/screenshot.sh                assets/screenshot-compendium.png

Clicking through six tabs against a timer is miserable, and the v0.34.0 set was
captured by a throwaway script instead: build a MainWindow in-process (skipping the
single-instance guard), walk tabs.setCurrentIndex, set any filter the shot wants, resize
to the page, and shell out to this script between steps. Note that widgets populated
from the catalog -- the Compendium's kind dropdown -- can only be set *after* the load
settles, not at tab-switch time.
USAGE
}

with_dialog=0
out=""
while [ $# -gt 0 ]; do
    case "$1" in
        --with-dialog) with_dialog=1 ;;
        -h|--help) usage; exit 0 ;;
        -*) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
        *) out="$1" ;;
    esac
    shift
done
[ -n "$out" ] || { usage >&2; exit 2; }

for tool in kdotool spectacle python3; do
    command -v "$tool" >/dev/null || { echo "$tool is not installed" >&2; exit 1; }
done

wid=$(timeout 15 kdotool search --class terrariabonker 2>/dev/null | head -1 || true)
[ -n "$wid" ] || { echo "the panel is not running (no window of class terrariabonker)" >&2; exit 1; }
timeout 10 kdotool windowactivate "$wid" >/dev/null 2>&1 || true
sleep 1.2                       # let it raise and repaint before the grab

rm -f "$out"
if [ "$with_dialog" -eq 0 ]; then
    # -S drops the compositor's drop shadow, which otherwise pads the image unevenly
    timeout 30 spectacle -a -b -n -S -o "$out" >/dev/null 2>&1 || true
    sleep 1.5
else
    tmp=$(mktemp --suffix=.png)
    trap 'rm -f "$tmp"' EXIT
    timeout 30 spectacle -f -b -n -o "$tmp" >/dev/null 2>&1 || true
    sleep 1.5
    geo=$(timeout 10 kdotool getwindowgeometry "$wid")
    python3 - "$tmp" "$out" \
        "$(printf '%s' "$geo" | awk '/Position/{print $2}')" \
        "$(printf '%s' "$geo" | awk '/Geometry/{print $2}')" <<'PY'
import sys
from PIL import Image

src, out, pos, dim = sys.argv[1:5]
x, y = (float(v) for v in pos.split(","))
w, h = (float(v) for v in dim.split("x"))
im = Image.open(src)


def scale_at(px, py):
    """The scale factor of the output containing logical point (px, py).

    kdotool reports logical coordinates while the capture is in device pixels, so the
    crop needs the factor between them. Read per-output from kscreen-doctor rather than
    hardcoded: a mixed-scale multi-monitor setup has no single answer, and Qt cannot help
    because an offscreen QGuiApplication invents an 800x800 screen at ratio 1.0.
    """
    import re
    import subprocess
    try:
        raw = subprocess.run(["kscreen-doctor", "-o"], capture_output=True,
                             text=True, timeout=15).stdout
    except (OSError, subprocess.SubprocessError):
        return 1.0
    raw = re.sub(r"\x1b\[[0-9;]*m", "", raw)          # strip the colour codes
    best, geo = 1.0, None
    for line in raw.splitlines():
        m = re.search(r"Geometry:\s*(-?\d+),(-?\d+)\s+(\d+)x(\d+)", line)
        if m:
            geo = tuple(int(v) for v in m.groups())
            continue
        m = re.search(r"Scale:\s*([\d.]+)", line)
        if m and geo:
            gx, gy, gw, gh = geo
            if gx <= px < gx + gw and gy <= py < gy + gh:
                return float(m.group(1))
            best = float(m.group(1))                   # fall back to the last seen
            geo = None
    return best


scale = scale_at(x, y)
box = (round(x * scale), round(y * scale), round((x + w) * scale), round((y + h) * scale))
box = (max(0, box[0]), max(0, box[1]), min(im.width, box[2]), min(im.height, box[3]))
im.crop(box).save(out)
PY
fi

[ -s "$out" ] || { echo "capture produced nothing" >&2; exit 1; }
python3 - "$out" <<'PY'
import os
import sys
from PIL import Image

p = sys.argv[1]
im = Image.open(p)
print("  %s  %dx%d  %.0fK" % (os.path.basename(p), im.width, im.height,
                              os.path.getsize(p) / 1024))
PY
