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
	accounts) echo "${CC_MANAGER_ACCOUNTS:-$config_dir/accounts.conf}" ;;
	sessions) echo "${CC_MANAGER_CONFIG:-$config_dir/sessions.conf}" ;;
	defaults) echo "${CC_MANAGER_DEFAULTS:-$config_dir/defaults.conf}" ;;
	esac
}

# ── Per-file sync ─────────────────────────────────────────────────────────────

# Known last documentation lines for migration (before marker existed).
# Used to find the insertion point in legacy config files.
KNOWN_LAST_LINES=(
	'# Whitespace around the pipe delimiters is trimmed.'
	'# Whitespace around the = delimiter is trimmed.'
)

migrate_marker() {
	local user_file="$1"

	for pattern in "${KNOWN_LAST_LINES[@]}"; do
		if grep -qF "$pattern" "$user_file"; then
			# Insert the marker line after the known last doc line
			sed -i '' "/^$(sed 's/[.[\*^$()+?{|]/\\&/g' <<<"$pattern")$/a\\
$MARKER
" "$user_file"
			echo "  Migrated: inserted marker into $user_file"
			return 0
		fi
	done

	return 1
}

sync_file() {
	local key="$1"
	local example_file="$REPO_DIR/config/${key}.example"
	local user_file
	user_file="$(resolve_user_file "$key")"

	# Skip if user file doesn't exist
	if [[ ! -f "$user_file" ]]; then
		return
	fi

	# Skip if example file doesn't exist
	if [[ ! -f "$example_file" ]]; then
		echo "  Warning: example file not found: $example_file" >&2
		return
	fi

	# If marker not found in user file, attempt migration
	if ! grep -qF "$MARKER" "$user_file"; then
		if ! migrate_marker "$user_file"; then
			echo "  Warning: cannot sync $user_file — no marker and no known" >&2
			echo "           last documentation line found. Add the marker manually:" >&2
			echo "           $MARKER" >&2
			return
		fi
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
