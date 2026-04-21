#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CMD_MAINTENANCE_SAVE_LOADED:-}" ]] && return 0
_CC_LAUNCH_CMD_MAINTENANCE_SAVE_LOADED=1

_extract_sessions_from_ls() {
    local ls_json="$1"
    local accounts_json="$2"
    local default_account="$3"
    echo "$ls_json" | jq -r --argjson accounts "$accounts_json" --arg default_account "$default_account" --arg strip_re "$_STRIP_RE" --arg emoji_re "$_OVERRIDE_EMOJI_RE" '
		[.[] | .tabs[] |
			select(
				[.windows[] | .foreground_processes[]?.cmdline[]? | test("claude")] | any
			) |
			{
				title: (.title | sub($strip_re; "") | sub("^" + $emoji_re; "")),
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
				groups: (
					[.windows[] | .user_vars.cc_groups // empty | select(. != "")] |
					if length > 0 then .[0] else "" end
				),
				mute: (
					[.windows[] | .user_vars.cc_mute // empty | select(. == "1")] |
					if length > 0 then "true" else "" end
				),
				commands: (
					[.windows[] | .user_vars.cc_commands // empty | select(. != "")] |
					if length > 0 then .[0] else "" end
				)
			}
		] | .[] | [.title, .dir, .account, .flags, .pw, .env, .groups, .mute, .commands] | @tsv
	'
}

cmd_save() {
    local output_file="${1:-$CONFIG_FILE}"

    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        err "No active Kitty instances found."
        return 1
    fi

    local accounts_json default_account
    accounts_json="$(build_accounts_json)"
    default_account="$(default_account_name)"

    local sessions=""
    local idx
    for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
        local inst_socket="${_INSTANCE_SOCKETS[$idx]}"
        local ls_json
        ls_json="$(kitty @ --to "$inst_socket" ls 2>/dev/null)" || continue

        local inst_sessions
        inst_sessions="$(_extract_sessions_from_ls "$ls_json" "$accounts_json" "$default_account")"
        if [[ -n "$inst_sessions" ]]; then
            if [[ -n "$sessions" ]]; then
                sessions+=$'\n'
            fi
            sessions+="$inst_sessions"
        fi
    done

    if [[ -z "$sessions" ]]; then
        echo "No Claude sessions to save." >&2
        return
    fi

    if [[ -f "$output_file" ]]; then
        if ! confirm "Overwrite $output_file? (backup will be created)"; then
            echo "Save cancelled." >&2
            return
        fi
        if ! cp "$output_file" "${output_file}.bak"; then
            err "backup failed, save aborted. Original file unchanged."
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

    local json_array
    json_array="$(echo "$sessions" | jq -Rnc --arg home "$HOME" '
        [inputs | select(length > 0) | split("\t") |
         { title: .[0], dir: (.[1] | if startswith($home) then "~" + .[$home | length:] else . end),
           account: .[2], flags: .[3], pw: .[4], env: .[5],
           groups: (.[6] | if . == "" then [] else split("\u001f") end), mute: .[7],
           commands: (.[8] // "" | if . == "" then [] else split("\u001f") end) }]
    ')" || {
        err "Failed to build session JSON."
        return 1
    }

    if ! echo "$json_array" | python3 "$_CC_COMMON_DIR/toml-helper.py" write-sessions "$output_file"; then
        err "Failed to write sessions to '$output_file'."
        return 1
    fi
}

_append_session_to_config() {
    local config_file="$1"
    local new_title="$2" new_dir="$3" new_account="$4"
    local new_flags="$5" new_pw="$6" new_env="$7" new_groups="$8"
    local new_mute="${9:-}"
    local new_commands="${10:-}"

    local all_tsv=""
    if [[ -f "$config_file" ]]; then
        all_tsv="$(python3 "$_CC_COMMON_DIR/toml-helper.py" read-sessions "$config_file" 2>/dev/null)" || all_tsv=""
    fi

    new_dir="${new_dir/#$HOME/\~}"
    local new_line
    new_line="$(printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s' "$new_title" "$new_dir" "$new_account" "$new_flags" "$new_pw" "$new_env" "$new_groups" "$new_mute" "$new_commands")"
    if [[ -n "$all_tsv" ]]; then
        all_tsv+=$'\n'"$new_line"
    else
        all_tsv="$new_line"
    fi

    local json_array
    json_array="$(echo "$all_tsv" | jq -Rnc '
        [inputs | select(length > 0) | split("\t") |
         { title: .[0], dir: .[1], account: .[2], flags: .[3],
           pw: .[4], env: .[5],
           groups: (.[6] | if . == "" then [] else split("\u001f") end), mute: .[7],
           commands: (.[8] // "" | if . == "" then [] else split("\u001f") end) }]
    ')" || {
        warn "failed to build session JSON."
        return 1
    }

    if ! echo "$json_array" | python3 "$_CC_COMMON_DIR/toml-helper.py" write-sessions "$config_file"; then
        warn "failed to update config file with new session."
        return 1
    fi
}
