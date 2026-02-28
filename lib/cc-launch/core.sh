#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CORE_LOADED:-}" ]] && return 0
_CC_LAUNCH_CORE_LOADED=1

SOCKET="" # Initialized in main() after argument parsing
_TARGET_INSTANCE_LABEL=""
_TARGET_TAB_ID=""
_MOVE_TARGET_INSTANCE=""

tab_exists() {
    local title="$1"
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
								(.[0] as $config_dir | $accounts[$config_dir] // "unknown")
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
				)
			}
		] | .[0] // empty | [.title, .account, .dir, .flags, .pw, .env] | @tsv
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

    local cfg_flags="$current_flags"
    local cfg_pw="$current_pw"
    local cfg_env="$current_env"

    if [[ -z "$cfg_flags" && -z "$cfg_pw" && -z "$cfg_env" && -f "$CONFIG_FILE" ]]; then
        local lookup_title="${title%%‾*}"
        local cfg_line
        while IFS= read -r cfg_line || [[ -n "$cfg_line" ]]; do
            [[ -z "$cfg_line" || "$cfg_line" =~ ^[[:space:]]*# ]] && continue
            local cfg_title cfg_f cfg_p cfg_e
            IFS='|' read -r cfg_title _ _ cfg_f cfg_p cfg_e <<<"$cfg_line"
            cfg_title="$(_trim "$cfg_title")"
            if [[ "$cfg_title" == "$lookup_title" ]]; then
                cfg_flags="$(_trim "${cfg_f:-}")"
                cfg_pw="$(_trim "${cfg_p:-}")"
                cfg_env="$(_trim "${cfg_e:-}")"
                break
            fi
        done <"$CONFIG_FILE"
    fi

    printf '%s\t%s\t%s\n' "$cfg_flags" "$cfg_pw" "$cfg_env"
}

launch_session() {
    local title="$1"
    local dir="$2"
    local account="${3:-work}"
    local flags="${4:-}"
    local pw_override="${5:-}"
    local location_args="${6:-}"
    local env_vars="${7:-}"

    dir="$(expand_tilde "$dir")"

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
    # shellcheck disable=SC2086
    kitty @ --to "$SOCKET" launch --type=tab $location_args --allow-remote-control --tab-title "$title" --cwd "$dir" --var cc_account="$account" --var cc_cwd="$dir" --var cc_flags="$flags" --var cc_pw="$pw_override" --var cc_env="$env_vars" $env_flag "${SHELL:-/bin/zsh}" -lic "$shell_cmd"

    local retry
    for ((retry = 0; retry < 5; retry++)); do
        # shellcheck disable=SC2086
        if kitty @ --to "$SOCKET" set-tab-color --match "title:^${title}$" $colors 2>/dev/null; then
            break
        fi
        sleep 0.1
    done

    local pw_mode
    pw_mode="$(resolve_project_window "$pw_override")"
    if [[ -n "$pw_mode" ]]; then
        kitty @ --to "$SOCKET" launch --type=window --match "title:^${title}$" --cwd "$dir" --keep-focus
        kitty @ --to "$SOCKET" goto-layout --match "title:^${title}$" "$pw_mode"
    fi

    echo "Launched [$title] (account=$account, dir=$dir)"
    return 0
}
