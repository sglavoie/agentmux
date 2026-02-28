#!/usr/bin/env bash

[[ -n "${_CC_COMMON_ANSI_LOADED:-}" ]] && return 0
_CC_COMMON_ANSI_LOADED=1

ansi_fg_for_color() {
	local color="$1"
	_load_palette
	printf '\033[38;2;%sm' "$(printf '%s' "$_PALETTE_JSON" | jq -r --arg c "$color" '
        (.colors[$c] // .colors["gray"]).text_fg | map(tostring) | join(";")
    ')"
}

ansi_fg_for_account() {
	local color
	color="$(_account_color "$1" 2>/dev/null)" || color="gray"
	ansi_fg_for_color "$color"
}

shorten_path() {
	local path="$1"
	if [[ "$path" == "$HOME"* ]]; then
		echo "~${path#"$HOME"}"
	else
		echo "$path"
	fi
}

repeat_char() {
	local ch="$1" count="$2"
	local i
	for ((i = 0; i < count; i++)); do
		printf '%s' "$ch"
	done
}

extra_bytes() {
	local str="$1"
	local byte_len char_len
	byte_len=$(printf '%s' "$str" | LC_ALL=C wc -c)
	char_len=${#str}
	echo $((byte_len - char_len))
}

init_color() {
	# shellcheck disable=SC2034 # used by scripts that source this library
	USE_COLOR=false
	BOLD="" DIM="" RESET=""
	if [[ -t 1 ]] && [[ -z "${NO_COLOR:-}" ]] && [[ "${TERM:-}" != "dumb" ]]; then
		# shellcheck disable=SC2034 # used by scripts that source this library
		USE_COLOR=true
		# shellcheck disable=SC2034 # used by scripts that source this library
		BOLD=$'\033[1m'
		# shellcheck disable=SC2034 # used by scripts that source this library
		DIM=$'\033[2m'
		# shellcheck disable=SC2034 # used by scripts that source this library
		RESET=$'\033[0m'
	fi
}
