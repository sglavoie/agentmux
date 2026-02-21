#!/usr/bin/env bash
# cc-title-icons.sh — Shared functions for tab title icon management.
# Sourced by cc-toggle-star and cc-hook-indicator.

# Remove leading ★ and ⚡ prefixes from a title string.
strip_icons() {
    local title="$1"
    title="${title#★ }"
    title="${title#⚡ }"
    echo "$title"
}

# Reconstruct a title from a base string plus boolean flags.
# Order is always: ★ ⚡ base
build_title() {
    local base="$1" has_star="$2" has_bolt="$3"
    local result="$base"
    if [[ "$has_bolt" == "true" ]]; then
        result="⚡ $result"
    fi
    if [[ "$has_star" == "true" ]]; then
        result="★ $result"
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
