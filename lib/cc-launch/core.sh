#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CORE_LOADED:-}" ]] && return 0
_CC_LAUNCH_CORE_LOADED=1

SOCKET="" # Initialized in main() after argument parsing
_TARGET_INSTANCE_LABEL=""
_TARGET_TAB_ID=""
_MOVE_TARGET_INSTANCE=""

# NOTE: There is an inherent race between tab_exists and launch_session —
# a tab could be created by another process between the check and the launch.
# This is accepted; Kitty will create a duplicate tab, and the user can close
# the extra one via the dashboard.  A locking mechanism would add complexity
# disproportionate to the risk.
tab_exists() {
    local title="$1"
    ccm_debug "tab_exists check: '$title'"
    local ls_json
    ls_json="$(kitty @ --to "$SOCKET" ls 2>/dev/null)" || return 1
    echo "$ls_json" | jq -e --arg t "$title" --arg strip_re "$_STRIP_RE" '
		[.[].tabs[] | select(.title | sub($strip_re; "") | . == $t)] | length > 0
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
    ls_json="$(kitty @ --to "$socket" ls 2>/dev/null)" || return 1

    echo "$ls_json" | jq -r --argjson tid "$tab_id" --argjson accounts "$accounts_json" --arg default_account "$default_account" --arg strip_re "$_STRIP_RE" '
		[.[] | .tabs[] |
			select(.id == $tid) |
			{
				title: (.title | sub($strip_re; "")),
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
				group: (
					[.windows[] | .user_vars.cc_group // empty | select(. != "")] |
					if length > 0 then .[0] else "" end
				)
			}
		] | .[0] // empty | [.title, .account, .dir, .flags, .pw, .env, .group] | @tsv
	'
}

resolve_location_args() {
    local socket="$1"
    local account="$2"
    local title="$3"

    local insert_loc
    insert_loc="$(compute_insert_location "$socket" "$account" "$title")"
    if [[ "$insert_loc" == "first" ]]; then
        echo "--location first"
        return 0
    fi

    if [[ -n "$insert_loc" ]]; then
        kitty @ --to "$socket" focus-tab --match "id:$insert_loc" 2>/dev/null
        sleep 0.1 # Wait for Kitty to process focus change before inserting tab
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

    local cfg_flags="$current_flags"
    local cfg_pw="$current_pw"
    local cfg_env="$current_env"

    if [[ -z "$cfg_flags" && -z "$cfg_pw" && -z "$cfg_env" && -f "$CONFIG_FILE" ]]; then
        local lookup_title="${title%%‾*}"
        _lookup_session_overrides_toml "$lookup_title"
    fi

    printf '%s\t%s\t%s\n' "$cfg_flags" "$cfg_pw" "$cfg_env"
}

_lookup_session_overrides_toml() {
    local lookup_title="$1"
    local cfg_title cfg_dir cfg_acct cfg_f cfg_p cfg_e cfg_g
    while IFS=$'\t' read -r cfg_title cfg_dir cfg_acct cfg_f cfg_p cfg_e cfg_g; do
        if [[ "$cfg_title" == "$lookup_title" ]]; then
            cfg_flags="$cfg_f"
            cfg_pw="$cfg_p"
            cfg_env="$cfg_e"
            break
        fi
    done < <(python3 "$_CC_COMMON_DIR/toml-helper.py" read-sessions "$CONFIG_FILE")
}

launch_session() {
    local title="$1"
    local dir="$2"
    local account="${3:-work}"
    local flags="${4:-}"
    local pw_override="${5:-}"
    local location_args="${6:-}"
    local env_vars="${7:-}"
    local group="${8:-}"

    dir="$(expand_tilde "$dir")"

    if [[ ! -d "$dir" ]]; then
        echo "Error: directory does not exist: $dir" >&2
        return 1
    fi

    if tab_exists "$title"; then
        echo "Skipping [$title]: already exists"
        return 1
    fi

    local config_dir colors
    config_dir="$(claude_config_dir_for_account "$account")"
    colors="$(tab_colors_for_account "$account")"

    local env_flag="--env KITTY_LISTEN_ON=$SOCKET"
    if [[ -n "$config_dir" && "$config_dir" != "$HOME/.claude" ]]; then
        env_flag="$env_flag --env CLAUDE_CONFIG_DIR=$config_dir"
    fi

    if [[ -n "$env_vars" && "$env_vars" != "-" ]]; then
        local kv
        for kv in $env_vars; do
            [[ "$kv" == *=* ]] && env_flag="$env_flag --env $kv"
        done
    fi

    local effective_flags
    effective_flags="$(resolve_claude_flags "$flags")"

    local shell_cmd="exec claude $effective_flags"
    ccm_debug "kitty @ launch: title=$title dir=$dir account=$account flags=$effective_flags"
    # shellcheck disable=SC2086
    kitty @ --to "$SOCKET" launch --type=tab $location_args --allow-remote-control --tab-title "$title" --cwd "$dir" --var cc_account="$account" --var cc_cwd="$dir" --var cc_flags="$flags" --var cc_pw="$pw_override" --var cc_env="$env_vars" --var cc_config_dir="$config_dir" --var cc_group="$group" $env_flag "${SHELL:-/bin/zsh}" -lic "$shell_cmd"

    if [[ $? -ne 0 ]]; then
        echo "Warning: Failed to launch tab '$title' (account=$account, dir=$dir)." >&2
        return 1
    fi

    ccm_debug "Tab '$title' created, setting colors..."
    local retry
    local color_set=false
    for ((retry = 0; retry < 5; retry++)); do
        # shellcheck disable=SC2086
        if kitty @ --to "$SOCKET" set-tab-color --match "title:^${title}$" $colors 2>/dev/null; then
            color_set=true
            break
        fi
        sleep 0.1 # Wait for Kitty to register the new tab before retrying color set
    done

    if [[ "$color_set" != "true" ]]; then
        echo "Warning: Failed to set tab color for '$title'." >&2
    fi

    local pw_mode
    pw_mode="$(resolve_project_window "$pw_override")"
    if [[ -n "$pw_mode" ]]; then
        if ! kitty @ --to "$SOCKET" launch --type=window --match "title:^${title}$" --cwd "$dir" --keep-focus; then
            echo "Warning: Failed to create project window for '$title'." >&2
        elif ! kitty @ --to "$SOCKET" goto-layout --match "title:^${title}$" "$pw_mode"; then
            echo "Warning: Failed to set layout '$pw_mode' for '$title'." >&2
        fi
    fi

    echo "Launched [$title] (account=$account, dir=$dir)"
    return 0
}
