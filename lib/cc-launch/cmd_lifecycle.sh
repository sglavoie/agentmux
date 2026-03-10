#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CMD_LIFECYCLE_LOADED:-}" ]] && return 0
_CC_LAUNCH_CMD_LIFECYCLE_LOADED=1

cmd_close_group() {
    if [[ -z "$_TARGET_GROUP" ]]; then
        echo "Error: --group requires a NAME argument." >&2
        exit 1
    fi

    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        echo "Error: No active Kitty instances found." >&2
        exit 1
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
                select([.windows[] | .user_vars.cc_group // empty
                        | select(. == $group)] | length > 0) |
                { title: .title, id: .id }
            ] | .[] | [.title, (.id | tostring)] | @tsv')"

        if [[ -n "$inst_matches" ]]; then
            while IFS=$'\t' read -r title tab_id; do
                [[ -z "$title" ]] && continue
                [[ -n "$matches" ]] && matches+=$'\n'
                matches+="${title}"$'\t'"${tab_id}"$'\t'"${inst_socket}"$'\t'"${inst_label}"
            done <<<"$inst_matches"
        fi
    done

    if [[ -z "$matches" ]]; then
        echo "No running sessions found in group '$_TARGET_GROUP'."
        return
    fi

    echo "Sessions in group '$_TARGET_GROUP':"
    echo ""
    local multi_instance=false
    [[ ${#_INSTANCE_SOCKETS[@]} -gt 1 ]] && multi_instance=true
    while IFS=$'\t' read -r title tab_id sock inst_label; do
        if [[ "$multi_instance" == "true" ]]; then
            echo "  $title (tab id: $tab_id, instance: $inst_label)"
        else
            echo "  $title (tab id: $tab_id)"
        fi
    done <<<"$matches"
    echo ""

    local confirmed=false
    if [[ "$HAS_GUM" == "true" ]]; then
        if gum confirm "Close all sessions in group '$_TARGET_GROUP'?" \
            --affirmative "Yes, close them" --negative "Cancel"; then
            confirmed=true
        fi
    else
        read -rp "Close all sessions in group '$_TARGET_GROUP'? [y/N] " answer
        if [[ "$answer" =~ ^[Yy] ]]; then
            confirmed=true
        fi
    fi

    if [[ "$confirmed" == "true" ]]; then
        local closed=0
        while IFS=$'\t' read -r title tab_id sock inst_label; do
            kitty @ --to "$sock" close-tab --match "id:$tab_id"
            echo "Closed: $title"
            closed=$((closed + 1))
        done <<<"$matches"
        echo "Closed $closed session(s) in group '$_TARGET_GROUP'."
    else
        echo "No sessions closed."
    fi
}

cmd_close() {
    if [[ -z "$_TARGET_INSTANCE_LABEL" || -z "$_TARGET_TAB_ID" ]]; then
        echo "Error: --close requires --instance LABEL and --tab-id ID." >&2
        echo "Usage: cc-launch --close --instance LABEL --tab-id ID" >&2
        exit 1
    fi

    local source_socket
    source_socket="$(resolve_instance_socket "$_TARGET_INSTANCE_LABEL")" || {
        echo "Error: No active Kitty instance found with label '$_TARGET_INSTANCE_LABEL'." >&2
        echo "Available instances:" >&2
        print_available_instances
        exit 1
    }

    local accounts_json default_account
    accounts_json="$(build_accounts_json)"
    default_account="$(default_account_name)"

    local tab_info title
    tab_info="$(tab_metadata_by_id "$source_socket" "$_TARGET_TAB_ID" "$accounts_json" "$default_account")"
    if [[ -z "$tab_info" ]]; then
        echo "Error: No tab found with id '$_TARGET_TAB_ID' in instance '$_TARGET_INSTANCE_LABEL'." >&2
        exit 1
    fi
    IFS=$'\t' read -r title _ <<<"$tab_info"

    kitty @ --to "$source_socket" close-tab --match "id:$_TARGET_TAB_ID"
    echo "Closed tab '$title' (instance=$_TARGET_INSTANCE_LABEL, tab_id=$_TARGET_TAB_ID)."
}

cmd_restart() {
    if [[ -z "$_TARGET_INSTANCE_LABEL" || -z "$_TARGET_TAB_ID" ]]; then
        echo "Error: --restart requires --instance LABEL and --tab-id ID." >&2
        echo "Usage: cc-launch --restart --instance LABEL --tab-id ID" >&2
        exit 1
    fi

    local source_socket
    source_socket="$(resolve_instance_socket "$_TARGET_INSTANCE_LABEL")" || {
        echo "Error: No active Kitty instance found with label '$_TARGET_INSTANCE_LABEL'." >&2
        echo "Available instances:" >&2
        print_available_instances
        exit 1
    }

    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        echo "Error: No active Kitty instances found." >&2
        exit 1
    fi

    local accounts_json default_account
    accounts_json="$(build_accounts_json)"
    default_account="$(default_account_name)"

    local tab_info
    tab_info="$(tab_metadata_by_id "$source_socket" "$_TARGET_TAB_ID" "$accounts_json" "$default_account")"
    if [[ -z "$tab_info" ]]; then
        echo "Error: No tab found with id '$_TARGET_TAB_ID' in instance '$_TARGET_INSTANCE_LABEL'." >&2
        exit 1
    fi

    local title account dir live_flags live_pw live_env live_group
    IFS=$'\t' read -r title account dir live_flags live_pw live_env live_group <<<"$tab_info"
    SOCKET="$source_socket"

    kitty @ --to "$SOCKET" close-tab --match "id:$_TARGET_TAB_ID"
    echo "Closed tab '$title', relaunching..."
    sleep 0.3 # Wait for Kitty to fully close the tab before relaunching

    local loc_args
    loc_args="$(resolve_location_args "$SOCKET" "$account" "$title")"

    local fallback
    fallback="$(lookup_session_overrides "$title" "$live_flags" "$live_pw" "$live_env")"
    local cfg_flags cfg_pw cfg_env
    IFS=$'\t' read -r cfg_flags cfg_pw cfg_env <<<"$fallback"

    launch_session "$title" "$dir" "$account" "$cfg_flags" "$cfg_pw" "$loc_args" "$cfg_env" "$live_group"
    sort_tabs "$SOCKET"
}

cmd_clone() {
    if [[ -z "$_TARGET_INSTANCE_LABEL" || -z "$_TARGET_TAB_ID" ]]; then
        echo "Error: --clone requires --instance LABEL and --tab-id ID." >&2
        echo "Usage: cc-launch --clone --instance LABEL --tab-id ID" >&2
        exit 1
    fi

    local source_socket
    source_socket="$(resolve_instance_socket "$_TARGET_INSTANCE_LABEL")" || {
        echo "Error: No active Kitty instance found with label '$_TARGET_INSTANCE_LABEL'." >&2
        echo "Available instances:" >&2
        print_available_instances
        exit 1
    }

    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        echo "Error: No active Kitty instances found." >&2
        exit 1
    fi

    local accounts_json default_account
    accounts_json="$(build_accounts_json)"
    default_account="$(default_account_name)"

    local tab_info
    tab_info="$(tab_metadata_by_id "$source_socket" "$_TARGET_TAB_ID" "$accounts_json" "$default_account")"
    if [[ -z "$tab_info" ]]; then
        echo "Error: No tab found with id '$_TARGET_TAB_ID' in instance '$_TARGET_INSTANCE_LABEL'." >&2
        exit 1
    fi

    local title account dir live_flags live_pw live_env live_group
    IFS=$'\t' read -r title account dir live_flags live_pw live_env live_group <<<"$tab_info"
    SOCKET="$source_socket"

    local base_title clone_title number
    local max_clones=100
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
        echo "Error: too many clones of '$base_title' (max $max_clones)." >&2
        return 1
    fi

    local loc_args
    loc_args="$(resolve_location_args "$SOCKET" "$account" "$clone_title")"

    local fallback
    fallback="$(lookup_session_overrides "$title" "$live_flags" "$live_pw" "$live_env")"
    local cfg_flags cfg_pw cfg_env
    IFS=$'\t' read -r cfg_flags cfg_pw cfg_env <<<"$fallback"

    if launch_session "$clone_title" "$dir" "$account" "$cfg_flags" "$cfg_pw" "$loc_args" "$cfg_env" "$live_group"; then
        sort_tabs "$SOCKET"
        if [[ -f "$CONFIG_FILE" ]]; then
            local conf_line="$clone_title | $dir | $account"
            if [[ -n "$cfg_flags" ]]; then
                conf_line="$conf_line | $cfg_flags"
            fi
            if [[ -n "$cfg_pw" || -n "$cfg_env" ]]; then
                if [[ -z "$cfg_flags" ]]; then
                    conf_line="$conf_line | "
                fi
                conf_line="$conf_line | $cfg_pw"
            fi
            if [[ -n "$cfg_env" ]]; then
                if [[ -z "$cfg_pw" && -z "$cfg_flags" ]]; then
                    conf_line="$conf_line | "
                fi
                conf_line="$conf_line | $cfg_env"
            fi
            {
                cat "$CONFIG_FILE"
                echo "$conf_line"
            } > "${CONFIG_FILE}.tmp" && mv "${CONFIG_FILE}.tmp" "$CONFIG_FILE"
        fi
        echo "Cloned '$title' as '$clone_title'."
    fi
}

cmd_move() {
    if [[ -z "$_TARGET_INSTANCE_LABEL" || -z "$_TARGET_TAB_ID" ]]; then
        echo "Error: --move requires --instance LABEL and --tab-id ID for the source tab." >&2
        echo "Usage: cc-launch --move --instance SOURCE --tab-id ID --target-instance TARGET" >&2
        exit 1
    fi

    if [[ -z "$_MOVE_TARGET_INSTANCE" ]]; then
        echo "Error: --move requires --target-instance TARGET to specify the destination." >&2
        echo "Usage: cc-launch --move --instance SOURCE --tab-id ID --target-instance TARGET" >&2
        exit 1
    fi

    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        echo "Error: No active Kitty instances found." >&2
        exit 1
    fi

    local source_socket
    source_socket="$(resolve_instance_socket "$_TARGET_INSTANCE_LABEL")" || {
        echo "Error: No active Kitty instance found with label '$_TARGET_INSTANCE_LABEL'." >&2
        echo "Available instances:" >&2
        print_available_instances
        exit 1
    }

    local target_socket=""
    local idx
    for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
        if [[ "${_INSTANCE_LABELS[$idx]}" == "$_MOVE_TARGET_INSTANCE" ]]; then
            target_socket="${_INSTANCE_SOCKETS[$idx]}"
            break
        fi
    done

    if [[ -z "$target_socket" ]]; then
        echo "Error: No active Kitty instance found with label '$_MOVE_TARGET_INSTANCE'." >&2
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
        echo "Error: No tab found with id '$_TARGET_TAB_ID' in instance '$_TARGET_INSTANCE_LABEL'." >&2
        exit 1
    fi

    if [[ "$source_socket" == "$target_socket" ]]; then
        echo "Error: Session is already in the target instance '$_MOVE_TARGET_INSTANCE'." >&2
        exit 1
    fi

    local title account dir live_flags live_pw live_env live_group
    IFS=$'\t' read -r title account dir live_flags live_pw live_env live_group <<<"$tab_info"

    kitty @ --to "$source_socket" close-tab --match "id:$_TARGET_TAB_ID"
    echo "Closed tab '$title' on source instance, relaunching on $_MOVE_TARGET_INSTANCE..."
    sleep 0.3 # Wait for Kitty to fully close the tab before relaunching on target

    SOCKET="$target_socket"
    local loc_args
    loc_args="$(resolve_location_args "$SOCKET" "$account" "$title")"

    local fallback
    fallback="$(lookup_session_overrides "$title" "$live_flags" "$live_pw" "$live_env")"
    local cfg_flags cfg_pw cfg_env
    IFS=$'\t' read -r cfg_flags cfg_pw cfg_env <<<"$fallback"

    launch_session "$title" "$dir" "$account" "$cfg_flags" "$cfg_pw" "$loc_args" "$cfg_env" "$live_group"
    sort_tabs "$SOCKET"
    echo "Session '$title' moved to $_MOVE_TARGET_INSTANCE."
}
