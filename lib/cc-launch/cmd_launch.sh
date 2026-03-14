#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CMD_LAUNCH_LOADED:-}" ]] && return 0
_CC_LAUNCH_CMD_LAUNCH_LOADED=1

cmd_launch_interactive() {
    local picker_script
    picker_script="$(dirname "$(readlink -f "$0")")/../kittens/cc_session_picker.py"

    if [[ ! -f "$picker_script" ]]; then
        echo "Error: Picker script not found: '$picker_script'." >&2
        exit 1
    fi

    local picker_output
    picker_output="$(python3 "$picker_script")" || {
        echo "No sessions selected." >&2
        return
    }

    local launched=0
    local existed=0

    while IFS='|' read -r title dir account flags pw env_vars group; do
        [[ -z "$title" ]] && continue
        title="$(_trim "$title")"
        dir="$(_trim "$dir")"
        account="$(_trim "${account:-work}")"
        flags="$(_trim "${flags:-}")"
        pw="$(_trim "${pw:-}")"
        env_vars="$(_trim "${env_vars:-}")"
        group="$(_trim "${group:-}")"
        [[ -z "$account" ]] && account="work"

        if ! _account_index "$account" >/dev/null 2>&1; then
            echo "Warning: session '$title' references unknown account '$account', skipping." >&2
            echo "  Available accounts: ${_ACCOUNT_NAMES[*]}" >&2
            continue
        fi

        local loc_args
        loc_args="$(resolve_location_args "$SOCKET" "$account" "$title")"

        if launch_session "$title" "$dir" "$account" "$flags" "$pw" "$loc_args" "$env_vars" "$group"; then
            launched=$((launched + 1))
        else
            existed=$((existed + 1))
        fi
    done <<<"$picker_output"

    echo "Launched $launched new session(s), $existed already existed." >&2
}

cmd_launch_all() {
    if [[ ! -f "$CONFIG_FILE" ]]; then
        echo "Error: Config file not found: '$CONFIG_FILE'." >&2
        exit 1
    fi

    local lines=""
    if ! lines="$(python3 "$_CC_COMMON_DIR/toml-helper.py" read-sessions "$CONFIG_FILE")"; then
        echo "Error: Failed to parse sessions config '$CONFIG_FILE'." >&2
        exit 1
    fi

    if [[ "$_DEFAULT_SORT_SESSIONS" == "alpha" ]]; then
        lines="$(echo -n "$lines" | sort -f)"
    fi

    if [[ -n "${_TARGET_GROUP:-}" ]]; then
        local filtered=""
        while IFS=$'\t' read -r title dir account flags pw env_vars group; do
            [[ -z "$title" ]] && continue
            if [[ "$(_trim "$group")" == "$_TARGET_GROUP" ]]; then
                [[ -n "$filtered" ]] && filtered+=$'\n'
                filtered+="${title}	${dir}	${account}	${flags}	${pw}	${env_vars}	${group}"
            fi
        done <<<"$lines"
        if [[ -z "$filtered" ]]; then
            echo "No sessions found in group '$_TARGET_GROUP'." >&2
            # List available groups from config
            local available_groups=""
            while IFS=$'\t' read -r _t _d _a _f _p _e g; do
                [[ -z "$_t" ]] && continue
                g="$(_trim "$g")"
                [[ -z "$g" ]] && continue
                # Deduplicate: only add if not already present
                case ",$available_groups," in
                    *",$g,"*) ;;
                    *) [[ -n "$available_groups" ]] && available_groups+=", "; available_groups+="$g" ;;
                esac
            done <<<"$lines"
            if [[ -n "$available_groups" ]]; then
                echo "  Available groups: $available_groups" >&2
            fi
            return
        fi
        lines="$filtered"
    fi

    local total=0
    while IFS= read -r _line; do
        [[ -n "$_line" ]] && total=$((total + 1))
    done <<<"$lines"
    local current=0
    local skipped=0
    local launched=0
    local existed=0

    while IFS=$'\t' read -r title dir account flags pw env_vars group; do
        [[ -z "$title" ]] && continue
        current=$((current + 1))
        title="$(_trim "$title")"
        dir="$(_trim "$dir")"
        account="$(_trim "${account:-work}")"
        flags="$(_trim "${flags:-}")"
        pw="$(_trim "${pw:-}")"
        env_vars="$(_trim "${env_vars:-}")"
        group="$(_trim "${group:-}")"
        [[ -z "$account" ]] && account="work"

        if ! _account_index "$account" >/dev/null 2>&1; then
            echo "Warning: [$current/$total] session '$title' references unknown account '$account', skipping." >&2
            echo "  Available accounts: ${_ACCOUNT_NAMES[*]}" >&2
            skipped=$((skipped + 1))
            continue
        fi

        local loc_args
        loc_args="$(resolve_location_args "$SOCKET" "$account" "$title")"

        echo "[$current/$total] Launching $title..." >&2
        if launch_session "$title" "$dir" "$account" "$flags" "$pw" "$loc_args" "$env_vars" "$group"; then
            launched=$((launched + 1))
        else
            existed=$((existed + 1))
        fi
    done <<<"$lines"

    echo "Launched $launched new session(s), $existed already existed." >&2
    if [[ "$skipped" -gt 0 ]]; then
        echo "Skipped $skipped session(s) with unknown accounts." >&2
    fi

    local _should_start_watcher=""
    if [[ "${_DEFAULT_NOTIFY_IDLE:-}" == "on" ]] &&
        [[ "${_DEFAULT_IDLE_ALERT_MINUTES:-0}" -gt 0 ]]; then
        _should_start_watcher=1
    fi
    if [[ "${_DEFAULT_DIM_AFTER_MINUTES:-0}" -gt 0 ]]; then
        _should_start_watcher=1
    fi
    if [[ -n "$_should_start_watcher" ]] &&
        command -v cc-idle-watch >/dev/null 2>&1; then
        cc-idle-watch --start
    fi
}

cmd_add() {
    local title=""
    local dir=""
    local account=""
    local env_vars=""
    local group=""

    while [[ $# -gt 0 ]]; do
        case "$1" in
        --group)
            group="${2:-}"
            shift 2
            ;;
        *)
            if [[ -z "$title" ]]; then
                title="$1"
            elif [[ -z "$dir" ]]; then
                dir="$1"
            elif [[ -z "$account" ]]; then
                account="$1"
            else
                env_vars="$1"
            fi
            shift
            ;;
        esac
    done

    if [[ -z "$title" || -z "$dir" ]]; then
        echo "Error: --add requires TITLE and DIR arguments." >&2
        echo "Usage: cc-launch --add TITLE DIR [ACCOUNT [ENV_VARS]] [--group GROUP]" >&2
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
                echo "No account selected." >&2
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

    launch_session "$title" "$dir" "$account" "" "" "$loc_args" "$env_vars" "$group"
}
