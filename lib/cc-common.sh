#!/usr/bin/env bash
# cc-common.sh — shared infrastructure for cc-manager shell scripts.
# Sourced by cc-launch and cc-status. Do not execute directly.

# Source guard: prevent double-sourcing
[[ -n "${_CC_COMMON_LOADED:-}" ]] && return 0
_CC_COMMON_LOADED=1

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
    case "$color" in
        green)  echo "#2d7d46" ;;
        blue)   echo "#2d5a88" ;;
        red)    echo "#8b2d2d" ;;
        orange) echo "#8b5a2d" ;;
        purple) echo "#5a2d8b" ;;
        teal)   echo "#2d7d7d" ;;
        pink)   echo "#8b2d6a" ;;
        yellow) echo "#7d7d2d" ;;
        gray)   echo "#555555" ;;
        cyan)   echo "#2d6a8b" ;;
        *)      echo "#555555" ;;
    esac
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

load_defaults() {
    _DEFAULT_CLAUDE_FLAGS=""
    _DEFAULT_PROJECT_WINDOW=""

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
    case "$color" in
        green)  echo "active_bg=#2d7d46 active_fg=#ffffff inactive_bg=#1a4a2a inactive_fg=#aaaaaa" ;;
        blue)   echo "active_bg=#2d5a88 active_fg=#ffffff inactive_bg=#1a3655 inactive_fg=#aaaaaa" ;;
        red)    echo "active_bg=#8b2d2d active_fg=#ffffff inactive_bg=#551a1a inactive_fg=#aaaaaa" ;;
        orange) echo "active_bg=#8b5a2d active_fg=#ffffff inactive_bg=#553a1a inactive_fg=#aaaaaa" ;;
        purple) echo "active_bg=#5a2d8b active_fg=#ffffff inactive_bg=#3a1a55 inactive_fg=#aaaaaa" ;;
        teal)   echo "active_bg=#2d7d7d active_fg=#ffffff inactive_bg=#1a4a4a inactive_fg=#aaaaaa" ;;
        pink)   echo "active_bg=#8b2d6a active_fg=#ffffff inactive_bg=#551a42 inactive_fg=#aaaaaa" ;;
        yellow) echo "active_bg=#7d7d2d active_fg=#ffffff inactive_bg=#4a4a1a inactive_fg=#aaaaaa" ;;
        gray)   echo "active_bg=#555555 active_fg=#ffffff inactive_bg=#333333 inactive_fg=#aaaaaa" ;;
        cyan)   echo "active_bg=#2d6a8b active_fg=#ffffff inactive_bg=#1a4255 inactive_fg=#aaaaaa" ;;
        *)
            echo "Warning: Unknown color '$color', falling back to gray." >&2
            echo "active_bg=#555555 active_fg=#ffffff inactive_bg=#333333 inactive_fg=#aaaaaa"
            ;;
    esac
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
