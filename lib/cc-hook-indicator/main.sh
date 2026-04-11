#!/usr/bin/env bash

# shellcheck disable=SC2154

[[ -n "${_CC_HOOK_INDICATOR_MAIN_LOADED:-}" ]] && return 0
_CC_HOOK_INDICATOR_MAIN_LOADED=1

_CC_HOOK_INDICATOR_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=../cc-title-icons.sh
source "$_CC_HOOK_INDICATOR_DIR/../cc-title-icons.sh"
# shellcheck source=activity.sh
source "$_CC_HOOK_INDICATOR_DIR/activity.sh"
# shellcheck source=notify.sh
source "$_CC_HOOK_INDICATOR_DIR/notify.sh"
# shellcheck source=state.sh
source "$_CC_HOOK_INDICATOR_DIR/state.sh"

cchi_usage() {
    echo "Usage: cc-hook-indicator active|stop|permission|idle|failure|clear [EVENT_KIND] [WINDOW_ID]" >&2
}

cchi_parse_args() {
    if [[ $# -lt 1 ]]; then
        cchi_usage
        return 1
    fi

    ACTION="$1"
    EVENT_KIND=""

    if [[ $# -ge 3 ]]; then
        EVENT_KIND="$2"
        KITTY_WINDOW_ID="$3"
    elif [[ $# -ge 2 ]]; then
        if [[ "$2" =~ ^[0-9]+$ ]]; then
            KITTY_WINDOW_ID="$2"
        else
            EVENT_KIND="$2"
        fi
    fi

    if [[ -z "${KITTY_WINDOW_ID:-}" ]]; then
        echo "Error: KITTY_WINDOW_ID is not set" >&2
        return 1
    fi
}

cchi_init_context() {
    load_defaults

    TO_ARGS=()
    if [[ -n "${KITTY_LISTEN_ON:-}" ]]; then
        TO_ARGS=("--to" "$KITTY_LISTEN_ON")
    fi

    local info
    info="$(get_tab_info "$KITTY_WINDOW_ID" ${TO_ARGS[@]+"${TO_ARGS[@]}"})"
    tab_id="$(echo "$info" | head -n1)"
    current_title="$(echo "$info" | tail -n1)"

    if [[ -z "$tab_id" || "$tab_id" == "null" ]]; then
        return 1
    fi

    _PIPE_MODE=false
    cchi_is_pipe_mode && _PIPE_MODE=true
    ccm_debug "hook: action=$ACTION event=$EVENT_KIND tab=$tab_id pipe=$_PIPE_MODE"

    _prev_last_ts=0
    _prev_last_type=""
    cchi_update_activity

    cchi_parse_current_title
    _state_file="$(cchi_state_file)"
    return 0
}

cc_hook_indicator_main() {
    cchi_parse_args "$@" || return 1
    cchi_init_context || return 0
    cchi_resolve_state || return 1
    ccm_debug "hook: state=$state title='$current_title'"

    cchi_apply_guard_before_title_update || return 0

    new_title="$(build_title "$remaining" "$has_star" "$state" "$has_flag" "$has_mute" "$has_dashboard")"
    ccm_debug "hook: new_title='$new_title' (was '$current_title')"

    cchi_send_action_notification
    cchi_apply_active_toc_tou_guard || return 0
    cchi_update_title_if_needed
    cchi_update_markers
    return 0
}
