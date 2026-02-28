#!/usr/bin/env bash

[[ -n "${_CC_COMMON_UI_LOADED:-}" ]] && return 0
_CC_COMMON_UI_LOADED=1

HAS_GUM=false
if command -v gum >/dev/null 2>&1; then
    HAS_GUM=true
fi

require_gum() {
    if [[ "$HAS_GUM" != "true" ]]; then
        echo "Error: This feature requires gum (charmbracelet/gum)." >&2
        echo "Install it with: brew install gum" >&2
        exit 1
    fi
}

# shellcheck disable=SC2034 # used by scripts that source this library
GUM_HEADER_COLOR="#aaaaaa"
# shellcheck disable=SC2034 # used by scripts that source this library
GUM_ACCENT_COLOR="#2d7d46"

gum_color_for_account() {
    local color
    color="$(_account_color "$1" 2>/dev/null)" || color="gray"
    _load_palette
    local rgb
    rgb=$(printf '%s' "$_PALETTE_JSON" | jq -r --arg c "$color" '
        (.colors[$c] // .colors["gray"]).text_fg | map(tostring) | join(";")
    ')
    local r g b_val
    IFS=';' read -r r g b_val <<<"$rgb"
    printf '#%02x%02x%02x\n' "$r" "$g" "$b_val"
}
