#!/usr/bin/env bash
# cc-title-icons.sh — Shared functions for tab title icon management.
# Sourced by cc-toggle-star and cc-hook-indicator.

# Source cc-common.sh for icon loading (source guard prevents double-sourcing).
_CC_TITLE_ICONS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "$_CC_TITLE_ICONS_DIR/cc-common.sh" ]]; then
    source "$_CC_TITLE_ICONS_DIR/cc-common.sh"
else
    # Follow symlink to find the real lib directory
    _real_path="$(readlink "${BASH_SOURCE[0]}" || echo "${BASH_SOURCE[0]}")"
    _real_dir="$(cd "$(dirname "$_real_path")" && pwd)"
    source "$_real_dir/cc-common.sh"
fi
_load_icons

# Remove leading ★and state icon prefixes from a title string.
strip_icons() {
    local title="$1"
    title="${title#"$_ICON_STAR"}"
    title="${title#"$_ICON_BOLT"}"
    title="${title#"$_ICON_STOP"}"
    title="${title#"$_ICON_KEY"}"
    title="${title#"$_ICON_ERROR"}"
    echo "$title"
}

# Detect which state icon is currently present (after stripping ★).
# Outputs one of: bolt, stop, key, error, or empty string.
detect_state() {
    local title="$1"
    title="${title#"$_ICON_STAR"}"
    case "$title" in
        "$_ICON_BOLT"*) echo "bolt" ;;
        "$_ICON_STOP"*) echo "stop" ;;
        "$_ICON_KEY"*)  echo "key" ;;
        "$_ICON_ERROR"*) echo "error" ;;
        *)              echo "" ;;
    esac
}

# Reconstruct a title from a base string, star flag, and state name.
# Order is always: ★<state-icon> base
# state is one of: bolt, stop, key, error, or empty string.
build_title() {
    local base="$1" has_star="$2" state="$3"
    local result="$base"
    case "$state" in
        bolt)  result="${_ICON_BOLT}$result" ;;
        stop)  result="${_ICON_STOP}$result" ;;
        key)   result="${_ICON_KEY}$result" ;;
        error) result="${_ICON_ERROR}$result" ;;
    esac
    if [[ "$has_star" == "true" ]]; then
        result="${_ICON_STAR}$result"
    fi
    echo "$result"
}

# Look up a tab by window ID via kitten @ ls.
# Outputs two lines: tab_id and current title.
# Usage: get_tab_info WINDOW_ID [--to SOCKET]
get_tab_info() {
    local win_id="$1"
    shift
    local to_args=()
    if [[ "${1:-}" == "--to" ]]; then
        to_args=("--to" "$2")
    fi
    kitten @ ${to_args[@]+"${to_args[@]}"} ls 2>/dev/null | jq -r --argjson wid "$win_id" '
        [.[] | .tabs[] | select(.windows[] | .id == $wid)] | first |
        "\(.id)\n\(.title)"
    '
}
