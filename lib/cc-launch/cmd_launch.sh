#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CMD_LAUNCH_LOADED:-}" ]] && return 0
_CC_LAUNCH_CMD_LAUNCH_LOADED=1

cmd_launch_interactive() {
    local picker_script
    picker_script="$(dirname "$(readlink -f "$0")")/../kittens/cc_session_picker.py"

    if [[ ! -f "$picker_script" ]]; then
        die "Picker script not found: '$picker_script'."
    fi

    local picker_output
    picker_output="$(python3 "$picker_script")" || {
        echo "No sessions selected." >&2
        return
    }

    local launched=0
    local existed=0

    while IFS=$'\t' read -r title dir account flags pw env_vars groups mute commands; do
        [[ -z "$title" ]] && continue
        title="$(_trim "$title")"
        dir="$(_trim "$dir")"
        account="$(_trim "${account:-work}")"
        flags="$(_trim "${flags:-}")"
        pw="$(_trim "${pw:-}")"
        env_vars="$(_trim "${env_vars:-}")"
        groups="$(_trim "${groups:-}")"
        mute="$(_trim "${mute:-}")"
        commands="$(_trim "${commands:-}")"
        [[ -z "$account" ]] && account="work"

        if ! _account_index "$account" >/dev/null 2>&1; then
            warn "session '$title' references unknown account '$account', skipping."
            echo "  Available accounts: ${_ACCOUNT_NAMES[*]}" >&2
            continue
        fi

        local loc_args
        loc_args="$(resolve_location_args "$SOCKET" "$account" "$title" "$groups")"

        if launch_session "$title" "$dir" "$account" "$flags" "$pw" "$loc_args" "$env_vars" "$groups" "$mute" "$commands"; then
            launched=$((launched + 1))
        else
            existed=$((existed + 1))
        fi
    done <<<"$picker_output"

    sort_tabs "$SOCKET"

    echo "Launched $launched new session(s), $existed already existed." >&2
}

cmd_launch_all() {
    if [[ ! -f "$CONFIG_FILE" ]]; then
        die "Config file not found: '$CONFIG_FILE'."
    fi

    local lines=""
    if ! lines="$(python3 "$_CC_COMMON_DIR/toml-helper.py" read-sessions "$CONFIG_FILE")"; then
        die "Failed to parse sessions config '$CONFIG_FILE'."
    fi

    if [[ "$_DEFAULT_SORT_SESSIONS" == "alpha" ]]; then
        lines="$(echo -n "$lines" | sort -f)"
    elif [[ "$_DEFAULT_SORT_SESSIONS" == "group" ]]; then
        lines="$(printf '%s' "$lines" | awk -F'\t' 'BEGIN{OFS="\t"} {
            g=$7; sub(/\x1f.*/, "", g); if(g=="") g="zzz";
            print g, $0
        }' | sort -f -t "$(printf '\t')" -k1,1 -k2,2 | cut -f2-)"
    fi

    if [[ -n "${_TARGET_GROUP:-}" ]]; then
        local filtered=""
        while IFS=$'\t' read -r title dir account flags pw env_vars groups mute commands; do
            [[ -z "$title" ]] && continue
            local _grp_matched=false
            local -a _grp_arr=()
            IFS=$'\x1f' read -ra _grp_arr <<< "$(_trim "$groups")"
            local _g
            for _g in "${_grp_arr[@]}"; do
                if [[ "$_g" == "$_TARGET_GROUP" ]]; then
                    _grp_matched=true
                    break
                fi
            done
            if [[ "$_grp_matched" == "true" ]]; then
                [[ -n "$filtered" ]] && filtered+=$'\n'
                filtered+="${title}	${dir}	${account}	${flags}	${pw}	${env_vars}	${groups}	${mute}	${commands}"
            fi
        done <<<"$lines"
        if [[ -z "$filtered" ]]; then
            echo "No sessions found in group '$_TARGET_GROUP'." >&2
            # List available groups from config
            local available_groups=""
            while IFS=$'\t' read -r _t _d _a _f _p _e g _m _c; do
                [[ -z "$_t" ]] && continue
                g="$(_trim "$g")"
                [[ -z "$g" ]] && continue
                # Split by \x1f and add each group
                local -a _gg_arr=()
                IFS=$'\x1f' read -ra _gg_arr <<< "$g"
                local _gg
                for _gg in "${_gg_arr[@]}"; do
                    [[ -z "$_gg" ]] && continue
                    case ",$available_groups," in
                        *",$_gg,"*) ;;
                        *) [[ -n "$available_groups" ]] && available_groups+=", "; available_groups+="$_gg" ;;
                    esac
                done
            done <<<"$lines"
            if [[ -n "$available_groups" ]]; then
                echo "  Available groups: $available_groups" >&2
            fi
            return
        fi
        lines="$filtered"
    fi

    local total=0
    local _dup_titles=""
    while IFS=$'\t' read -r _t _d _a _f _p _e _g _m _c; do
        [[ -z "$_t" ]] && continue
        _t="$(_trim "$_t")"
        total=$((total + 1))
        case "|${_dup_titles}|" in
            *"|${_t}|"*) warn "duplicate session title '$_t' in config; only the first will launch." ;;
            *) _dup_titles="${_dup_titles}|${_t}" ;;
        esac
    done <<<"$lines"
    local current=0
    local skipped=0
    local launched=0
    local existed=0

    while IFS=$'\t' read -r title dir account flags pw env_vars groups mute commands; do
        [[ -z "$title" ]] && continue
        current=$((current + 1))
        title="$(_trim "$title")"
        dir="$(_trim "$dir")"
        account="$(_trim "${account:-work}")"
        flags="$(_trim "${flags:-}")"
        pw="$(_trim "${pw:-}")"
        env_vars="$(_trim "${env_vars:-}")"
        groups="$(_trim "${groups:-}")"
        mute="$(_trim "${mute:-}")"
        commands="$(_trim "${commands:-}")"
        [[ -z "$account" ]] && account="work"

        if ! _account_index "$account" >/dev/null 2>&1; then
            warn "[$current/$total] session '$title' references unknown account '$account', skipping."
            echo "  Available accounts: ${_ACCOUNT_NAMES[*]}" >&2
            skipped=$((skipped + 1))
            continue
        fi

        local loc_args
        loc_args="$(resolve_location_args "$SOCKET" "$account" "$title" "$groups")"

        echo "[$current/$total] Launching $title..." >&2
        if launch_session "$title" "$dir" "$account" "$flags" "$pw" "$loc_args" "$env_vars" "$groups" "$mute" "$commands"; then
            launched=$((launched + 1))
        else
            existed=$((existed + 1))
        fi
    done <<<"$lines"

    sort_tabs "$SOCKET"

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
    local groups=""

    while [[ $# -gt 0 ]]; do
        case "$1" in
        --group)
            groups="${2:-}"
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
        die "--add requires TITLE and DIR arguments." \
            "Usage: cc-launch --add TITLE DIR [ACCOUNT [ENV_VARS]] [--group GROUP]"
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
    loc_args="$(resolve_location_args "$SOCKET" "$account" "$title" "$groups")"

    launch_session "$title" "$dir" "$account" "" "" "$loc_args" "$env_vars" "$groups"
}
