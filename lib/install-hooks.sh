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

HOOKS_EXAMPLE="${1:?Usage: install-hooks.sh <hooks-example-json>}"

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

if [[ -f "$ACCOUNTS_CONFIG" ]]; then
	while IFS= read -r line || [[ -n "$line" ]]; do
		[[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
		_local_name="" local_dir="" _local_color=""
		IFS='|' read -r _local_name local_dir _local_color <<<"$line"
		local_dir="$(echo "$local_dir" | xargs)"
		local_dir="$(expand_tilde "$local_dir")"
		[[ -z "$local_dir" ]] && local_dir="$HOME/.claude"
		CONFIG_DIRS+=("$local_dir")
	done <"$ACCOUNTS_CONFIG"
fi

# Fallback: if no accounts.conf or no entries found, use the default dir
if [[ ${#CONFIG_DIRS[@]} -eq 0 ]]; then
	CONFIG_DIRS=("$HOME/.claude")
fi

# ── Merge hooks into each settings.json ──────────────────────────────────────

# Extract the hook entries we want to install from the example file,
# substituting bare cc-hook-indicator with its absolute path
EXAMPLE_HOOKS="$(jq --arg cmd "$HOOK_CMD" '
    .hooks | walk(if type == "string" then gsub("cc-hook-indicator"; $cmd) else . end)
' "$HOOKS_EXAMPLE")"

# List of hook events to process
HOOK_EVENTS=(UserPromptSubmit PostToolUse PostToolUseFailure PermissionRequest Stop SessionEnd)

for config_dir in "${CONFIG_DIRS[@]}"; do
	settings_file="$config_dir/settings.json"

	# If settings.json doesn't exist, create it with just the hooks
	if [[ ! -f "$settings_file" ]]; then
		mkdir -p "$config_dir"
		jq --arg cmd "$HOOK_CMD" '
            walk(if type == "string" then gsub("cc-hook-indicator"; $cmd) else . end)
        ' "$HOOKS_EXAMPLE" >"$settings_file"
		echo "  Created $settings_file with cc-hook-indicator hooks"
		continue
	fi

	# Build the jq filter that, for each event, strips existing ccm entries
	# and appends the current ones from hooks.example.json.
	# User-defined hook entries (those without cc-hook-indicator) are preserved.
	jq_filter='.hooks //= {}'
	for event in "${HOOK_EVENTS[@]}"; do
		new_entries="$(echo "$EXAMPLE_HOOKS" | jq --arg event "$event" '.[$event] // []')"
		jq_filter="$jq_filter"'
            | .hooks["'"$event"'"] = (
                [(.hooks["'"$event"'"] // [])[] |
                    select([.hooks[]? | .command // "" | test("cc-hook-indicator")] | any | not)
                ] + '"$new_entries"'
            )'
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
