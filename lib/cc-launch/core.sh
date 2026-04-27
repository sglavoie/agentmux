#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CORE_LOADED:-}" ]] && return 0
_CC_LAUNCH_CORE_LOADED=1

SOCKET="" # Initialized in main() after argument parsing
_TARGET_INSTANCE_LABEL=""
_TARGET_TAB_ID=""
_MOVE_TARGET_INSTANCE=""

# Retry/backoff constants for set-tab-color after tab creation.
# Kitty may not register a new tab immediately, so we retry with exponential
# backoff: 100ms, 150ms, 225ms, 337ms, 506ms (total ~1.3s worst case).
# 5 retries is sufficient in practice; failures beyond that indicate a real
# problem (wrong socket, remote control disabled, etc.).
_COLOR_MAX_RETRIES=5
_COLOR_INITIAL_DELAY_MS=100
_COLOR_BACKOFF_FACTOR_NUM=3 # numerator of 3/2 = 1.5x
_COLOR_BACKOFF_FACTOR_DEN=2 # denominator

# NOTE: There is an inherent race between tab_exists and launch_session —
# a tab could be created by another process between the check and the launch.
# This is accepted; Kitty will create a duplicate tab, and the user can close
# the extra one via the dashboard.  A locking mechanism would add complexity
# disproportionate to the risk.
tab_exists() {
    local title="$1"
    ccm_debug "tab_exists check: '$title'"
    local ls_json
    ls_json="$(_suppress_stderr kitty @ --to "$SOCKET" ls)" || return 1
    echo "$ls_json" | jq -e --arg t "$title" --arg strip_re "$_STRIP_RE" --arg emoji_re "$_OVERRIDE_EMOJI_RE" '
		[.[].tabs[] | select(.title | sub($strip_re; "") | sub("^" + $emoji_re; "") | . == $t)] | length > 0
	' >/dev/null 2>&1
}

resolve_instance_socket() {
    local label="$1"
    discover_all_sockets
    local idx
    for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
        if [[ "${_INSTANCE_LABELS[$idx]}" == "$label" ]]; then
            echo "${_INSTANCE_SOCKETS[$idx]}"
            return 0
        fi
    done
    return 1
}

resolve_instance_socket_or_die() {
    local label="$1"
    local result
    result="$(resolve_instance_socket "$label")" || {
        err "No active Kitty instance found with label '$label'."
        echo "Available instances:" >&2
        print_available_instances
        exit 1
    }
    echo "$result"
}

print_available_instances() {
    discover_all_sockets
    local idx
    for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
        echo "  ${_INSTANCE_LABELS[$idx]}  (${_INSTANCE_SOCKETS[$idx]})" >&2
    done
}

tab_metadata_by_id() {
    local socket="$1"
    local tab_id="$2"
    local accounts_json="$3"
    local default_account="$4"
    local ls_json
    ls_json="$(_suppress_stderr kitty @ --to "$socket" ls)" || return 1

    echo "$ls_json" | jq -r --argjson tid "$tab_id" --argjson accounts "$accounts_json" --arg default_account "$default_account" --arg strip_re "$_STRIP_RE" --arg emoji_re "$_OVERRIDE_EMOJI_RE" '
		[.[] | .tabs[] |
			select(.id == $tid) |
			{
				title: (.title | sub($strip_re; "") | sub("^" + $emoji_re; "")),
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
				dir: (
					[.windows[] | .user_vars.cc_cwd // empty | select(. != "")] |
					if length > 0 then .[0]
					else
						([.windows[] |
							select([.foreground_processes[]?.cmdline[]? | test("claude")] | any)
						] | if length > 0 then .[0].cwd else .windows[0].cwd end)
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
		] | .[0] // empty | [.title, .account, .dir, .flags, .pw, .env, .groups, .mute, .commands] | @tsv
	'
}

resolve_location_args() {
    local socket="$1"
    local account="$2"
    local title="$3"
    local groups="${4:-}"

    local insert_loc
    insert_loc="$(compute_insert_location "$socket" "$account" "$title" "$groups")"
    if [[ "$insert_loc" == "first" ]]; then
        echo "--location first"
        return 0
    fi

    if [[ -n "$insert_loc" ]]; then
        _suppress_stderr kitty @ --to "$socket" focus-tab --match "id:$insert_loc" ||
            ccm_debug "focus-tab failed for id:$insert_loc (cosmetic, continuing)"
        # NOTE: Brief delay for Kitty to process the focus change before
        # inserting the new tab. A polling approach would be more robust but
        # adds complexity for marginal benefit in practice.
        sleep 0.1
        echo "--location after"
        return 0
    fi

    echo ""
}

lookup_session_overrides() {
    local title="$1"
    local current_flags="$2"
    local current_pw="$3"
    local current_env="$4"
    local current_commands="$5"

    # Always attempt config lookup
    local toml_flags="" toml_pw="" toml_env="" toml_commands=""
    if [[ -f "$CONFIG_FILE" ]]; then
        local lookup_title
        lookup_title="$(echo "$title" | sed -E "s/^${_STRIP_RE}//")"
        # Strip leading override emoji (colored square/circle)
        lookup_title="$(echo "$lookup_title" | sed -E "s/^$(_override_emoji_re)//")"
        local toml_result
        toml_result="$(_lookup_session_overrides_toml "$lookup_title")"
        if [[ -n "$toml_result" ]]; then
            IFS=$'\t' read -r toml_flags toml_pw toml_env toml_commands <<<"$toml_result"
        fi
    fi

    # Per-field: use live value if present, else config value
    local cfg_flags="${current_flags:-$toml_flags}"
    local cfg_pw="${current_pw:-$toml_pw}"
    local cfg_env="${current_env:-$toml_env}"
    local cfg_commands="${current_commands:-$toml_commands}"

    printf '%s\t%s\t%s\t%s\n' "$cfg_flags" "$cfg_pw" "$cfg_env" "$cfg_commands"
}

_lookup_session_overrides_toml() {
    local lookup_title="$1"
    local _parse_err_file="${_CCM_TMPDIR}/ccm-parse-err.$$"
    local cfg_title cfg_f cfg_p cfg_e cfg_commands
    while IFS=$'\t' read -r cfg_title _ _ cfg_f cfg_p cfg_e _ _ cfg_commands; do
        if [[ "$cfg_title" == "$lookup_title" ]]; then
            printf '%s\t%s\t%s\t%s\n' "$cfg_f" "$cfg_p" "$cfg_e" "$cfg_commands"
            rm -f "$_parse_err_file"
            return 0
        fi
    done < <(python3 "$_CC_COMMON_DIR/toml-helper.py" read-sessions "$CONFIG_FILE" 2>"$_parse_err_file" || {
        warn "failed to parse sessions config '$CONFIG_FILE'."
        [[ -s "$_parse_err_file" ]] && sed 's/^/  Detail: /' "$_parse_err_file" >&2
    })
    rm -f "$_parse_err_file"
}

launch_session() {
    local title="$1"
    local dir="$2"
    local account="${3:-work}"
    local flags="${4:-}"
    local pw_override="${5:-}"
    local location_args_str="${6:-}"
    local env_vars="${7:-}"
    local groups="${8:-}"
    local mute="${9:-}"
    local commands_str="${10:-}"
    local -a location_args=()
    if [[ -n "$location_args_str" ]]; then
        read -ra location_args <<<"$location_args_str"
    fi

    dir="$(expand_tilde "$dir")"

    if [[ ! -d "$dir" ]]; then
        err "directory does not exist: $dir (session='$title', account=$account)"
        return 1
    fi

    if tab_exists "$title"; then
        echo "Skipping [$title]: already exists" >&2
        return 1
    fi

    local config_dir colors color_name
    if ! config_dir="$(claude_config_dir_for_account "$account")"; then
        echo "  Hint: Check the account name in sessions.toml or add '$account' to accounts.toml (session='$title')." >&2
        return 1
    fi
    color_name="$(_account_color "$account")" || color_name=""
    colors="$(tab_colors_for_account "$account")"

    local -a env_flags=("--env" "KITTY_LISTEN_ON=$SOCKET")
    if [[ -n "$config_dir" && "$config_dir" != "$HOME/.claude" ]]; then
        env_flags+=("--env" "CLAUDE_CONFIG_DIR=$config_dir")
    fi

    if [[ -n "$env_vars" && "$env_vars" != "-" ]]; then
        local kv
        while IFS= read -r kv; do
            [[ -n "$kv" && "$kv" == *=* ]] && env_flags+=("--env" "$kv")
        done < <(printf '%s' "$env_vars" | python3 -c "import shlex, sys; [print(t) for t in shlex.split(sys.stdin.read())]")
    fi

    # Handle mute: prepend icon and add user var
    local -a mute_args=()
    if [[ "$mute" == "true" ]]; then
        if [[ "$title" != *"$_ICON_MUTE"* ]]; then
            title="${_ICON_MUTE}${title}"
        fi
        mute_args=(--var "cc_mute=1")
    fi

    local effective_flags
    effective_flags="$(resolve_claude_flags "$flags")"

    local shell_cmd="exec claude $effective_flags"
    ccm_debug "kitty @ launch: title=$title dir=$dir account=$account flags=$effective_flags"
    local _launch_err_file="${_CCM_TMPDIR}/launch-err.$$"
    local window_id
    if ! window_id="$(kitty @ --to "$SOCKET" launch --type=tab ${location_args[@]+"${location_args[@]}"} --allow-remote-control --tab-title "$title" --cwd "$dir" --var cc_account="$account" --var cc_color="$color_name" --var cc_cwd="$dir" --var cc_flags="$flags" --var cc_pw="$pw_override" --var cc_env="$env_vars" --var cc_config_dir="$config_dir" --var cc_groups="$groups" --var cc_commands="$commands_str" ${mute_args[@]+"${mute_args[@]}"} "${env_flags[@]}" "${SHELL:-/bin/zsh}" -lic "$shell_cmd" 2>"$_launch_err_file")"; then
        local launch_err
        launch_err="$(<"$_launch_err_file" 2>/dev/null)"
        rm -f "$_launch_err_file"
        local msg="Failed to launch tab '$title' (account=$account, dir=$dir, socket=$SOCKET)."
        if [[ -n "$launch_err" ]]; then
            msg="$msg Kitty error: $launch_err"
            if [[ "$launch_err" == *"Connection refused"* ]]; then
                msg="$msg (Is remote control enabled in kitty.conf?)"
            elif [[ "$launch_err" == *"No such file"* ]]; then
                msg="$msg (Check that the working directory exists)"
            fi
        fi
        warn "$msg"
        return 1
    fi
    rm -f "$_launch_err_file"
    window_id="${window_id%%[[:space:]]*}"

    ccm_debug "Tab '$title' created (window_id=$window_id), setting colors..."
    local retry
    local color_set=false
    local color_err=""
    local delay=$_COLOR_INITIAL_DELAY_MS
    for ((retry = 0; retry < _COLOR_MAX_RETRIES; retry++)); do
        # shellcheck disable=SC2086
        if color_err="$(kitty @ --to "$SOCKET" set-tab-color --match "window_id:$window_id" $colors 2>&1)"; then
            color_set=true
            break
        fi
        sleep "$(awk "BEGIN {printf \"%.2f\", $delay/1000}")" # Wait for Kitty to register the new tab before retrying
        delay=$((delay * _COLOR_BACKOFF_FACTOR_NUM / _COLOR_BACKOFF_FACTOR_DEN))
    done

    if [[ "$color_set" != "true" ]]; then
        local color_msg="Failed to set tab color for '$title' (socket=$SOCKET)."
        [[ -n "$color_err" ]] && color_msg="$color_msg Kitty error: $color_err"
        warn "$color_msg"
    fi

    # Auto-enable stack layout if commands set and no pw_override (C1)
    if [[ -n "$commands_str" && "$commands_str" != "-" && -z "$pw_override" ]]; then
        pw_override="stack"
    fi

    local pw_mode
    pw_mode="$(resolve_project_window "$pw_override")"
    if [[ -n "$pw_mode" ]]; then
        if [[ "$commands_str" == "-" ]]; then
            :
        elif [[ -n "$commands_str" ]]; then
            local -a cmds=()
            IFS=$'\x1f' read -ra cmds <<<"$commands_str"
            for cmd_entry in "${cmds[@]}"; do
                if [[ "$cmd_entry" == "shell" ]]; then
                    kitty @ --to "$SOCKET" launch --type=window \
                        --match "window_id:$window_id" \
                        --title "shell" \
                        --cwd "$dir" --keep-focus
                elif [[ -n "$cmd_entry" ]]; then
                    kitty @ --to "$SOCKET" launch --type=window \
                        --match "window_id:$window_id" \
                        --title "$cmd_entry" \
                        --cwd "$dir" --keep-focus --hold \
                        -- "${SHELL:-/bin/zsh}" -lic "$cmd_entry"
                fi
            done
        else
            # Legacy: no commands, just a plain shell window
            kitty @ --to "$SOCKET" launch --type=window \
                --match "window_id:$window_id" \
                --cwd "$dir" --keep-focus
        fi
        kitty @ --to "$SOCKET" goto-layout \
            --match "window_id:$window_id" "$pw_mode"
    fi

    echo "Launched [$title] (account=$account, dir=$dir)" >&2
    return 0
}
