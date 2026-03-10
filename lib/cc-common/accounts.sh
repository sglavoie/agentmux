#!/usr/bin/env bash

[[ -n "${_CC_COMMON_ACCOUNTS_LOADED:-}" ]] && return 0
_CC_COMMON_ACCOUNTS_LOADED=1

_ACCOUNT_NAMES=()
_ACCOUNT_DIRS=()
_ACCOUNT_COLORS=()

load_accounts() {
	_ACCOUNT_NAMES=()
	_ACCOUNT_DIRS=()
	_ACCOUNT_COLORS=()

	if [[ -f "$ACCOUNTS_CONFIG" ]]; then
		_require_python3
		while IFS=$'\t' read -r name config_dir color; do
			[[ -z "$name" ]] && continue
			config_dir="$(expand_tilde "$config_dir")"
			color="${color:-gray}"
			if ! _palette_has_color "$color"; then
				echo "Warning: unknown color '$color' for account '$name', falling back to gray." >&2
				color="gray"
			fi
			_ACCOUNT_NAMES+=("$name")
			_ACCOUNT_DIRS+=("$config_dir")
			_ACCOUNT_COLORS+=("$color")
		done < <(python3 "$_CC_COMMON_DIR/toml-helper.py" read-accounts "$ACCOUNTS_CONFIG")

		if [[ ${#_ACCOUNT_NAMES[@]} -eq 0 ]]; then
			echo "Warning: accounts config exists but produced no accounts. Check $ACCOUNTS_CONFIG for errors." >&2
		fi
	else
		_ACCOUNT_NAMES=(work personal)
		_ACCOUNT_DIRS=("$HOME/.claude" "$HOME/.claude-personal")
		_ACCOUNT_COLORS=(green blue)
		echo "Note: No accounts config found at $ACCOUNTS_CONFIG; using default work/personal accounts." >&2
	fi
}

_account_index() {
	local target="$1"
	local i
	for ((i = 0; i < ${#_ACCOUNT_NAMES[@]}; i++)); do
		if [[ "${_ACCOUNT_NAMES[$i]}" == "$target" ]]; then
			echo "$i"
			return 0
		fi
	done
	return 1
}

_account_dir() {
	local idx
	idx="$(_account_index "$1")" || return 1
	echo "${_ACCOUNT_DIRS[$idx]}"
}

_account_color() {
	local idx
	idx="$(_account_index "$1")" || return 1
	echo "${_ACCOUNT_COLORS[$idx]}"
}

_all_account_names() {
	local IFS=' '
	echo "${_ACCOUNT_NAMES[*]}"
}

build_accounts_json() {
	local json="["
	local first=true
	local i
	for ((i = 0; i < ${#_ACCOUNT_NAMES[@]}; i++)); do
		[[ "$first" == "true" ]] || json+=","
		first=false
		json+="$(printf '{"name":%s,"dir":%s}' \
			"$(echo "${_ACCOUNT_NAMES[$i]}" | jq -Rs '.[:-1]')" \
			"$(echo "${_ACCOUNT_DIRS[$i]}" | jq -Rs '.[:-1]')")"
	done
	json+="]"
	echo "$json"
}

default_account_name() {
	local i
	for ((i = 0; i < ${#_ACCOUNT_NAMES[@]}; i++)); do
		local dir="${_ACCOUNT_DIRS[$i]}"
		if [[ -z "$dir" || "$dir" == "$HOME/.claude" ]]; then
			echo "${_ACCOUNT_NAMES[$i]}"
			return
		fi
	done
	echo "work"
}

claude_config_dir_for_account() {
	local account="$1"
	local config_dir
	config_dir="$(_account_dir "$account")" || {
		echo "Error: Unknown account '$account'. Available: $(_all_account_names)." >&2
		return 1
	}
	echo "$config_dir"
}
