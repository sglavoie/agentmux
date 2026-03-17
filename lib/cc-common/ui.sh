#!/usr/bin/env bash

[[ -n "${_CC_COMMON_UI_LOADED:-}" ]] && return 0
_CC_COMMON_UI_LOADED=1

HAS_GUM=false
if command -v gum >/dev/null 2>&1; then
	HAS_GUM=true
fi

# die MESSAGE [SUGGESTION]
# Print a fatal error to stderr and exit 1.
die() {
	local msg="$1"
	local suggestion="${2:-}"
	echo "Error: $msg" >&2
	[[ -n "$suggestion" ]] && echo "  $suggestion" >&2
	exit 1
}

# Gum styling constants — muted gray for section headers, dark green for
# highlighted/selected items. These match the cc-manager palette aesthetic.
# shellcheck disable=SC2034 # used by scripts that source this library
GUM_HEADER_COLOR="#aaaaaa"
# shellcheck disable=SC2034 # used by scripts that source this library
GUM_ACCENT_COLOR="#2d7d46"

# confirm PROMPT [AFFIRMATIVE] [NEGATIVE]
# Prompt the user for yes/no confirmation. Uses gum if available, else read.
# Returns 0 for yes, 1 for no.
confirm() {
	local prompt="$1"
	local affirmative="${2:-Yes}"
	local negative="${3:-Cancel}"
	if [[ "$HAS_GUM" == "true" ]]; then
		local rc=0
		gum confirm "$prompt" --affirmative "$affirmative" --negative "$negative" || rc=$?
		# 0 = yes, 1 = no, 130 = Ctrl-C — all valid gum responses.
		# Anything else means gum crashed; fall through to read fallback.
		if [[ $rc -le 130 ]]; then
			return $rc
		fi
	fi
	local ans
	read -rp "$prompt [y/N] " ans
	[[ "$ans" =~ ^[Yy] ]]
}
