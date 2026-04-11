#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CMD_MAINTENANCE_LIST_LOADED:-}" ]] && return 0
_CC_LAUNCH_CMD_MAINTENANCE_LIST_LOADED=1

cmd_list() {
    discover_all_sockets

    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        err "No active Kitty instances found."
        return 1
    fi

    local accounts_json default_account
    accounts_json="$(build_accounts_json)"
    default_account="$(default_account_name)"

    local table=""
    local idx
    for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
        local inst_socket="${_INSTANCE_SOCKETS[$idx]}"
        local inst_label="${_INSTANCE_LABELS[$idx]}"
        local ls_json
        ls_json="$(kitty @ --to "$inst_socket" ls 2>/dev/null)" || continue

        local inst_rows
        inst_rows="$(echo "$ls_json" | jq -r --argjson accounts "$accounts_json" --arg default_account "$default_account" --arg managed_re "$_MANAGED_RE" --arg strip_re "$_STRIP_RE" --arg icon_bolt "$_ICON_BOLT" --arg icon_stop "$_ICON_STOP" --arg icon_key "$_ICON_KEY" --arg icon_error "$_ICON_ERROR" --arg icon_star "$_ICON_STAR" --arg icon_flag "$_ICON_FLAG" --arg icon_mute "$_ICON_MUTE" --arg inst_label "$inst_label" '
			[.[] | . as $os |
				$os.tabs | to_entries[] |
				.key as $tab_idx | .value as $tab |
				select(
					[$tab.windows[] | .foreground_processes[]?.cmdline[]? | test("claude")] | any
				) |
				select(
					([$tab.windows[] | .user_vars.cc_account // empty | select(. != "")] | length > 0) or
					($tab.title | test($managed_re))
				) |
				{
					index: ($tab_idx + 1),
					tab_id: $tab.id,
					title: $tab.title,
					account: (
						[$tab.windows[] | .user_vars.cc_account // empty | select(. != "")] |
						if length > 0 then .[0]
						else
							(
								[$tab.windows[] | .foreground_processes[]? |
									select(.cmdline | any(test("CLAUDE_CONFIG_DIR"))) |
									.cmdline[] | capture("CLAUDE_CONFIG_DIR=(?<d>.+)") | .d
								] | if length > 0 then
									(.[0] as $config_dir | (first($accounts[] | select(.dir == $config_dir) | .name) // "unknown"))
								else $default_account end
							)
						end
					),
					dir: (
						[$tab.windows[] |
							select([.foreground_processes[]?.cmdline[]? | test("claude")] | any)
						] | if length > 0 then .[0].cwd else ($tab.windows[0].cwd // "unknown") end
					),
					status: (
						if ($os.is_focused and $tab.is_focused)
						then "focused"
						elif ($tab.title | test("^(" + $icon_star + ")?(" + $icon_flag + ")?(" + $icon_mute + ")?" + $icon_bolt))
						then "active"
						elif ($tab.title | test("^(" + $icon_star + ")?(" + $icon_flag + ")?(" + $icon_mute + ")?" + $icon_stop))
						then "stopped"
						elif ($tab.title | test("^(" + $icon_star + ")?(" + $icon_flag + ")?(" + $icon_mute + ")?" + $icon_key))
						then "permission"
						elif ($tab.title | test("^(" + $icon_star + ")?(" + $icon_flag + ")?(" + $icon_mute + ")?" + $icon_error))
						then "error"
						else "idle"
						end
					),
					instance: $inst_label,
					group: ([$tab.windows[] | .user_vars.cc_group // empty | select(. != "")] | if length > 0 then .[0] else "" end)
				}
			] | .[] |
			[(.index | tostring), (.tab_id | tostring), .title, .account, .dir, .status, .instance, .group] |
			@tsv
		')" || continue
        if [[ -n "$inst_rows" ]]; then
            if [[ -n "$table" ]]; then
                table+=$'\n'
            fi
            table+="$inst_rows"
        fi
    done

    if [[ -z "$table" ]]; then
        echo "No Claude sessions found." >&2
        return
    fi

    init_color

    local header_mk=" " header_idx="Index" header_tab_id="TabID" header_title="Title"
    local header_account="Account" header_dir="Directory" header_status="Status"
    local header_inst="Instance" header_group="Group"
    local max_mk=2 w1=${#header_idx} w2=${#header_tab_id} w3=${#header_title} w4=${#header_account} w5=${#header_dir} w6=${#header_status}
    local w7=${#header_inst} w8=${#header_group}
    local has_groups=false

    local -a rows=()
    while IFS=$'\t' read -r row_idx tab_id title account dir status instance group; do
        dir="$(shorten_path "$dir")"
        rows+=("$row_idx"$'\t'"$tab_id"$'\t'"$title"$'\t'"$account"$'\t'"$dir"$'\t'"$status"$'\t'"$instance"$'\t'"$group")
        ((${#row_idx} > w1)) && w1=${#row_idx}
        ((${#tab_id} > w2)) && w2=${#tab_id}
        ((${#title} > w3)) && w3=${#title}
        ((${#account} > w4)) && w4=${#account}
        ((${#dir} > w5)) && w5=${#dir}
        ((${#status} > w6)) && w6=${#status}
        ((${#instance} > w7)) && w7=${#instance}
        if [[ -n "$group" ]]; then
            has_groups=true
            ((${#group} > w8)) && w8=${#group}
        fi
    done <<<"$table"

    if [[ "$has_groups" == "true" ]]; then
        printf "%s%-${max_mk}s%-${w1}s  %-${w2}s  %-${w3}s  %-${w4}s  %-${w8}s  %-${w7}s  %-${w5}s  %-${w6}s%s\n" \
            "$BOLD" "$header_mk" "$header_idx" "$header_tab_id" "$header_title" "$header_account" "$header_group" "$header_inst" "$header_dir" "$header_status" "$RESET"
        printf "%s%s%s\n" "$DIM" \
            "$(repeat_char '─' $((max_mk + w1 + 2 + w2 + 2 + w3 + 2 + w4 + 2 + w8 + 2 + w7 + 2 + w5 + 2 + w6)))" \
            "$RESET"
    else
        printf "%s%-${max_mk}s%-${w1}s  %-${w2}s  %-${w3}s  %-${w4}s  %-${w7}s  %-${w5}s  %-${w6}s%s\n" \
            "$BOLD" "$header_mk" "$header_idx" "$header_tab_id" "$header_title" "$header_account" "$header_inst" "$header_dir" "$header_status" "$RESET"
        printf "%s%s%s\n" "$DIM" \
            "$(repeat_char '─' $((max_mk + w1 + 2 + w2 + 2 + w3 + 2 + w4 + 2 + w7 + 2 + w5 + 2 + w6)))" \
            "$RESET"
    fi

    for row in "${rows[@]}"; do
        IFS=$'\t' read -r row_idx tab_id title account dir status instance group <<<"$row"

        local local_mk=" "
        if [[ "$status" == "focused" ]]; then
            local_mk=">"
        fi

        local local_color="" local_reset=""
        if [[ "$USE_COLOR" == "true" ]]; then
            local_color="$(ansi_fg_for_account "$account")"
            local_reset="$RESET"
        fi

        local local_status="$status"
        if [[ "$USE_COLOR" == "true" ]]; then
            case "$status" in
            focused) local_status="${BOLD}${status}${RESET}${local_color}" ;;
            idle) local_status="${DIM}${status}${RESET}${local_color}" ;;
            esac
        fi

        if [[ "$has_groups" == "true" ]]; then
            printf "%s%-$((max_mk + $(extra_bytes "$local_mk")))s%-$((w1 + $(extra_bytes "$row_idx")))s  %-$((w2 + $(extra_bytes "$tab_id")))s  %-$((w3 + $(extra_bytes "$title")))s  %-$((w4 + $(extra_bytes "$account")))s  %-$((w8 + $(extra_bytes "$group")))s  %-$((w7 + $(extra_bytes "$instance")))s  %-$((w5 + $(extra_bytes "$dir")))s  %s%s\n" \
                "$local_color" "$local_mk" "$row_idx" "$tab_id" "$title" "$account" "$group" "$instance" "$dir" "$local_status" "$local_reset"
        else
            printf "%s%-$((max_mk + $(extra_bytes "$local_mk")))s%-$((w1 + $(extra_bytes "$row_idx")))s  %-$((w2 + $(extra_bytes "$tab_id")))s  %-$((w3 + $(extra_bytes "$title")))s  %-$((w4 + $(extra_bytes "$account")))s  %-$((w7 + $(extra_bytes "$instance")))s  %-$((w5 + $(extra_bytes "$dir")))s  %s%s\n" \
                "$local_color" "$local_mk" "$row_idx" "$tab_id" "$title" "$account" "$instance" "$dir" "$local_status" "$local_reset"
        fi
    done
}
