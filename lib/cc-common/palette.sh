#!/usr/bin/env bash

[[ -n "${_CC_COMMON_PALETTE_LOADED:-}" ]] && return 0
_CC_COMMON_PALETTE_LOADED=1

resolve_palette_color() {
    local color="$1"
    _load_palette
    if ! printf '%s' "$_PALETTE_JSON" | jq -e --arg c "$color" '.colors[$c]' >/dev/null 2>&1; then
        echo "Warning: Unknown color '$color', falling back to gray." >&2
    fi
    printf '%s' "$_PALETTE_JSON" | jq -r --arg c "$color" \
        --arg afg "$_PALETTE_ACTIVE_FG" --arg ifg "$_PALETTE_INACTIVE_FG" '
        (.colors[$c] // .colors["gray"]) |
        "active_bg=\(.active_bg) active_fg=\($afg) inactive_bg=\(.inactive_bg) inactive_fg=\($ifg)"
    '
}

tab_colors_for_account() {
    local account="$1"
    local color
    color="$(_account_color "$account")" || {
        echo "Warning: Unknown account '$account', using gray." >&2
        resolve_palette_color "gray"
        return
    }
    resolve_palette_color "$color"
}
