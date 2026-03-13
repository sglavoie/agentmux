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
_COLOR_BACKOFF_FACTOR_NUM=3   # numerator of 3/2 = 1.5x
_COLOR_BACKOFF_FACTOR_DEN=2   # denominator

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
		[.[].tabs[] | select(.title | sub($strip_re; "") | sub("^(🟩|🟦|🟥|🟧|🟪|🟢|🟣|🟨|⬜|🔵)"; "") | . == $t)] | length > 0
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
				title: (.title | sub($strip_re; "") | sub("^(🟩|🟦|🟥|🟧|🟪|🟢|🟣|🟨|⬜|🔵)"; "")),
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
		kitty @ --to "$socket" focus-tab --match "id:$insert_loc" 2>/dev/null \
			|| ccm_debug "focus-tab failed for id:$insert_loc (cosmetic, continuing)"
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

	local cfg_flags="$current_flags"
	local cfg_pw="$current_pw"
	local cfg_env="$current_env"

	if [[ -z "$cfg_flags" && -z "$cfg_pw" && -z "$cfg_env" && -f "$CONFIG_FILE" ]]; then
		local lookup_title
		lookup_title="$(echo "$title" | sed -E "s/^${_STRIP_RE}//")"
		# Strip leading override emoji (colored square/circle)
		lookup_title="$(echo "$lookup_title" | sed -E 's/^(🟩|🟦|🟥|🟧|🟪|🟢|🟣|🟨|⬜|🔵)//')"
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
	done < <(python3 "$_CC_COMMON_DIR/toml-helper.py" read-sessions "$CONFIG_FILE" 2>/dev/null || {
		echo "Warning: failed to parse sessions config '$CONFIG_FILE'." >&2
	})
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
		echo "Error: directory does not exist: $dir (session='$title', account=$account)" >&2
		return 1
	fi

	if tab_exists "$title"; then
		echo "Skipping [$title]: already exists" >&2
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
		local _old_IFS="$IFS"
		IFS=' '
		read -ra _env_arr <<< "$env_vars"
		IFS="$_old_IFS"
		for kv in "${_env_arr[@]}"; do
			[[ "$kv" == *=* ]] && env_flag="$env_flag --env $kv"
		done
	fi

	local effective_flags
	effective_flags="$(resolve_claude_flags "$flags")"

	local shell_cmd="exec claude $effective_flags"
	ccm_debug "kitty @ launch: title=$title dir=$dir account=$account flags=$effective_flags"
	local launch_err
	# shellcheck disable=SC2086
	if ! launch_err="$(kitty @ --to "$SOCKET" launch --type=tab $location_args --allow-remote-control --tab-title "$title" --cwd "$dir" --var cc_account="$account" --var cc_cwd="$dir" --var cc_flags="$flags" --var cc_pw="$pw_override" --var cc_env="$env_vars" --var cc_config_dir="$config_dir" --var cc_group="$group" $env_flag "${SHELL:-/bin/zsh}" -lic "$shell_cmd" 2>&1 >/dev/null)"; then
		local msg="Warning: Failed to launch tab '$title' (account=$account, dir=$dir, socket=$SOCKET)."
		if [[ -n "$launch_err" ]]; then
			msg="$msg Kitty error: $launch_err"
			if [[ "$launch_err" == *"Connection refused"* ]]; then
				msg="$msg (Is remote control enabled in kitty.conf?)"
			elif [[ "$launch_err" == *"No such file"* ]]; then
				msg="$msg (Check that the working directory exists)"
			fi
		fi
		echo "$msg" >&2
		return 1
	fi

	ccm_debug "Tab '$title' created, setting colors..."
	local retry
	local color_set=false
	local color_err=""
	local delay=$_COLOR_INITIAL_DELAY_MS
	for ((retry = 0; retry < _COLOR_MAX_RETRIES; retry++)); do
		# shellcheck disable=SC2086
		if color_err="$(kitty @ --to "$SOCKET" set-tab-color --match "title:^${title}$" $colors 2>&1)"; then
			color_set=true
			break
		fi
		sleep "$(awk "BEGIN {printf \"%.2f\", $delay/1000}")" # Wait for Kitty to register the new tab before retrying
		delay=$((delay * _COLOR_BACKOFF_FACTOR_NUM / _COLOR_BACKOFF_FACTOR_DEN))
	done

	if [[ "$color_set" != "true" ]]; then
		local color_msg="Warning: Failed to set tab color for '$title' (socket=$SOCKET)."
		[[ -n "$color_err" ]] && color_msg="$color_msg Kitty error: $color_err"
		echo "$color_msg" >&2
	fi

	local pw_mode
	pw_mode="$(resolve_project_window "$pw_override")"
	if [[ -n "$pw_mode" ]]; then
		local pw_err
		if ! pw_err="$(kitty @ --to "$SOCKET" launch --type=window --match "title:^${title}$" --cwd "$dir" --keep-focus 2>&1)"; then
			local pw_msg="Warning: Failed to create project window for '$title' (socket=$SOCKET)."
			[[ -n "$pw_err" ]] && pw_msg="$pw_msg Kitty error: $pw_err"
			echo "$pw_msg" >&2
		else
			local layout_err
			if ! layout_err="$(kitty @ --to "$SOCKET" goto-layout --match "title:^${title}$" "$pw_mode" 2>&1)"; then
				local layout_msg="Warning: Failed to set layout '$pw_mode' for '$title' (socket=$SOCKET)."
				[[ -n "$layout_err" ]] && layout_msg="$layout_msg Kitty error: $layout_err"
				echo "$layout_msg" >&2
			fi
		fi
	fi

	echo "Launched [$title] (account=$account, dir=$dir)" >&2
	return 0
}
