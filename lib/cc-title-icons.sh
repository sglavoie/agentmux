#!/usr/bin/env bash
# cc-title-icons.sh — Shared functions for tab title icon management.
# Sourced by cc-toggle-star and cc-hook-indicator.

# Remove leading ★and state icon prefixes from a title string.
strip_icons() {
    local title="$1"
    title="${title#★}"
    title="${title#⚡}"
    title="${title#🛑}"
    title="${title#🔑}"
    title="${title#⚠️}"
    echo "$title"
}

# Detect which state icon is currently present (after stripping ★).
# Outputs one of: bolt, stop, key, error, or empty string.
detect_state() {
    local title="$1"
    title="${title#★}"
    case "$title" in
        "⚡"*) echo "bolt" ;;
        "🛑"*) echo "stop" ;;
        "🔑"*) echo "key" ;;
        "⚠️"*) echo "error" ;;
        *)      echo "" ;;
    esac
}

# Reconstruct a title from a base string, star flag, and state name.
# Order is always: ★<state-icon> base
# state is one of: bolt, stop, key, error, or empty string.
build_title() {
    local base="$1" has_star="$2" state="$3"
    local result="$base"
    case "$state" in
        bolt) result="⚡$result" ;;
        stop) result="🛑$result" ;;
        key)   result="🔑$result" ;;
        error) result="⚠️$result" ;;
    esac
    if [[ "$has_star" == "true" ]]; then
        result="★$result"
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
