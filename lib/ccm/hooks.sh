#!/usr/bin/env bash

[[ -n "${_CCM_HOOKS_LOADED:-}" ]] && return 0
_CCM_HOOKS_LOADED=1

cmd_hooks_sync() {
	local hooks_example install_script
	install_script="$REPO_DIR/lib/install-hooks.sh"
	hooks_example="$REPO_DIR/config/hooks.example.json"

	if [[ ! -f "$install_script" ]]; then
		err "install-hooks.sh not found at '$install_script'."
		return 1
	fi
	if [[ ! -f "$hooks_example" ]]; then
		err "hooks.example.json not found at '$hooks_example'."
		return 1
	fi

	if [[ $# -gt 0 ]]; then
		bash "$install_script" --account "$1" "$hooks_example"
	else
		bash "$install_script" "$hooks_example"
	fi
}

cmd_hooks_status() {
	if ! command -v jq >/dev/null 2>&1; then
		err "jq is required but not found."
		return 1
	fi

	# Source cc-common.sh for account loading
	# shellcheck source=../cc-common.sh
	source "$REPO_DIR/lib/cc-common.sh"
	load_accounts

	# Load hooks.json for account_overrides
	local ccm_config_dir hooks_json
	ccm_config_dir="$(resolve_config_dir)"
	hooks_json="$ccm_config_dir/hooks.json"

	local account_overrides="{}"
	if [[ -f "$hooks_json" ]]; then
		local _ov
		_ov="$(jq -r '.account_overrides // {}' "$hooks_json" 2>/dev/null || true)"
		if [[ -n "$_ov" && "$_ov" != "null" ]]; then
			account_overrides="$_ov"
		fi
	fi

	# Resolve cc-hook-indicator path
	local hook_cmd=""
	if [[ -x "$HOME/.local/bin/cc-hook-indicator" ]]; then
		hook_cmd="$HOME/.local/bin/cc-hook-indicator"
	elif command -v cc-hook-indicator >/dev/null 2>&1; then
		hook_cmd="$(command -v cc-hook-indicator)"
	fi

	# Resolve cc-activity-log path
	local log_cmd=""
	if [[ -x "$HOME/.local/bin/cc-activity-log" ]]; then
		log_cmd="$HOME/.local/bin/cc-activity-log"
	elif command -v cc-activity-log >/dev/null 2>&1; then
		log_cmd="$(command -v cc-activity-log)"
	fi

	local hooks_example="$REPO_DIR/config/hooks.example.json"
	if [[ ! -f "$hooks_example" ]]; then
		err "hooks.example.json not found."
		return 1
	fi

	local example_hooks
	if [[ -n "$hook_cmd" || -n "$log_cmd" ]]; then
		example_hooks="$(jq --arg cmd "${hook_cmd:-cc-hook-indicator}" --arg logcmd "${log_cmd:-cc-activity-log}" '
			.hooks | walk(if type == "string" then gsub("cc-hook-indicator"; $cmd) | gsub("cc-activity-log"; $logcmd) else . end)
		' "$hooks_example")"
	else
		example_hooks="$(jq '.hooks' "$hooks_example")"
	fi

	# Load hook events from canonical source (data/icons.json)
	local _icons_json=""
	if [[ -f "$HOME/.local/lib/cc-manager/icons.json" ]]; then
		_icons_json="$HOME/.local/lib/cc-manager/icons.json"
	elif [[ -f "$REPO_DIR/data/icons.json" ]]; then
		_icons_json="$REPO_DIR/data/icons.json"
	fi
	local hook_events=()
	if [[ -n "$_icons_json" ]]; then
		while IFS= read -r _ev; do
			hook_events+=("$_ev")
		done < <(jq -r '.hook_events[]' "$_icons_json")
	fi
	if [[ ${#hook_events[@]} -eq 0 ]]; then
		err "Could not load hook_events from icons.json."
		return 1
	fi

	printf "%-20s %s\n" "Account" "Status"
	printf "%-20s %s\n" "-------" "------"

	local i
	for ((i = 0; i < ${#_ACCOUNT_NAMES[@]}; i++)); do
		local acct_name="${_ACCOUNT_NAMES[$i]}"
		local config_dir="${_ACCOUNT_DIRS[$i]}"
		local settings_file="$config_dir/settings.json"
		local status="in_sync"

		if [[ ! -f "$settings_file" ]]; then
			status="missing"
		else
			local acct_ov acct_disabled acct_extra
			acct_ov="$(echo "$account_overrides" | jq --arg name "$acct_name" '.[$name] // {}')"
			acct_disabled="$(echo "$acct_ov" | jq '.disabled_events // []')"
			acct_extra="$(echo "$acct_ov" | jq '
				if .extra_hooks then .extra_hooks
				elif .extra_commands then
					.extra_commands | to_entries | map({
						key: .key,
						value: [.value[] | {hooks: [.]}]
					}) | from_entries
				else {}
				end
			')"

			for event in "${hook_events[@]}"; do
				local is_disabled extra_for_event expected_entries
				is_disabled="$(echo "$acct_disabled" | jq --arg e "$event" 'map(. == $e) | any')"
				extra_for_event="$(echo "$acct_extra" | jq --arg e "$event" '(.[$e] // [])')"

				if [[ "$is_disabled" == "true" ]]; then
					# Expected: extra_hooks only (cc-hook-indicator disabled)
					expected_entries="$extra_for_event"
				else
					local base_entries
					base_entries="$(echo "$example_hooks" | jq --arg e "$event" '.[$e] // []')"
					expected_entries="$(jq -n --argjson a "$base_entries" --argjson b "$extra_for_event" '$a + $b')"
				fi

				# Extract installed entries, filtering to only ccm-managed ones
				# (those containing cc-hook-indicator in any hook command)
				local all_installed managed_installed
				all_installed="$(jq --arg e "$event" '(.hooks[$e] // [])' "$settings_file")"

				if [[ -n "$hook_cmd" || -n "$log_cmd" ]]; then
					managed_installed="$(echo "$all_installed" | jq --arg cmd "${hook_cmd:-cc-hook-indicator}" --arg logcmd "${log_cmd:-cc-activity-log}" \
						'[.[] | select([.hooks[]? | .command // "" | (contains("cc-hook-indicator") or contains($cmd) or contains("cc-activity-log") or contains($logcmd))] | any)]')"
				else
					managed_installed="$(echo "$all_installed" | jq \
						'[.[] | select([.hooks[]? | .command // "" | (contains("cc-hook-indicator") or contains("cc-activity-log"))] | any)]')"
				fi

				# Normalize both sides: strip absolute paths back to bare names
				local norm_expected norm_installed
				if [[ -n "$hook_cmd" || -n "$log_cmd" ]]; then
					norm_expected="$(echo "$expected_entries" | jq --arg cmd "${hook_cmd:-cc-hook-indicator}" --arg logcmd "${log_cmd:-cc-activity-log}" '
						walk(if type == "string" then gsub($cmd; "cc-hook-indicator") | gsub($logcmd; "cc-activity-log") else . end)
					')"
					norm_installed="$(echo "$managed_installed" | jq --arg cmd "${hook_cmd:-cc-hook-indicator}" --arg logcmd "${log_cmd:-cc-activity-log}" '
						walk(if type == "string" then gsub($cmd; "cc-hook-indicator") | gsub($logcmd; "cc-activity-log") else . end)
					')"
				else
					norm_expected="$expected_entries"
					norm_installed="$managed_installed"
				fi

				if [[ "$(echo "$norm_expected" | jq -Sc '.')" != "$(echo "$norm_installed" | jq -Sc '.')" ]]; then
					status="drifted"
					break
				fi
			done
		fi

		printf "%-20s %s\n" "$acct_name" "$status"
	done
}

cmd_hooks() {
	case "${1:-}" in
	sync)
		shift
		cmd_hooks_sync "$@"
		;;
	status)
		cmd_hooks_status
		;;
	"" | --help | -h | help)
		echo "Usage: ccm hooks <sync|status> [ACCOUNT]" >&2
		echo "" >&2
		echo "Subcommands:" >&2
		echo "    sync [ACCOUNT]    Sync hooks to settings.json (all accounts or one)" >&2
		echo "    status            Show drift status for all accounts" >&2
		echo "" >&2
		echo "Run 'ccm help hooks' for detailed usage." >&2
		return 1
		;;
	*)
		err "Unknown hooks subcommand '$1'. Use: sync, status."
		return 1
		;;
	esac
}
