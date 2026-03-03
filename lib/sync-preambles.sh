#!/usr/bin/env bash
set -euo pipefail

# sync-preambles.sh — Sync documentation preambles from example config files
# into user config files, preserving user data below the marker line.
#
# Usage: bash lib/sync-preambles.sh /path/to/repo

REPO_DIR="${1:?Usage: sync-preambles.sh <repo-dir>}"

MARKER='# --- END OF DOCUMENTATION (do not edit above this line) ---'

# ── Config path resolution (mirrors cc-common.sh / ccm logic) ────────────────

resolve_config_dir() {
	if [[ -n "${CC_MANAGER_CONFIG:-}" ]]; then
		dirname "$CC_MANAGER_CONFIG"
	elif [[ -n "${CC_MANAGER_ACCOUNTS:-}" ]]; then
		dirname "$CC_MANAGER_ACCOUNTS"
	else
		echo "${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager"
	fi
}

resolve_user_file() {
	local key="$1"
	local config_dir
	config_dir="$(resolve_config_dir)"

	case "$key" in
	accounts) echo "${CC_MANAGER_ACCOUNTS:-$config_dir/accounts.toml}" ;;
	sessions) echo "${CC_MANAGER_CONFIG:-$config_dir/sessions.toml}" ;;
	defaults) echo "${CC_MANAGER_DEFAULTS:-$config_dir/defaults.toml}" ;;
	esac
}

# ── Per-file sync ─────────────────────────────────────────────────────────────

sync_file() {
	local key="$1"
	local user_file
	user_file="$(resolve_user_file "$key")"

	local example_file="$REPO_DIR/config/${key}.example.toml"

	# Skip if user file doesn't exist
	if [[ ! -f "$user_file" ]]; then
		return
	fi

	# Skip if example file doesn't exist
	if [[ ! -f "$example_file" ]]; then
		echo "  Warning: example file not found: $example_file" >&2
		return
	fi

	# If marker not found in user file, warn and skip
	if ! grep -qF "$MARKER" "$user_file"; then
		echo "  Warning: cannot sync $user_file — no marker found. Add the marker manually:" >&2
		echo "           $MARKER" >&2
		return
	fi

	# Extract new preamble (everything up to and including the marker)
	local new_preamble
	new_preamble="$(sed -n "1,/^# --- END OF DOCUMENTATION/p" "$example_file")"

	# Extract user data (everything after the marker)
	local user_data
	user_data="$(sed "1,/^# --- END OF DOCUMENTATION/d" "$user_file")"

	# Write to tmp file
	local tmp_file="${user_file}.tmp"
	printf '%s\n%s' "$new_preamble" "$user_data" >"$tmp_file"

	# Compare and replace if changed
	if diff -q "$user_file" "$tmp_file" >/dev/null 2>&1; then
		rm "$tmp_file"
		echo "  Skipped $user_file (preamble already up to date)"
	else
		cp "$user_file" "${user_file}.bak"
		mv "$tmp_file" "$user_file"
		echo "  Updated preamble in $user_file (backup: ${user_file}.bak)"
	fi
}

# ── Main ──────────────────────────────────────────────────────────────────────

for key in accounts sessions defaults; do
	sync_file "$key"
done
