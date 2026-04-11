#!/usr/bin/env bash

# shellcheck disable=SC2034,SC2154

[[ -n "${_CC_HOOK_INDICATOR_STATE_LOADED:-}" ]] && return 0
_CC_HOOK_INDICATOR_STATE_LOADED=1

cchi_parse_current_title() {
    local parsed_remaining
    {
        read -r has_star
        read -r has_flag
        read -r has_mute
        read -r has_dashboard
        read -r parsed_remaining
    } < <(parse_icon_prefix "$current_title")

    remaining="$(strip_icons "$parsed_remaining")"
    _MUTED=false
    [[ "$has_mute" == true ]] && _MUTED=true
}

cchi_state_file() {
    printf '%s/ccm-state-%s' "$_CCM_TMPDIR" "$tab_id"
}

cchi_auto_undim() {
    local undim_socket="${KITTY_LISTEN_ON:-}"
    [[ -n "$undim_socket" ]] || return 0

    local undim_ls dim_type
    undim_ls="$(kitty @ --to "$undim_socket" ls 2>/dev/null)" || undim_ls=""
    [[ -n "$undim_ls" ]] || return 0

    dim_type=""
    if dim_type="$(is_dimmed "$tab_id" "$undim_ls")" 2>/dev/null; then
        if [[ "$dim_type" == "auto" ]]; then
            apply_undim "$undim_socket" "$tab_id" "$KITTY_WINDOW_ID" "$undim_ls" || true
        fi
    fi
}

cchi_resolve_state() {
    case "$ACTION" in
    active)
        if [[ "$EVENT_KIND" == "subagent_stop" || "$EVENT_KIND" == "task_complete" ]]; then
            state="$(detect_state "$current_title")"
        else
            state="bolt"
            cchi_auto_undim
        fi
        ;;
    stop)
        if cchi_is_post_permission; then
            ccm_debug "hook: post-permission stop suppressed, clearing guard"
            state=""
            rm -f "$_state_file"
        else
            state="stop"
        fi
        ;;
    permission) state="key" ;;
    idle) state="" ;;
    failure) state="error" ;;
    clear) state="" ;;
    *)
        echo "Error: unknown action '$ACTION' (expected 'active', 'stop', 'permission', 'idle', 'failure', or 'clear')" >&2
        return 1
        ;;
    esac
}

cchi_guard_action() {
    awk '{print $1}' "$_state_file" 2>/dev/null || true
}

cchi_guard_ts() {
    awk '{print $2}' "$_state_file" 2>/dev/null || true
}

cchi_allow_active_transition() {
    [[ -f "$_state_file" ]] || return 0

    if [[ "$EVENT_KIND" == "prompt" ]]; then
        local guard_ts now
        guard_ts="$(cchi_guard_ts)"
        if [[ -n "$guard_ts" ]]; then
            now=$(date +%s)
            if ((now - guard_ts < 2)); then
                return 1
            fi
        fi
        rm -f "$_state_file"
        return 0
    fi

    local guard_action
    guard_action="$(cchi_guard_action)"
    if [[ "$guard_action" == "permission" ]]; then
        rm -f "$_state_file"
        return 0
    fi

    return 1
}

cchi_apply_guard_before_title_update() {
    case "$ACTION" in
    stop | failure | permission)
        if [[ "$ACTION" != "stop" ]] || ! cchi_is_post_permission; then
            local tmp_state="${_state_file}.tmp.$$"
            echo "$ACTION $(date +%s)" >"$tmp_state" && mv "$tmp_state" "$_state_file"
        fi
        ;;
    active)
        cchi_allow_active_transition || return 1
        ;;
    idle | clear)
        rm -f "$_state_file"
        ;;
    esac

    return 0
}

cchi_apply_active_toc_tou_guard() {
    [[ "$ACTION" == "active" ]] || return 0
    [[ -f "$_state_file" ]] || return 0

    ccm_debug "hook: TOCTOU guard detected action=$ACTION event=$EVENT_KIND"
    cchi_allow_active_transition
}

cchi_update_title_if_needed() {
    if [[ "$new_title" != "$current_title" ]]; then
        kitten @ ${TO_ARGS[@]+"${TO_ARGS[@]}"} set-tab-title --match "id:$tab_id" "$new_title" 2>/dev/null || true
    fi
}

cchi_update_markers() {
    if [[ "$ACTION" == "failure" ]]; then
        kitten @ ${TO_ARGS[@]+"${TO_ARGS[@]}"} create-marker --match "id:$KITTY_WINDOW_ID" iregex 1 '\bError\b' 2 '\bFailed\b' 3 '\bWarning\b' 3 '\bDEPRECATED\b' 2>/dev/null || true
    elif [[ "$ACTION" == "active" || "$ACTION" == "clear" ]]; then
        kitten @ ${TO_ARGS[@]+"${TO_ARGS[@]}"} remove-marker --match "id:$KITTY_WINDOW_ID" 2>/dev/null || true
    fi
}
