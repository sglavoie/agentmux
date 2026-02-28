#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CMD_LAUNCH_LOADED:-}" ]] && return 0
_CC_LAUNCH_CMD_LAUNCH_LOADED=1

cmd_launch_interactive() {
    local picker_script
    picker_script="$(dirname "$(readlink -f "$0")")/../kittens/cc_session_picker.py"

    if [[ ! -f "$picker_script" ]]; then
        echo "Error: Picker script not found: $picker_script" >&2
        exit 1
    fi

    local picker_output
    picker_output="$(python3 "$picker_script")" || {
        echo "No sessions selected."
        return
    }

    local launched=0
    local existed=0

    while IFS='|' read -r title dir account flags pw env_vars; do
        [[ -z "$title" ]] && continue
        title="$(_trim "$title")"
        dir="$(_trim "$dir")"
        account="$(_trim "${account:-work}")"
        flags="$(_trim "${flags:-}")"
        pw="$(_trim "${pw:-}")"
        env_vars="$(_trim "${env_vars:-}")"
        [[ -z "$account" ]] && account="work"

        local loc_args
        loc_args="$(resolve_location_args "$SOCKET" "$account" "$title")"

        if launch_session "$title" "$dir" "$account" "$flags" "$pw" "$loc_args" "$env_vars"; then
            launched=$((launched + 1))
        else
            existed=$((existed + 1))
        fi
    done <<<"$picker_output"

    echo "Launched $launched new session(s), $existed already existed."
}

cmd_launch_all() {
    if [[ ! -f "$CONFIG_FILE" ]]; then
        echo "Error: Config file not found: $CONFIG_FILE" >&2
        exit 1
    fi

    local lines=""
    while IFS= read -r line || [[ -n "$line" ]]; do
        [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
        lines+="$line"$'\n'
    done <"$CONFIG_FILE"

    if [[ "$_DEFAULT_SORT_SESSIONS" == "alpha" ]]; then
        lines="$(echo -n "$lines" | sort -f)"
    fi

    local launched=0
    local existed=0

    while IFS= read -r line; do
        [[ -z "$line" ]] && continue

        local title dir account flags pw env_vars
        IFS='|' read -r title dir account flags pw env_vars <<<"$line"
        title="$(_trim "$title")"
        dir="$(_trim "$dir")"
        account="$(_trim "${account:-work}")"
        flags="$(_trim "${flags:-}")"
        pw="$(_trim "${pw:-}")"
        env_vars="$(_trim "${env_vars:-}")"
        [[ -z "$account" ]] && account="work"

        local loc_args
        loc_args="$(resolve_location_args "$SOCKET" "$account" "$title")"

        if launch_session "$title" "$dir" "$account" "$flags" "$pw" "$loc_args" "$env_vars"; then
            launched=$((launched + 1))
        else
            existed=$((existed + 1))
        fi
    done <<<"$lines"

    echo "Launched $launched new session(s), $existed already existed."

    if [[ "${_DEFAULT_NOTIFY_IDLE:-}" == "on" ]] &&
        [[ -n "${_DEFAULT_IDLE_ALERT_MINUTES:-}" ]] &&
        [[ "${_DEFAULT_IDLE_ALERT_MINUTES:-0}" -gt 0 ]] &&
        command -v cc-idle-watch >/dev/null 2>&1; then
        cc-idle-watch --start
    fi
}

cmd_add() {
    local title="${1:-}"
    local dir="${2:-}"
    local account="${3:-}"
    local env_vars="${4:-}"

    if [[ -z "$title" || -z "$dir" ]]; then
        echo "Error: --add requires TITLE and DIR arguments." >&2
        echo "Usage: cc-launch --add TITLE DIR [ACCOUNT [ENV_VARS]]" >&2
        exit 1
    fi

    if [[ -z "$account" ]]; then
        if [[ "$HAS_GUM" == "true" && ${#_ACCOUNT_NAMES[@]} -gt 1 ]]; then
            local -a display_items=()
            local idx
            for ((idx = 0; idx < ${#_ACCOUNT_NAMES[@]}; idx++)); do
                display_items+=("${_ACCOUNT_NAMES[$idx]}  (${_ACCOUNT_COLORS[$idx]})")
            done
            local chosen
            chosen="$(printf '%s\n' "${display_items[@]}" | gum choose \
                --header "Select account:" \
                --header.foreground "$GUM_HEADER_COLOR" \
                --cursor.foreground "$GUM_ACCENT_COLOR")" || {
                echo "No account selected."
                return
            }
            account="${chosen%%  (*}"
        elif [[ ${#_ACCOUNT_NAMES[@]} -eq 1 ]]; then
            account="${_ACCOUNT_NAMES[0]}"
        else
            account="work"
        fi
    fi

    local loc_args
    loc_args="$(resolve_location_args "$SOCKET" "$account" "$title")"

    launch_session "$title" "$dir" "$account" "" "" "$loc_args" "$env_vars"
}
