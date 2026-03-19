#!/usr/bin/env bash
# cc-title-icons.sh — Shared functions for tab title icon management.
# Sourced by cc-toggle-star and cc-hook-indicator.

# Source cc-common.sh for icon loading (source guard prevents double-sourcing).
_CC_TITLE_ICONS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "$_CC_TITLE_ICONS_DIR/cc-common.sh" ]]; then
	source "$_CC_TITLE_ICONS_DIR/cc-common.sh"
else
	# Follow symlink to find the real lib directory
	_real_path="$(readlink "${BASH_SOURCE[0]}" || echo "${BASH_SOURCE[0]}")"
	_real_dir="$(cd "$(dirname "$_real_path")" && pwd)"
	source "$_real_dir/cc-common.sh"
fi
_load_icons

# Parse the icon prefix of a title string.
# Outputs four lines: has_star, has_flag, has_mute (true/false each), remaining title.
# Usage: read -r has_star; read -r has_flag; read -r has_mute; read -r remaining < <(parse_icon_prefix "$title")
parse_icon_prefix() {
	local title="$1"
	local has_star=false
	local has_flag=false
	local has_mute=false
	if [[ "$title" == "${_ICON_STAR}"* ]]; then
		has_star=true
		title="${title#"$_ICON_STAR"}"
	fi
	if [[ "$title" == "${_ICON_FLAG}"* ]]; then
		has_flag=true
	fi
	if [[ "$title" == *"${_ICON_MUTE}"* ]]; then
		has_mute=true
	fi
	printf '%s\n%s\n%s\n%s\n' "$has_star" "$has_flag" "$has_mute" "$title"
}

# Remove leading ★, ❗, 🔇, and state icon prefixes from a title string.
strip_icons() {
	local title="$1"
	title="${title#"$_ICON_STAR"}"
	title="${title#"$_ICON_FLAG"}"
	title="${title#"$_ICON_MUTE"}"
	title="${title#"$_ICON_BOLT"}"
	title="${title#"$_ICON_STOP"}"
	title="${title#"$_ICON_KEY"}"
	title="${title#"$_ICON_ERROR"}"
	echo "$title"
}

# Detect which state icon is currently present (after stripping ★ and ❗).
# Outputs one of: bolt, stop, key, error, or empty string.
detect_state() {
	local title="$1"
	title="${title#"$_ICON_STAR"}"
	title="${title#"$_ICON_FLAG"}"
	title="${title#"$_ICON_MUTE"}"
	case "$title" in
	"$_ICON_BOLT"*) echo "bolt" ;;
	"$_ICON_STOP"*) echo "stop" ;;
	"$_ICON_KEY"*) echo "key" ;;
	"$_ICON_ERROR"*) echo "error" ;;
	*) echo "" ;;
	esac
}

# Reconstruct a title from a base string, star flag, flag flag, and state name.
# Order is always: ★❗🔇<state-icon> base
# state is one of: bolt, stop, key, error, or empty string.
build_title() {
	local base="$1" has_star="$2" state="$3" has_flag="${4:-}" has_mute="${5:-}"
	local result="$base"
	case "$state" in
	bolt) result="${_ICON_BOLT}$result" ;;
	stop) result="${_ICON_STOP}$result" ;;
	key) result="${_ICON_KEY}$result" ;;
	error) result="${_ICON_ERROR}$result" ;;
	esac
	if [[ "$has_mute" == "true" ]]; then
		result="${_ICON_MUTE}$result"
	fi
	if [[ "$has_flag" == "true" ]]; then
		result="${_ICON_FLAG}$result"
	fi
	if [[ "$has_star" == "true" ]]; then
		result="${_ICON_STAR}$result"
	fi
	echo "$result"
}

# Shared preamble for toggle scripts. Call after sourcing this file.
# Sets: _TGL_TAB_ID, _TGL_HAS_STAR, _TGL_HAS_FLAG, _TGL_HAS_MUTE,
#        _TGL_REMAINING, _TGL_STATE, _TGL_TO_ARGS
# Usage: toggle_preamble WINDOW_ID
toggle_preamble() {
	local win_id="$1"

	_TGL_TO_ARGS=()
	if [[ -n "${KITTY_LISTEN_ON:-}" ]]; then
		_TGL_TO_ARGS=("--to" "$KITTY_LISTEN_ON")
	fi

	local info
	info="$(get_tab_info "$win_id" ${_TGL_TO_ARGS[@]+"${_TGL_TO_ARGS[@]}"})"
	_TGL_TAB_ID="$(echo "$info" | head -n1)"
	local current_title
	current_title="$(echo "$info" | tail -n1)"

	if [[ -z "$_TGL_TAB_ID" || "$_TGL_TAB_ID" == "null" ]]; then
		err "Could not find tab for window '$win_id'."
		return 1
	fi

	local _has_star _has_flag _has_mute _remaining
	{
		read -r _has_star
		read -r _has_flag
		read -r _has_mute
		read -r _remaining
	} < <(parse_icon_prefix "$current_title")

	_TGL_HAS_STAR="$_has_star"
	_TGL_HAS_FLAG="$_has_flag"
	_TGL_HAS_MUTE="$_has_mute"
	_TGL_STATE="$(detect_state "$current_title")"
	_TGL_REMAINING="$(strip_icons "$_remaining")"
}

# Remove a leading override emoji from a title string.
# This is a separate operation from strip_icons — used only when you need the
# clean title without the override emoji (config lookup, save, clone).
strip_override_emoji() {
	local title="$1"
	local -a emojis=()
	# shellcheck disable=SC2207
	IFS=' ' emojis=($(_override_emoji_list))
	for e in "${emojis[@]}"; do
		if [[ "$title" == "${e}"* ]]; then
			echo "${title#"$e"}"
			return
		fi
	done
	echo "$title"
}

# Look up a tab by window ID via kitten @ ls.
# Outputs two lines: tab_id and current title.
# Usage: get_tab_info WINDOW_ID [--to SOCKET]
get_tab_info() {
	local win_id="$1"
	shift
	local to_args=()
	if [[ "${1:-}" == "--to" ]]; then
		to_args=("--to" "$2")
	fi
	kitten @ ${to_args[@]+"${to_args[@]}"} ls 2>/dev/null | jq -r --argjson wid "$win_id" '
        [.[] | .tabs[] | select(.windows[] | .id == $wid)] | first |
        "\(.id)\n\(.title)"
    '
}
