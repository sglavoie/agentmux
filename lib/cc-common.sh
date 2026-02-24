#!/usr/bin/env bash
# cc-common.sh — shared infrastructure for cc-manager shell scripts.
# Sourced by cc-launch and cc-status. Do not execute directly.

# Source guard: prevent double-sourcing
[[ -n "${_CC_COMMON_LOADED:-}" ]] && return 0
_CC_COMMON_LOADED=1

# ---------------------------------------------------------------------------
# Data directory resolution and JSON loading
# ---------------------------------------------------------------------------
_CC_DATA_DIR=""
_PALETTE_JSON=""
_ICONS_JSON=""
_PALETTE_ACTIVE_FG=""
_PALETTE_INACTIVE_FG=""
_ICON_STAR=""
_ICON_BOLT=""
_ICON_STOP=""
_ICON_KEY=""
_ICON_ERROR=""

_resolve_data_dir() {
    [[ -n "$_CC_DATA_DIR" ]] && return 0
    local script_path lib_dir
    script_path="${BASH_SOURCE[0]}"
    if [[ -L "$script_path" ]]; then
        script_path="$(readlink "$script_path")"
    fi
    lib_dir="$(cd "$(dirname "$script_path")" && pwd)"
    # Repo layout: lib/ is sibling to data/
    if [[ -f "$lib_dir/../data/palette.json" ]]; then
        _CC_DATA_DIR="$(cd "$lib_dir/../data" && pwd)"
    # Installed layout: JSON files symlinked alongside cc-common.sh
    elif [[ -f "$lib_dir/palette.json" ]]; then
        _CC_DATA_DIR="$lib_dir"
    else
        echo "Error: Cannot find data directory (palette.json) from $lib_dir" >&2
        return 1
    fi
}

_load_palette() {
    [[ -n "$_PALETTE_JSON" ]] && return 0
    _resolve_data_dir
    _PALETTE_JSON="$(< "$_CC_DATA_DIR/palette.json")"
    _PALETTE_ACTIVE_FG=$(printf '%s' "$_PALETTE_JSON" | jq -r '.active_fg')
    _PALETTE_INACTIVE_FG=$(printf '%s' "$_PALETTE_JSON" | jq -r '.inactive_fg')
}

_load_icons() {
    [[ -n "$_ICONS_JSON" ]] && return 0
    _resolve_data_dir
    _ICONS_JSON="$(< "$_CC_DATA_DIR/icons.json")"
    _ICON_STAR=$(printf '%s' "$_ICONS_JSON" | jq -r '.star')
    _ICON_BOLT=$(printf '%s' "$_ICONS_JSON" | jq -r '.states.bolt')
    _ICON_STOP=$(printf '%s' "$_ICONS_JSON" | jq -r '.states.stop')
    _ICON_KEY=$(printf '%s' "$_ICONS_JSON" | jq -r '.states.key')
    _ICON_ERROR=$(printf '%s' "$_ICONS_JSON" | jq -r '.states.error')
}

# Build a jq-compatible regex pattern for stripping icon prefixes.
# Output: ^(★)?(⚡|🛑|🔑|⚠️)?
_icon_strip_regex() {
    _load_icons
    local states
    states=$(printf '%s' "$_ICONS_JSON" | jq -r '
        [.state_order[] as $k | .states[$k]] | join("|")
    ')
    printf '^(%s)?(%s)?' "$_ICON_STAR" "$states"
}

# ---------------------------------------------------------------------------
# Socket discovery
# ---------------------------------------------------------------------------
discover_socket() {
    if [[ -n "${KITTY_LISTEN_ON:-}" ]]; then
        echo "$KITTY_LISTEN_ON"
        return
    fi

    local latest=""
    latest="$(ls -t /tmp/kitty-* 2>/dev/null | head -n1)" || true
    if [[ -n "$latest" ]]; then
        echo "unix:$latest"
        return
    fi

    echo "Error: No kitty socket found. Set KITTY_LISTEN_ON or ensure kitty is running with remote control enabled." >&2
    exit 1
}

# ---------------------------------------------------------------------------
# Gum detection and styling
# ---------------------------------------------------------------------------
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

GUM_HEADER_COLOR="#aaaaaa"
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
    IFS=';' read -r r g b_val <<< "$rgb"
    printf '#%02x%02x%02x\n' "$r" "$g" "$b_val"
}

# ---------------------------------------------------------------------------
# ANSI formatting helpers (shared by cc-status and cc-launch --list)
# ---------------------------------------------------------------------------

# Map palette color name → 24-bit ANSI foreground escape (uses text_fg RGB)
ansi_fg_for_color() {
    local color="$1"
    _load_palette
    printf '\033[38;2;%sm' "$(printf '%s' "$_PALETTE_JSON" | jq -r --arg c "$color" '
        (.colors[$c] // .colors["gray"]).text_fg | map(tostring) | join(";")
    ')"
}

# Convenience: look up account color, then return ANSI foreground
ansi_fg_for_account() {
    local color
    color="$(_account_color "$1" 2>/dev/null)" || color="gray"
    ansi_fg_for_color "$color"
}

# Replace $HOME prefix with ~
shorten_path() {
    local path="$1"
    if [[ "$path" == "$HOME"* ]]; then
        echo "~${path#"$HOME"}"
    else
        echo "$path"
    fi
}

# Print a character N times (Bash 3 compatible)
repeat_char() {
    local ch="$1" count="$2"
    local i
    for ((i = 0; i < count; i++)); do
        printf '%s' "$ch"
    done
}

# Return extra bytes beyond character count (for printf padding with multibyte)
extra_bytes() {
    local str="$1"
    local byte_len char_len
    byte_len=$(printf '%s' "$str" | LC_ALL=C wc -c)
    char_len=${#str}
    echo $(( byte_len - char_len ))
}

# Detect whether color output is supported; sets BOLD, DIM, RESET, USE_COLOR
init_color() {
    USE_COLOR=false
    BOLD="" DIM="" RESET=""
    if [[ -t 1 ]] && [[ -z "${NO_COLOR:-}" ]] && [[ "${TERM:-}" != "dumb" ]]; then
        USE_COLOR=true
        BOLD=$'\033[1m'
        DIM=$'\033[2m'
        RESET=$'\033[0m'
    fi
}

# ---------------------------------------------------------------------------
# Config file paths
# ---------------------------------------------------------------------------
CONFIG_FILE="${CC_MANAGER_CONFIG:-$HOME/.config/cc-manager/sessions.conf}"
ACCOUNTS_CONFIG="${CC_MANAGER_ACCOUNTS:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager/accounts.conf}"
DEFAULTS_CONFIG="${CC_MANAGER_DEFAULTS:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager/defaults.conf}"

# ---------------------------------------------------------------------------
# Tilde expansion for directory paths
# ---------------------------------------------------------------------------
expand_tilde() {
    local path="$1"
    if [[ "$path" == "~"* ]]; then
        echo "${HOME}${path:1}"
    else
        echo "$path"
    fi
}

# ---------------------------------------------------------------------------
# Account loading from accounts.conf
# ---------------------------------------------------------------------------
# Parallel indexed arrays (bash 3 compatible)
_ACCOUNT_NAMES=()
_ACCOUNT_DIRS=()
_ACCOUNT_COLORS=()

load_accounts() {
    _ACCOUNT_NAMES=()
    _ACCOUNT_DIRS=()
    _ACCOUNT_COLORS=()

    if [[ -f "$ACCOUNTS_CONFIG" ]]; then
        while IFS= read -r line || [[ -n "$line" ]]; do
            [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
            local name config_dir color
            IFS='|' read -r name config_dir color <<< "$line"
            name="$(echo "$name" | xargs)"
            config_dir="$(echo "$config_dir" | xargs)"
            color="$(echo "$color" | xargs)"
            config_dir="$(expand_tilde "$config_dir")"
            [[ -z "$name" ]] && continue
            _ACCOUNT_NAMES+=("$name")
            _ACCOUNT_DIRS+=("$config_dir")
            _ACCOUNT_COLORS+=("${color:-gray}")
        done < "$ACCOUNTS_CONFIG"
    else
        _ACCOUNT_NAMES=(work personal)
        _ACCOUNT_DIRS=("$HOME/.claude" "$HOME/.claude-personal")
        _ACCOUNT_COLORS=(green blue)
        echo "Note: No accounts.conf found at $ACCOUNTS_CONFIG; using default work/personal accounts." >&2
    fi
}

# Lookup helpers for the parallel arrays
_account_index() {
    local target="$1"
    local i
    for ((i = 0; i < ${#_ACCOUNT_NAMES[@]}; i++)); do
        if [[ "${_ACCOUNT_NAMES[$i]}" == "$target" ]]; then
            echo "$i"
            return 0
        fi
    done
    return 1
}

_account_dir() {
    local idx
    idx="$(_account_index "$1")" || return 1
    echo "${_ACCOUNT_DIRS[$idx]}"
}

_account_color() {
    local idx
    idx="$(_account_index "$1")" || return 1
    echo "${_ACCOUNT_COLORS[$idx]}"
}

_all_account_names() {
    local IFS=' '
    echo "${_ACCOUNT_NAMES[*]}"
}

# ---------------------------------------------------------------------------
# Defaults loading from defaults.conf
# ---------------------------------------------------------------------------
_DEFAULT_CLAUDE_FLAGS=""
_DEFAULT_PROJECT_WINDOW=""
_DEFAULT_SORT_SESSIONS=""
_DEFAULT_DASHBOARD_REFRESH=""

load_defaults() {
    _DEFAULT_CLAUDE_FLAGS=""
    _DEFAULT_PROJECT_WINDOW=""
    _DEFAULT_SORT_SESSIONS=""
    _DEFAULT_DASHBOARD_REFRESH=""

    [[ -f "$DEFAULTS_CONFIG" ]] || return 0

    while IFS= read -r line || [[ -n "$line" ]]; do
        [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
        local key value
        IFS='=' read -r key value <<< "$line"
        key="$(echo "$key" | xargs)"
        value="$(echo "$value" | xargs)"
        case "$key" in
            claude_flags)
                _DEFAULT_CLAUDE_FLAGS="$value"
                ;;
            project_window)
                _DEFAULT_PROJECT_WINDOW="$value"
                ;;
            sort_sessions)
                _DEFAULT_SORT_SESSIONS="$value"
                ;;
            dashboard_refresh)
                _DEFAULT_DASHBOARD_REFRESH="$value"
                ;;
        esac
    done < "$DEFAULTS_CONFIG"
}

# ---------------------------------------------------------------------------
# Resolve effective claude flags for a session
# ---------------------------------------------------------------------------
resolve_claude_flags() {
    local override="${1:-}"
    if [[ -z "$override" ]]; then
        echo "$_DEFAULT_CLAUDE_FLAGS"
    elif [[ "$override" == "-" ]]; then
        echo ""
    else
        echo "$override"
    fi
}

# ---------------------------------------------------------------------------
# Resolve effective project window layout for a session
# ---------------------------------------------------------------------------
resolve_project_window() {
    local override="${1:-}"
    if [[ -z "$override" ]]; then
        echo "$_DEFAULT_PROJECT_WINDOW"
    elif [[ "$override" == "-" ]]; then
        echo ""
    else
        echo "$override"
    fi
}

# ---------------------------------------------------------------------------
# Color palette (10 named colors)
# ---------------------------------------------------------------------------
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

# ---------------------------------------------------------------------------
# Account → config dir mapping
# ---------------------------------------------------------------------------
claude_config_dir_for_account() {
    local account="$1"
    local config_dir
    config_dir="$(_account_dir "$account")" || {
        echo "Error: Unknown account '$account'. Available: $(_all_account_names)" >&2
        return 1
    }
    echo "$config_dir"
}

# ---------------------------------------------------------------------------
# Account → tab color mapping
# ---------------------------------------------------------------------------
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

# ---------------------------------------------------------------------------
# Build JSON mapping of config_dir → account_name (for jq --argjson)
# ---------------------------------------------------------------------------
build_accounts_json() {
    local json="{"
    local first=true
    local i
    for ((i = 0; i < ${#_ACCOUNT_NAMES[@]}; i++)); do
        [[ "$first" == "true" ]] || json+=","
        first=false
        json+="$(printf '%s:%s' \
            "$(echo "${_ACCOUNT_DIRS[$i]}" | jq -Rs '.[:-1]')" \
            "$(echo "${_ACCOUNT_NAMES[$i]}" | jq -Rs '.[:-1]')")"
    done
    json+="}"
    echo "$json"
}

# ---------------------------------------------------------------------------
# Find the account name that uses the default config dir
# ---------------------------------------------------------------------------
default_account_name() {
    local i
    for ((i = 0; i < ${#_ACCOUNT_NAMES[@]}; i++)); do
        local dir="${_ACCOUNT_DIRS[$i]}"
        if [[ -z "$dir" || "$dir" == "$HOME/.claude" ]]; then
            echo "${_ACCOUNT_NAMES[$i]}"
            return
        fi
    done
    echo "work"
}
