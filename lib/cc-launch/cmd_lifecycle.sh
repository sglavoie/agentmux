#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CMD_LIFECYCLE_LOADED:-}" ]] && return 0
_CC_LAUNCH_CMD_LIFECYCLE_LOADED=1

# Poll constants for waiting on tab closure before relaunch.
# 5 polls at 100ms intervals = ~500ms max wait. This is enough for Kitty to
# process a close-tab command; if the tab is still present after 500ms, we
# warn and proceed anyway (the launch will likely succeed regardless).
_CLOSE_POLL_MAX=5
_CLOSE_POLL_DELAY=0.1  # seconds

# Wait for a tab to disappear from Kitty after a close-tab command.
# Usage: _wait_for_tab_close SOCKET TAB_ID TITLE ACTION
#   SOCKET — kitty socket to poll
#   TAB_ID — numeric tab id to wait for
#   TITLE  — tab title (for warning messages)
#   ACTION — verb for the warning message (e.g. "restart", "move")
_wait_for_tab_close() {
    local socket="$1" tab_id="$2" title="$3" action="$4"
    local _poll
    for ((_poll = 0; _poll < _CLOSE_POLL_MAX; _poll++)); do
        if ! kitty @ --to "$socket" ls 2>/dev/null | jq -e --argjson tid "$tab_id" '[.[].tabs[] | select(.id == $tid)] | length > 0' >/dev/null 2>&1; then
            return 0
        fi
        sleep "$_CLOSE_POLL_DELAY"
    done
    warn "tab '$title' may not have closed (timed out after ${_CLOSE_POLL_MAX} polls). Proceeding with $action."
}

cmd_close_group() {
    if [[ -z "$_TARGET_GROUP" ]]; then
        die "--group requires a NAME argument."
    fi

    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        die "No active Kitty instances found."
    fi

    local matches=""
    local idx
    for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
        local inst_socket="${_INSTANCE_SOCKETS[$idx]}"
        local inst_label="${_INSTANCE_LABELS[$idx]}"
        local ls_json
        ls_json="$(kitty @ --to "$inst_socket" ls 2>/dev/null)" || continue

        local inst_matches
        inst_matches="$(echo "$ls_json" | jq -r --arg group "$_TARGET_GROUP" '
            [.[] | .tabs[] |
                select([.windows[] | .user_vars.cc_groups // empty | select(. != "")
                        | split("\u001f")[] | select(. == $group)] | length > 0) |
                { title: .title, id: .id }
            ] | .[] | [.title, (.id | tostring)] | @tsv')" || continue

        if [[ -n "$inst_matches" ]]; then
            while IFS=$'\t' read -r title tab_id; do
                [[ -z "$title" ]] && continue
                [[ -n "$matches" ]] && matches+=$'\n'
                matches+="${title}"$'\t'"${tab_id}"$'\t'"${inst_socket}"$'\t'"${inst_label}"
            done <<<"$inst_matches"
        fi
    done

    if [[ -z "$matches" ]]; then
        echo "No running sessions found in group '$_TARGET_GROUP'." >&2
        return
    fi

    echo "Sessions in group '$_TARGET_GROUP':" >&2
    echo "" >&2
    local multi_instance=false
    [[ ${#_INSTANCE_SOCKETS[@]} -gt 1 ]] && multi_instance=true
    while IFS=$'\t' read -r title tab_id sock inst_label; do
        if [[ "$multi_instance" == "true" ]]; then
            echo "  $title (tab id: $tab_id, instance: $inst_label)" >&2
        else
            echo "  $title (tab id: $tab_id)" >&2
        fi
    done <<<"$matches"
    echo "" >&2

    if [[ "$_DRY_RUN" == "true" ]]; then
        echo "Dry run: no sessions were closed." >&2
        return
    fi

    if confirm "Close all sessions in group '$_TARGET_GROUP'?" "Yes, close them"; then
        local closed=0
        while IFS=$'\t' read -r title tab_id sock inst_label; do
            if kitty @ --to "$sock" close-tab --match "id:$tab_id"; then
                echo "Closed: $title" >&2
                closed=$((closed + 1))
            else
                warn "failed to close '$title' (tab id: $tab_id)."
            fi
        done <<<"$matches"
        echo "Closed $closed session(s) in group '$_TARGET_GROUP'." >&2
    else
        echo "No sessions closed." >&2
    fi
}

cmd_close() {
    if [[ -z "$_TARGET_INSTANCE_LABEL" || -z "$_TARGET_TAB_ID" ]]; then
        die "--close requires --instance LABEL and --tab-id ID." \
            "Usage: cc-launch --close --instance LABEL --tab-id ID"
    fi

    local source_socket
    source_socket="$(resolve_instance_socket_or_die "$_TARGET_INSTANCE_LABEL")"

    local accounts_json default_account
    accounts_json="$(build_accounts_json)"
    default_account="$(default_account_name)"

    local tab_info title
    tab_info="$(tab_metadata_by_id "$source_socket" "$_TARGET_TAB_ID" "$accounts_json" "$default_account")"
    if [[ -z "$tab_info" ]]; then
        die "No tab found with id '$_TARGET_TAB_ID' in instance '$_TARGET_INSTANCE_LABEL'."
    fi
    IFS=$'\t' read -r title _ <<<"$tab_info"

    if [[ "$_DRY_RUN" == "true" ]]; then
        echo "Would close: $title (instance=$_TARGET_INSTANCE_LABEL, tab_id=$_TARGET_TAB_ID)" >&2
        return
    fi

    if ! kitty @ --to "$source_socket" close-tab --match "id:$_TARGET_TAB_ID"; then
        die "failed to close tab '$title' (tab_id=$_TARGET_TAB_ID)."
    fi
    echo "Closed tab '$title' (instance=$_TARGET_INSTANCE_LABEL, tab_id=$_TARGET_TAB_ID)." >&2
}

cmd_restart() {
    if [[ -z "$_TARGET_INSTANCE_LABEL" || -z "$_TARGET_TAB_ID" ]]; then
        die "--restart requires --instance LABEL and --tab-id ID." \
            "Usage: cc-launch --restart --instance LABEL --tab-id ID"
    fi

    local source_socket
    source_socket="$(resolve_instance_socket_or_die "$_TARGET_INSTANCE_LABEL")"

    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        die "No active Kitty instances found."
    fi

    local accounts_json default_account
    accounts_json="$(build_accounts_json)"
    default_account="$(default_account_name)"

    local tab_info
    tab_info="$(tab_metadata_by_id "$source_socket" "$_TARGET_TAB_ID" "$accounts_json" "$default_account")"
    if [[ -z "$tab_info" ]]; then
        die "No tab found with id '$_TARGET_TAB_ID' in instance '$_TARGET_INSTANCE_LABEL'."
    fi

    local title account dir live_flags live_pw live_env live_groups live_mute live_commands
    IFS=$'\t' read -r title account dir live_flags live_pw live_env live_groups live_mute live_commands <<<"$tab_info"
    if [[ -z "$title" || -z "$account" || -z "$dir" ]]; then
        die "incomplete tab metadata for tab '$_TARGET_TAB_ID'."
    fi
    SOCKET="$source_socket"

    if [[ "$_DRY_RUN" == "true" ]]; then
        echo "Would restart: $title (instance=$_TARGET_INSTANCE_LABEL, tab_id=$_TARGET_TAB_ID, dir=$dir, account=$account)" >&2
        return
    fi

    if ! kitty @ --to "$SOCKET" close-tab --match "id:$_TARGET_TAB_ID"; then
        die "failed to close tab '$title' for restart."
    fi
    echo "Closed tab '$title', relaunching..." >&2
    _wait_for_tab_close "$SOCKET" "$_TARGET_TAB_ID" "$title" "restart"

    local loc_args
    loc_args="$(resolve_location_args "$SOCKET" "$account" "$title" "$live_groups")"

    local fallback
    fallback="$(lookup_session_overrides "$title" "$live_flags" "$live_pw" "$live_env" "$live_commands")"
    local cfg_flags cfg_pw cfg_env cfg_commands
    IFS=$'\t' read -r cfg_flags cfg_pw cfg_env cfg_commands <<<"$fallback"

    if launch_session "$title" "$dir" "$account" "$cfg_flags" "$cfg_pw" "$loc_args" "$cfg_env" "$live_groups" "$live_mute" "$cfg_commands"; then
        sort_tabs "$SOCKET"
    else
        die "Failed to relaunch '$title' after closing for restart. Check the error above."
    fi
}

cmd_clone() {
    if [[ -z "$_TARGET_INSTANCE_LABEL" || -z "$_TARGET_TAB_ID" ]]; then
        die "--clone requires --instance LABEL and --tab-id ID." \
            "Usage: cc-launch --clone --instance LABEL --tab-id ID"
    fi

    local source_socket
    source_socket="$(resolve_instance_socket_or_die "$_TARGET_INSTANCE_LABEL")"

    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        die "No active Kitty instances found."
    fi

    local accounts_json default_account
    accounts_json="$(build_accounts_json)"
    default_account="$(default_account_name)"

    local tab_info
    tab_info="$(tab_metadata_by_id "$source_socket" "$_TARGET_TAB_ID" "$accounts_json" "$default_account")"
    if [[ -z "$tab_info" ]]; then
        die "No tab found with id '$_TARGET_TAB_ID' in instance '$_TARGET_INSTANCE_LABEL'."
    fi

    local title account dir live_flags live_pw live_env live_groups live_mute live_commands
    IFS=$'\t' read -r title account dir live_flags live_pw live_env live_groups live_mute live_commands <<<"$tab_info"
    if [[ -z "$title" || -z "$account" || -z "$dir" ]]; then
        die "incomplete tab metadata for tab '$_TARGET_TAB_ID'."
    fi
    SOCKET="$source_socket"

    if [[ "$_DRY_RUN" == "true" ]]; then
        echo "Would clone: $title (instance=$_TARGET_INSTANCE_LABEL, tab_id=$_TARGET_TAB_ID, dir=$dir, account=$account)" >&2
        return
    fi

    local base_title clone_title number
    local max_clones=100  # Safety cap to prevent runaway loop; 100 clones per session is far beyond practical use
    base_title="$(echo "$title" | sed 's/-[0-9]*$//')"
    number=2
    while [[ $number -le $max_clones ]]; do
        clone_title="${base_title}-${number}"
        if ! tab_exists "$clone_title"; then
            break
        fi
        number=$((number + 1))
    done
    if [[ $number -gt $max_clones ]]; then
        err "too many clones of '$base_title' (max $max_clones)."
        return 1
    fi

    local loc_args
    loc_args="$(resolve_location_args "$SOCKET" "$account" "$clone_title" "$live_groups")"

    local fallback
    fallback="$(lookup_session_overrides "$title" "$live_flags" "$live_pw" "$live_env" "$live_commands")"
    local cfg_flags cfg_pw cfg_env cfg_commands
    IFS=$'\t' read -r cfg_flags cfg_pw cfg_env cfg_commands <<<"$fallback"

    if launch_session "$clone_title" "$dir" "$account" "$cfg_flags" "$cfg_pw" "$loc_args" "$cfg_env" "$live_groups" "$live_mute" "$cfg_commands"; then
        sort_tabs "$SOCKET"
        if [[ -f "$CONFIG_FILE" ]]; then
            _append_session_to_config "$CONFIG_FILE" "$clone_title" "$dir" "$account" "$cfg_flags" "$cfg_pw" "$cfg_env" "$live_groups" "$live_mute" "$cfg_commands"
        fi
        echo "Cloned '$title' as '$clone_title'." >&2
    fi
}

cmd_move() {
    if [[ -z "$_TARGET_INSTANCE_LABEL" || -z "$_TARGET_TAB_ID" ]]; then
        die "--move requires --instance LABEL and --tab-id ID for the source tab." \
            "Usage: cc-launch --move --instance SOURCE --tab-id ID --target-instance TARGET"
    fi

    if [[ -z "$_MOVE_TARGET_INSTANCE" ]]; then
        die "--move requires --target-instance TARGET to specify the destination." \
            "Usage: cc-launch --move --instance SOURCE --tab-id ID --target-instance TARGET"
    fi

    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        die "No active Kitty instances found."
    fi

    local source_socket
    source_socket="$(resolve_instance_socket_or_die "$_TARGET_INSTANCE_LABEL")"

    local target_socket=""
    local idx
    for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
        if [[ "${_INSTANCE_LABELS[$idx]}" == "$_MOVE_TARGET_INSTANCE" ]]; then
            target_socket="${_INSTANCE_SOCKETS[$idx]}"
            break
        fi
    done

    if [[ -z "$target_socket" ]]; then
        err "No active Kitty instance found with label '$_MOVE_TARGET_INSTANCE'."
        echo "Available instances:" >&2
        print_available_instances
        exit 1
    fi

    local accounts_json default_account
    accounts_json="$(build_accounts_json)"
    default_account="$(default_account_name)"

    local tab_info
    tab_info="$(tab_metadata_by_id "$source_socket" "$_TARGET_TAB_ID" "$accounts_json" "$default_account")"
    if [[ -z "$tab_info" ]]; then
        die "No tab found with id '$_TARGET_TAB_ID' in instance '$_TARGET_INSTANCE_LABEL'."
    fi

    if [[ "$source_socket" == "$target_socket" ]]; then
        die "Session is already in the target instance '$_MOVE_TARGET_INSTANCE'."
    fi

    local title account dir live_flags live_pw live_env live_groups live_mute live_commands
    IFS=$'\t' read -r title account dir live_flags live_pw live_env live_groups live_mute live_commands <<<"$tab_info"
    if [[ -z "$title" || -z "$account" || -z "$dir" ]]; then
        die "incomplete tab metadata for tab '$_TARGET_TAB_ID'."
    fi

    if [[ "$_DRY_RUN" == "true" ]]; then
        echo "Would move: $title (from=$_TARGET_INSTANCE_LABEL, to=$_MOVE_TARGET_INSTANCE, tab_id=$_TARGET_TAB_ID)" >&2
        return
    fi

    if ! kitty @ --to "$source_socket" close-tab --match "id:$_TARGET_TAB_ID"; then
        die "failed to close tab '$title' for move."
    fi
    echo "Closed tab '$title' on source instance, relaunching on $_MOVE_TARGET_INSTANCE..." >&2
    _wait_for_tab_close "$source_socket" "$_TARGET_TAB_ID" "$title" "move"

    SOCKET="$target_socket"
    local loc_args
    loc_args="$(resolve_location_args "$SOCKET" "$account" "$title" "$live_groups")"

    local fallback
    fallback="$(lookup_session_overrides "$title" "$live_flags" "$live_pw" "$live_env" "$live_commands")"
    local cfg_flags cfg_pw cfg_env cfg_commands
    IFS=$'\t' read -r cfg_flags cfg_pw cfg_env cfg_commands <<<"$fallback"

    if launch_session "$title" "$dir" "$account" "$cfg_flags" "$cfg_pw" "$loc_args" "$cfg_env" "$live_groups" "$live_mute" "$cfg_commands"; then
        sort_tabs "$SOCKET"
        echo "Session '$title' moved to $_MOVE_TARGET_INSTANCE." >&2
    else
        die "Failed to relaunch '$title' on $_MOVE_TARGET_INSTANCE after closing. Check the error above."
    fi
}
