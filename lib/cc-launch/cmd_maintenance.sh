#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CMD_MAINTENANCE_LOADED:-}" ]] && return 0
_CC_LAUNCH_CMD_MAINTENANCE_LOADED=1

cmd_list() {
    discover_all_sockets

    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        echo "Error: No active Kitty instances found." >&2
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
        inst_rows="$(echo "$ls_json" | jq -r --argjson accounts "$accounts_json" --arg default_account "$default_account" --arg managed_re "$_MANAGED_RE" --arg strip_re "$_STRIP_RE" --arg icon_bolt "$_ICON_BOLT" --arg icon_stop "$_ICON_STOP" --arg icon_key "$_ICON_KEY" --arg icon_error "$_ICON_ERROR" --arg icon_star "$_ICON_STAR" --arg icon_flag "$_ICON_FLAG" --arg inst_label "$inst_label" '
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
						elif ($tab.title | test("^(" + $icon_star + ")?(" + $icon_flag + ")?" + $icon_bolt))
						then "active"
						elif ($tab.title | test("^(" + $icon_star + ")?(" + $icon_flag + ")?" + $icon_stop))
						then "stopped"
						elif ($tab.title | test("^(" + $icon_star + ")?(" + $icon_flag + ")?" + $icon_key))
						then "permission"
						elif ($tab.title | test("^(" + $icon_star + ")?(" + $icon_flag + ")?" + $icon_error))
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
		')"
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

cmd_sockets() {
    _load_icons
    local strip_re
    strip_re="$(_icon_strip_regex)"
    local managed_re="${strip_re}(🟩|🟦|🟥|🟧|🟪|🟢|🟣|🟨|⬜|🔵)?[A-Z]:"

    local found=0
    for sock in "${TMPDIR:-/tmp}"/kitty-*; do
        [[ -e "$sock" ]] || continue
        found=1
        local pid="${sock#"${TMPDIR:-/tmp}"/kitty-}"
        local status="stale"
        local managed_count=""
        local ls_out
        if ls_out="$(kitty @ --to "unix:$sock" ls 2>/dev/null)"; then
            status="active"
            managed_count="$(printf '%s' "$ls_out" | jq -r --arg managed_re "$managed_re" '
				[.[] | .tabs[] | . as $tab |
					select(
						([$tab.windows[] | .user_vars.cc_account // empty | select(. != "")] | length > 0) or
						($tab.title | test($managed_re))
					)
				] | length
			' 2>/dev/null)" || managed_count="?"
        fi
        if [[ -n "$managed_count" && "$managed_count" != "0" ]]; then
            echo "unix:$sock  ($status, pid=$pid, $managed_count managed tab(s))"
        else
            echo "unix:$sock  ($status, pid=$pid)"
        fi
    done

    if [[ $found -eq 0 ]]; then
        echo "No kitty sockets found in ${TMPDIR:-/tmp}/" >&2
    fi

    if [[ -n "${KITTY_LISTEN_ON:-}" ]]; then
        echo "" >&2
        echo "KITTY_LISTEN_ON=$KITTY_LISTEN_ON" >&2
    fi
}

cmd_cleanup() {
    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        echo "Error: No active Kitty instances found." >&2
        return 1
    fi

    local multi_instance=false
    if [[ ${#_INSTANCE_SOCKETS[@]} -gt 1 ]]; then
        multi_instance=true
    fi

    local initials=""
    local idx
    for ((idx = 0; idx < ${#_ACCOUNT_NAMES[@]}; idx++)); do
        local ch="${_ACCOUNT_NAMES[$idx]:0:1}"
        ch="$(echo "$ch" | tr '[:lower:]' '[:upper:]')"
        if [[ "$initials" != *"$ch"* ]]; then
            initials+="$ch"
        fi
    done
    if [[ -z "$initials" ]]; then
        # Derive from default account names as fallback
        local defaults=("work" "personal")
        local d ch_d
        for d in "${defaults[@]}"; do
            ch_d="$(echo "${d:0:1}" | tr '[:lower:]' '[:upper:]')"
            if [[ "$initials" != *"$ch_d"* ]]; then
                initials+="$ch_d"
            fi
        done
    fi
    local title_pattern="${_STRIP_RE}${_OVERRIDE_EMOJI_RE}[$initials]:"

    local stale=""
    local live_ids=""
    for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
        local inst_socket="${_INSTANCE_SOCKETS[$idx]}"
        local inst_label="${_INSTANCE_LABELS[$idx]}"
        local ls_json
        ls_json="$(kitty @ --to "$inst_socket" ls 2>/dev/null)" || continue

        local inst_stale
        inst_stale="$(echo "$ls_json" | jq -r --arg pat "$title_pattern" '
			[.[] | .tabs[] |
				select(.title | test($pat)) |
				select(
					[.windows[] | .foreground_processes[]?.cmdline[]? | test("claude")] | any | not
				) |
				{ title: .title, id: .id }
			] | .[] | [.title, (.id | tostring)] | @tsv
		')"

        if [[ -n "$inst_stale" ]]; then
            while IFS=$'\t' read -r title tab_id; do
                if [[ -n "$stale" ]]; then
                    stale+=$'\n'
                fi
                stale+="${title}"$'\t'"${tab_id}"$'\t'"${inst_socket}"$'\t'"${inst_label}"
            done <<<"$inst_stale"
        fi

        local inst_live
        inst_live="$(echo "$ls_json" | jq -r --arg pat "$title_pattern" '
			[.[] | .tabs[] |
				select(
					(.title | test($pat)) or
					([.windows[] | .user_vars.cc_account // empty | select(. != "")] | length > 0)
				) | .id
			] | .[]
		')"
        if [[ -n "$inst_live" ]]; then
            if [[ -n "$live_ids" ]]; then
                live_ids+=$'\n'
            fi
            live_ids+="$inst_live"
        fi
    done

    if [[ -z "$stale" ]]; then
        echo "No stale sessions found." >&2
    else
        echo "Stale sessions (no Claude process running):" >&2
        echo "" >&2
        while IFS=$'\t' read -r title tab_id sock inst_label; do
            if [[ "$multi_instance" == "true" ]]; then
                echo "  $title (tab id: $tab_id, instance: $inst_label)" >&2
            else
                echo "  $title (tab id: $tab_id)" >&2
            fi
        done <<<"$stale"
        echo "" >&2

        local confirmed=false
        if [[ "$HAS_GUM" == "true" ]]; then
            if gum confirm "Close all stale sessions?" --affirmative "Yes, close them" --negative "Cancel"; then
                confirmed=true
            fi
        else
            read -rp "Close all stale sessions? [y/N] " answer
            if [[ "$answer" =~ ^[Yy] ]]; then
                confirmed=true
            fi
        fi

        if [[ "$confirmed" == "true" ]]; then
            while IFS=$'\t' read -r title tab_id sock inst_label; do
                if kitty @ --to "$sock" close-tab --match "id:$tab_id"; then
                    echo "Closed: $title" >&2
                else
                    echo "Warning: failed to close '$title' (tab id: $tab_id)." >&2
                fi
            done <<<"$stale"
        else
            echo "No sessions closed." >&2
        fi
    fi

    local orphaned=0
    for f in "${_CCM_TMPDIR}"/ccm-activity-*.counts "${_CCM_TMPDIR}"/ccm-notify-* "${_CCM_TMPDIR}"/ccm-dim-*; do
        [[ -e "$f" ]] || continue
        local fname="${f##*/}"
        local file_id=""
        case "$fname" in
        ccm-activity-*.counts)
            file_id="${fname#ccm-activity-}"
            file_id="${file_id%.counts}"
            ;;
        ccm-notify-*) file_id="${fname##*-}" ;;
        ccm-dim-*) file_id="${fname#ccm-dim-}" ;;
        esac
        [[ -z "$file_id" ]] && continue
        if ! echo "$live_ids" | grep -qx "$file_id"; then
            rm -f "$f"
            orphaned=$((orphaned + 1))
        fi
    done
    if [[ $orphaned -gt 0 ]]; then
        echo "Removed $orphaned orphaned activity/notification file(s)." >&2
    fi
}

cmd_save() {
    local output_file="${1:-$CONFIG_FILE}"

    local ls_json
    ls_json="$(kitty @ --to "$SOCKET" ls 2>/dev/null)" || {
        echo "Error: Failed to query kitty." >&2
        return 1
    }

    local accounts_json default_account
    accounts_json="$(build_accounts_json)"
    default_account="$(default_account_name)"

    local sessions
    sessions="$(echo "$ls_json" | jq -r --argjson accounts "$accounts_json" --arg default_account "$default_account" --arg strip_re "$_STRIP_RE" '
		[.[] | .tabs[] |
			select(
				[.windows[] | .foreground_processes[]?.cmdline[]? | test("claude")] | any
			) |
			{
				title: (.title | sub($strip_re; "") | sub("^(🟩|🟦|🟥|🟧|🟪|🟢|🟣|🟨|⬜|🔵)"; "")),
				dir: (
					[.windows[] |
						select([.foreground_processes[]?.cmdline[]? | test("claude")] | any)
					] | if length > 0 then .[0].cwd else .windows[0].cwd end
				),
				account: (
					[.windows[] | .user_vars.cc_account // empty | select(. != "")] |
					if length > 0 then .[0]
					else
						(
							[.windows[] | .foreground_processes[]? |
								select(.cmdline | any(test("CLAUDE_CONFIG_DIR"))) |
								.cmdline[] | capture("CLAUDE_CONFIG_DIR=(?<d>.+)") | .d
							] | if length > 0 then
								(.[0] as $config_dir | (first($accounts[] | select(.dir == $config_dir) | .name) // "unknown"))
							else $default_account end
						)
					end
				),
				flags: (
					[.windows[] | .user_vars.cc_flags // empty | select(. != "")] |
					if length > 0 then .[0] else "" end
				),
				pw: (
					[.windows[] | .user_vars.cc_pw // empty | select(. != "")] |
					if length > 0 then .[0] else "" end
				),
				env: (
					[.windows[] | .user_vars.cc_env // empty | select(. != "")] |
					if length > 0 then .[0] else "" end
				),
				group: (
					[.windows[] | .user_vars.cc_group // empty | select(. != "")] |
					if length > 0 then .[0] else "" end
				)
			}
		] | .[] | [.title, .dir, .account, .flags, .pw, .env, .group] | @tsv
	')"

    if [[ -z "$sessions" ]]; then
        echo "No Claude sessions to save." >&2
        return
    fi

    if [[ -f "$output_file" ]]; then
        if [[ "$HAS_GUM" == "true" ]]; then
            if ! gum confirm "Overwrite $output_file? (backup will be created)" --affirmative "Yes" --negative "Cancel"; then
                echo "Save cancelled." >&2
                return
            fi
        else
            read -rp "Overwrite $output_file? (backup will be created) [y/N] " answer
            if [[ ! "$answer" =~ ^[Yy] ]]; then
                echo "Save cancelled." >&2
                return
            fi
        fi
        if ! cp "$output_file" "${output_file}.bak"; then
            echo "Error: backup failed, save aborted. Original file unchanged." >&2
            return 1
        fi
        echo "Backed up existing config to ${output_file}.bak" >&2
    fi

    _save_sessions_toml "$output_file" "$sessions"

    local count
    count="$(_trim "$(echo "$sessions" | wc -l)")"
    echo "Saved $count session(s) to $output_file" >&2
}

_save_sessions_toml() {
    local output_file="$1"
    local sessions="$2"

    # Build a JSON array from the TSV session data and pipe to toml-helper.
    local json_array="["
    local first=true
    while IFS=$'\t' read -r title dir account flags pw env_vars group; do
        [[ -z "$title" ]] && continue
        dir="${dir/#$HOME/\~}"
        [[ "$first" == "true" ]] || json_array+=","
        first=false
        json_array+="$(jq -nc \
            --arg t "$title" --arg d "$dir" --arg a "$account" \
            --arg f "$flags" --arg p "$pw" --arg e "$env_vars" \
            --arg g "$group" \
            '{title:$t, dir:$d, account:$a, flags:$f, pw:$p, env:$e, group:$g}')"
    done <<<"$sessions"
    json_array+="]"

    if ! echo "$json_array" | python3 "$_CC_COMMON_DIR/toml-helper.py" write-sessions "$output_file"; then
        echo "Error: Failed to write sessions to '$output_file'." >&2
        return 1
    fi
}

