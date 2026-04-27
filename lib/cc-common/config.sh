#!/usr/bin/env bash

[[ -n "${_CC_COMMON_CONFIG_LOADED:-}" ]] && return 0
_CC_COMMON_CONFIG_LOADED=1

# shellcheck disable=SC2034 # used by scripts that source this library
CONFIG_FILE="${CC_MANAGER_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager/sessions.toml}"
# shellcheck disable=SC2034 # used by scripts that source this library
ACCOUNTS_CONFIG="${CC_MANAGER_ACCOUNTS:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager/accounts.toml}"
# shellcheck disable=SC2034 # used by scripts that source this library
DEFAULTS_CONFIG="${CC_MANAGER_DEFAULTS:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager/defaults.toml}"

expand_tilde() {
	# Expands ~/... to $HOME/... for the current user only.
	# Does NOT handle ~user/... syntax — cc-manager always operates as the
	# current user, so ~otheruser/ paths are unsupported. If encountered,
	# a warning is emitted and the path is returned unexpanded.
	local path="$1"
	# shellcheck disable=SC2088 # literal ~ pattern match, not expansion
	if [[ "$path" == "~/"* || "$path" == "~" ]]; then
		echo "${HOME}${path:1}"
	elif [[ "$path" == "~"* ]]; then
		warn "~user/ paths are not supported, returning as-is: $path"
		echo "$path"
	else
		echo "$path"
	fi
}
