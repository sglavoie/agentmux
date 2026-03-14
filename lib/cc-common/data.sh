#!/usr/bin/env bash

[[ -n "${_CC_COMMON_DATA_LOADED:-}" ]] && return 0
_CC_COMMON_DATA_LOADED=1

_CC_DATA_DIR=""
_PALETTE_JSON=""
_ICONS_JSON=""
_PALETTE_ACTIVE_FG=""
_PALETTE_INACTIVE_FG=""
_ICON_STAR=""
_ICON_FLAG=""
_ICON_BOLT=""
_ICON_STOP=""
_ICON_KEY=""
_ICON_ERROR=""

_resolve_data_dir() {
	[[ -n "$_CC_DATA_DIR" ]] && return 0

	local base_dir="${_CC_COMMON_DIR:-}"
	if [[ -z "$base_dir" ]]; then
		local script_path
		script_path="$(readlink -f "${BASH_SOURCE[0]}")"
		base_dir="$(cd "$(dirname "$script_path")" && pwd)"
	fi

	if [[ -f "$base_dir/../data/palette.json" ]]; then
		_CC_DATA_DIR="$(cd "$base_dir/../data" && pwd)"
	elif [[ -f "$base_dir/data/palette.json" ]]; then
		_CC_DATA_DIR="$(cd "$base_dir/data" && pwd)"
	elif [[ -f "$base_dir/../../data/palette.json" ]]; then
		_CC_DATA_DIR="$(cd "$base_dir/../../data" && pwd)"
	elif [[ -f "$base_dir/palette.json" ]]; then
		_CC_DATA_DIR="$base_dir"
	else
		echo "Error: Cannot find data directory (palette.json) from '$base_dir'." >&2
		return 1
	fi
}

_load_palette() {
	[[ -n "$_PALETTE_JSON" ]] && return 0
	_resolve_data_dir
	_PALETTE_JSON="$(<"$_CC_DATA_DIR/palette.json")"
	_PALETTE_ACTIVE_FG=$(printf '%s' "$_PALETTE_JSON" | jq -r '.active_fg')
	_PALETTE_INACTIVE_FG=$(printf '%s' "$_PALETTE_JSON" | jq -r '.inactive_fg')
}

_load_icons() {
	[[ -n "$_ICONS_JSON" ]] && return 0
	_resolve_data_dir
	_ICONS_JSON="$(<"$_CC_DATA_DIR/icons.json")"
	_ICON_STAR=$(printf '%s' "$_ICONS_JSON" | jq -r '.star')
	_ICON_FLAG=$(printf '%s' "$_ICONS_JSON" | jq -r '.flag')
	_ICON_BOLT=$(printf '%s' "$_ICONS_JSON" | jq -r '.states.bolt')
	_ICON_STOP=$(printf '%s' "$_ICONS_JSON" | jq -r '.states.stop')
	_ICON_KEY=$(printf '%s' "$_ICONS_JSON" | jq -r '.states.key')
	_ICON_ERROR=$(printf '%s' "$_ICONS_JSON" | jq -r '.states.error')
}

_CACHED_OVERRIDE_EMOJI_RE=""
_CACHED_OVERRIDE_EMOJI_LIST=""

_override_emoji_re() {
	if [[ -n "$_CACHED_OVERRIDE_EMOJI_RE" ]]; then
		printf '%s' "$_CACHED_OVERRIDE_EMOJI_RE"
		return 0
	fi
	_load_palette
	_CACHED_OVERRIDE_EMOJI_RE="$(printf '%s' "$_PALETTE_JSON" | jq -r '
		[.colors[].emoji] | join("|") | "(" + . + ")"
	')"
	printf '%s' "$_CACHED_OVERRIDE_EMOJI_RE"
}

_override_emoji_list() {
	if [[ -n "$_CACHED_OVERRIDE_EMOJI_LIST" ]]; then
		printf '%s' "$_CACHED_OVERRIDE_EMOJI_LIST"
		return 0
	fi
	_load_palette
	_CACHED_OVERRIDE_EMOJI_LIST="$(printf '%s' "$_PALETTE_JSON" | jq -r '
		[.colors[].emoji] | join(" ")
	')"
	printf '%s' "$_CACHED_OVERRIDE_EMOJI_LIST"
}

_palette_has_color() {
	local color="$1"
	_load_palette
	printf '%s' "$_PALETTE_JSON" | jq -e --arg c "$color" '.colors[$c]' >/dev/null 2>&1
}

_icon_strip_regex() {
	_load_icons
	local states
	states=$(printf '%s' "$_ICONS_JSON" | jq -r '
        [.state_order[] as $k | .states[$k]] | join("|")
    ')
	printf '^(%s)?(%s)?(%s)?' "$_ICON_STAR" "$_ICON_FLAG" "$states"
}
