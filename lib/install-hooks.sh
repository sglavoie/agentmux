#!/usr/bin/env bash
set -euo pipefail

# install-hooks.sh — Idempotently install cc-hook-indicator hooks into each
# account's Claude Code settings.json.
#
# Usage: bash lib/install-hooks.sh /path/to/config/hooks.example.json
#
# Reads account config dirs from accounts.conf (same logic as cc-launch),
# then for each config dir merges the hook entries from hooks.example.json
# into $config_dir/settings.json without replacing existing hooks.

# ── Argument parsing ─────────────────────────────────────────────────────────

FILTER_ACCOUNT=""
HOOKS_EXAMPLE=""

while [[ $# -gt 0 ]]; do
	case "$1" in
	--account)
		[[ $# -lt 2 ]] && {
			echo "Error: --account requires a NAME argument" >&2
			exit 1
		}
		FILTER_ACCOUNT="$2"
		shift 2
		;;
	-*)
		echo "Error: unknown option: $1" >&2
		echo "Usage: install-hooks.sh [--account NAME] <hooks-example-json>" >&2
		exit 1
		;;
	*)
		HOOKS_EXAMPLE="$1"
		shift
		;;
	esac
done

if [[ -z "$HOOKS_EXAMPLE" ]]; then
	echo "Usage: install-hooks.sh [--account NAME] <hooks-example-json>" >&2
	exit 1
fi

if [[ ! -f "$HOOKS_EXAMPLE" ]]; then
	echo "Error: hooks example file not found: $HOOKS_EXAMPLE" >&2
	exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
	echo "Error: jq is required but not found." >&2
	exit 1
fi

# ── Resolve absolute path to cc-hook-indicator ───────────────────────────────

HOOK_CMD=""
# Try the standard install location first (matches Makefile PREFIX_BIN)
if [[ -x "$HOME/.local/bin/cc-hook-indicator" ]]; then
	HOOK_CMD="$HOME/.local/bin/cc-hook-indicator"
else
	# Derive from repo layout: lib/install-hooks.sh -> bin/cc-hook-indicator
	repo_dir="$(cd "$(dirname "$HOOKS_EXAMPLE")/.." 2>/dev/null && pwd)"
	if [[ -x "$repo_dir/bin/cc-hook-indicator" ]]; then
		HOOK_CMD="$repo_dir/bin/cc-hook-indicator"
	fi
fi

if [[ -z "$HOOK_CMD" ]]; then
	echo "Error: cc-hook-indicator not found in ~/.local/bin/ or repo bin/." >&2
	exit 1
fi

# ── Load accounts ────────────────────────────────────────────────────────────

ACCOUNTS_CONFIG="${CC_MANAGER_ACCOUNTS:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager/accounts.conf}"

expand_tilde() {
	local path="$1"
	if [[ "$path" == "~"* ]]; then
		echo "${HOME}${path:1}"
	else
		echo "$path"
	fi
}

CONFIG_DIRS=()
ACCOUNT_NAMES=()

if [[ -f "$ACCOUNTS_CONFIG" ]]; then
	while IFS= read -r line || [[ -n "$line" ]]; do
		[[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
		_local_name="" local_dir="" _local_color=""
		IFS='|' read -r _local_name local_dir _local_color <<<"$line"
		_local_name="$(echo "$_local_name" | xargs)"
		local_dir="$(echo "$local_dir" | xargs)"
		local_dir="$(expand_tilde "$local_dir")"
		[[ -z "$local_dir" ]] && local_dir="$HOME/.claude"
		# When --account is set, skip non-matching accounts
		if [[ -n "$FILTER_ACCOUNT" && "$_local_name" != "$FILTER_ACCOUNT" ]]; then
			continue
		fi
		ACCOUNT_NAMES+=("$_local_name")
		CONFIG_DIRS+=("$local_dir")
	done <"$ACCOUNTS_CONFIG"
fi

# Fallback: if no accounts.conf or no entries found, use the default dir
if [[ ${#CONFIG_DIRS[@]} -eq 0 ]]; then
	if [[ -n "$FILTER_ACCOUNT" ]]; then
		echo "Error: account '$FILTER_ACCOUNT' not found in $ACCOUNTS_CONFIG" >&2
		exit 1
	fi
	CONFIG_DIRS=("$HOME/.claude")
	ACCOUNT_NAMES=("default")
fi

# ── Load account overrides from hooks.json ──────────────────────────────────

CCM_CONFIG_DIR="${CC_MANAGER_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager}"
HOOKS_JSON="$CCM_CONFIG_DIR/hooks.json"

ACCOUNT_OVERRIDES="{}"
if [[ -f "$HOOKS_JSON" ]]; then
	_overrides="$(jq -r '.account_overrides // {}' "$HOOKS_JSON" 2>/dev/null || true)"
	if [[ -n "$_overrides" && "$_overrides" != "null" ]]; then
		ACCOUNT_OVERRIDES="$_overrides"
	fi
fi

# ── Merge hooks into each settings.json ──────────────────────────────────────

# Extract the hook entries we want to install from the example file,
# substituting bare cc-hook-indicator with its absolute path
EXAMPLE_HOOKS="$(jq --arg cmd "$HOOK_CMD" '
    .hooks | walk(if type == "string" then gsub("cc-hook-indicator"; $cmd) else . end)
' "$HOOKS_EXAMPLE")"

# List of hook events to process
HOOK_EVENTS=(UserPromptSubmit PostToolUse PostToolUseFailure PermissionRequest Stop TaskCompleted SessionEnd)

for i in "${!CONFIG_DIRS[@]}"; do
	config_dir="${CONFIG_DIRS[$i]}"
	account_name="${ACCOUNT_NAMES[$i]}"
	settings_file="$config_dir/settings.json"

	# Look up this account's overrides (disabled_events and extra_commands)
	acct_overrides="$(echo "$ACCOUNT_OVERRIDES" | jq --arg name "$account_name" '.[$name] // {}')"
	acct_disabled="$(echo "$acct_overrides" | jq '.disabled_events // []')"
	acct_extra="$(echo "$acct_overrides" | jq '.extra_commands // {}')"

	# If settings.json doesn't exist, create it with hooks (respecting overrides)
	if [[ ! -f "$settings_file" ]]; then
		mkdir -p "$config_dir"
		# Build a fresh hooks object respecting disabled_events and extra_commands
		create_filter=''
		for event in "${HOOK_EVENTS[@]}"; do
			is_disabled="$(echo "$acct_disabled" | jq --arg e "$event" 'map(. == $e) | any')"
			extra_for_event="$(echo "$acct_extra" | jq --arg e "$event" --arg cmd "$HOOK_CMD" '
				(.[$e] // []) | walk(if type == "string" then gsub("cc-hook-indicator"; $cmd) else . end)
			')"
			if [[ "$is_disabled" == "true" ]]; then
				# Event disabled: only include extra_commands if any
				if [[ "$(echo "$extra_for_event" | jq 'length')" -gt 0 ]]; then
					create_filter="$create_filter | .hooks[\"$event\"] = $extra_for_event"
				fi
			else
				example_entries="$(echo "$EXAMPLE_HOOKS" | jq --arg event "$event" '.[$event] // []')"
				combined="$(jq -n --argjson a "$example_entries" --argjson b "$extra_for_event" '$a + $b')"
				create_filter="$create_filter | .hooks[\"$event\"] = $combined"
			fi
		done
		jq -n "{ hooks: {} } $create_filter" >"$settings_file"
		echo "  Created $settings_file with cc-hook-indicator hooks"
		continue
	fi

	# Build the jq filter that, for each event, strips existing ccm entries
	# and appends the current ones from hooks.example.json.
	# User-defined hook entries (those without cc-hook-indicator) are preserved.
	jq_filter='.hooks //= {}'
	for event in "${HOOK_EVENTS[@]}"; do
		is_disabled="$(echo "$acct_disabled" | jq --arg e "$event" 'map(. == $e) | any')"
		extra_for_event="$(echo "$acct_extra" | jq --arg e "$event" --arg cmd "$HOOK_CMD" '
			(.[$e] // []) | walk(if type == "string" then gsub("cc-hook-indicator"; $cmd) else . end)
		')"

		if [[ "$is_disabled" == "true" ]]; then
			# Event disabled: strip cc-hook-indicator entries, preserve user entries, add extra_commands
			jq_filter="$jq_filter"'
                | .hooks["'"$event"'"] = (
                    [(.hooks["'"$event"'"] // [])[] |
                        select([.hooks[]? | .command // "" | test("cc-hook-indicator")] | any | not)
                    ] + '"$extra_for_event"'
                )'
		else
			new_entries="$(echo "$EXAMPLE_HOOKS" | jq --arg event "$event" '.[$event] // []')"
			combined="$(jq -n --argjson a "$new_entries" --argjson b "$extra_for_event" '$a + $b')"
			jq_filter="$jq_filter"'
                | .hooks["'"$event"'"] = (
                    [(.hooks["'"$event"'"] // [])[] |
                        select([.hooks[]? | .command // "" | test("cc-hook-indicator")] | any | not)
                    ] + '"$combined"'
                )'
		fi
	done

	tmp_file="${settings_file}.tmp"
	jq "$jq_filter" "$settings_file" >"$tmp_file"

	# Compare to see if anything actually changed
	if diff -q "$settings_file" "$tmp_file" >/dev/null 2>&1; then
		rm "$tmp_file"
		echo "  Skipped $settings_file (hooks already up to date)"
	else
		cp "$settings_file" "${settings_file}.bak"
		mv "$tmp_file" "$settings_file"
		echo "  Updated cc-hook-indicator hooks in $settings_file"
	fi
done
