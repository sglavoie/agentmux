#!/usr/bin/env bash

[[ -n "${_CC_COMMON_CONFIG_LOADED:-}" ]] && return 0
_CC_COMMON_CONFIG_LOADED=1

# shellcheck disable=SC2034 # used by scripts that source this library
CONFIG_FILE="${CC_MANAGER_CONFIG:-$HOME/.config/cc-manager/sessions.toml}"
# shellcheck disable=SC2034 # used by scripts that source this library
ACCOUNTS_CONFIG="${CC_MANAGER_ACCOUNTS:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager/accounts.toml}"
# shellcheck disable=SC2034 # used by scripts that source this library
DEFAULTS_CONFIG="${CC_MANAGER_DEFAULTS:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager/defaults.toml}"

expand_tilde() {
	local path="$1"
	if [[ "$path" == "~"* ]]; then
		echo "${HOME}${path:1}"
	else
		echo "$path"
	fi
}
