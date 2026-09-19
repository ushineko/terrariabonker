#!/bin/bash
#
# Register (or remove) a KWin rule that remembers the window's position.
#
# Why the compositor and not the app: under KWin/Wayland a client cannot place
# itself and cannot read where it really is (measured on the panel this replaced:
# asked for (700,400), the toolkit reported (700,400), KWin had it at
# (1116,1762)). An app cannot save a position it is not allowed to read, so
# placement is left to KWin, which already has a "Remember" rule type for exactly
# this. The window still owns its own size.
#
# Writes go through kwriteconfig6, so the format is whatever KDE itself considers
# canonical. It rewrites the whole file -- sections reordered, keys sorted -- the
# same normalisation any KDE settings dialog performs. Idempotent: re-running
# refreshes the existing rule instead of adding another, and --remove takes it
# back out.
#
# Skips quietly where the KDE tools are absent, so the installer can call it
# unconditionally.

set -u

FILE="kwinrulesrc"
WMCLASS="terrariabonker"
DESCRIPTION="terrariabonker Remember Position"

# Rule types in kwinrulesrc: 2 = Force, 4 = Remember.
REMEMBER="4"

# Matching on wmclass alone also catches the app's dialogs, and "remember
# position" then pins each one to the window's stored spot instead of letting
# KWin centre it on its parent. Restricting by window type did not help; the
# discriminator is the title, because only the main window's carries the
# version.
TITLE="terrariabonker v"
SUBSTRING_MATCH="2" # 0 unimportant, 1 exact, 2 substring, 3 regex

read_key() { kreadconfig6 --file "$FILE" --group "$1" --key "$2" 2>/dev/null; }
write_key() { kwriteconfig6 --file "$FILE" --group "$1" --key "$2" "$3"; }
delete_key() { kwriteconfig6 --file "$FILE" --group "$1" --key "$2" --delete 2>/dev/null; }

# reconfigure asks KWin to reload its rules, so this takes effect without a
# logout.
reconfigure() {
    if command -v qdbus6 >/dev/null; then
        qdbus6 org.kde.KWin /KWin reconfigure >/dev/null 2>&1 && return
    fi
    if command -v qdbus >/dev/null; then
        qdbus org.kde.KWin /KWin reconfigure >/dev/null 2>&1 && return
    fi
    if command -v gdbus >/dev/null; then
        gdbus call --session --dest org.kde.KWin --object-path /KWin \
            --method org.kde.KWin.reconfigure >/dev/null 2>&1 && return
    fi
}

rules() { read_key General rules | tr ',' '\n' | grep -v '^$'; }

find_existing() {
    local group
    while read -r group; do
        [ "$(read_key "$group" wmclass)" = "$WMCLASS" ] && { echo "$group"; return; }
    done < <(rules)
}

write_rule() {
    local group=$1
    write_key "$group" Description "$DESCRIPTION"
    write_key "$group" wmclass "$WMCLASS"
    write_key "$group" wmclassmatch "1"
    write_key "$group" title "$TITLE"
    write_key "$group" titlematch "$SUBSTRING_MATCH"
    write_key "$group" positionrule "$REMEMBER"
    write_key "$group" screenrule "$REMEMBER"
}

install_rule() {
    local group existing all next
    existing=$(find_existing)
    if [ -n "$existing" ]; then
        write_rule "$existing"
        reconfigure
        echo "[kwin] rule [$existing] already present - refreshed"
        return 0
    fi
    mapfile -t all < <(rules)
    next=1
    for group in "${all[@]:-}"; do
        case "$group" in
            ''|*[!0-9]*) continue ;;
            *) [ "$group" -ge "$next" ] && next=$((group + 1)) ;;
        esac
    done
    all+=("$next")
    write_key General rules "$(printf '%s,' "${all[@]}" | sed 's/,$//')"
    write_key General count "${#all[@]}"
    write_rule "$next"
    reconfigure
    echo "[kwin] added rule [$next] - KWin will remember the window's position"
}

remove_rule() {
    local group key rest
    group=$(find_existing)
    if [ -z "$group" ]; then
        echo "[kwin] no terrariabonker rule to remove"
        return 0
    fi
    for key in Description wmclass wmclassmatch title titlematch positionrule screenrule \
               position screen size types; do
        delete_key "$group" "$key"
    done
    rest=$(rules | grep -v "^$group$" | paste -sd, -)
    write_key General rules "$rest"
    write_key General count "$(rules | grep -c . || true)"
    reconfigure
    echo "[kwin] removed rule [$group]"
}

if ! command -v kwriteconfig6 >/dev/null || ! command -v kreadconfig6 >/dev/null; then
    echo "[kwin] KDE config tools not found - skipping the window-position rule"
    exit 0
fi

case "${1:-}" in
    --remove) remove_rule ;;
    -h|--help) sed -n '3,20p' "$0" | sed 's/^# \{0,1\}//' ;;
    *) install_rule ;;
esac
