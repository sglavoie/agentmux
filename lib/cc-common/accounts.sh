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
				warn "unknown color '$color' for account '$name', falling back to gray."
				color="gray"
			fi
			_ACCOUNT_NAMES+=("$name")
			_ACCOUNT_DIRS+=("$config_dir")
			_ACCOUNT_COLORS+=("$color")
		done < <({
			local _parse_stderr _parse_rc=0
			_parse_stderr="$(python3 "$_CC_COMMON_DIR/toml-helper.py" read-accounts "$ACCOUNTS_CONFIG" 2>&1 1>&3)" 3>&1 || _parse_rc=$?
			if [[ "$_parse_rc" -ne 0 ]]; then
				err "failed to parse accounts config '$ACCOUNTS_CONFIG'."
				[[ -n "$_parse_stderr" ]] && echo "  Detail: $_parse_stderr" >&2
				echo "  Run 'ccm config validate' for full diagnostics." >&2
			fi
		})

		if [[ ${#_ACCOUNT_NAMES[@]} -eq 0 ]]; then
			warn "accounts config exists but produced no accounts. Check $ACCOUNTS_CONFIG for errors."
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
	local i
	local names="" dirs=""
	for ((i = 0; i < ${#_ACCOUNT_NAMES[@]}; i++)); do
		[[ -n "$names" ]] && names+=$'\n'
		names+="${_ACCOUNT_NAMES[$i]}"
		[[ -n "$dirs" ]] && dirs+=$'\n'
		dirs+="${_ACCOUNT_DIRS[$i]}"
	done
	jq -nc --arg names "$names" --arg dirs "$dirs" \
		'($names | split("\n")) as $n |
		 ($dirs | split("\n")) as $d |
		 [range($n | length) | {name: $n[.], dir: $d[.]}]'
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

# Resolve an account name from the uppercase initial of a tab title.
# Outputs the matching account name, or "unknown" if no match.
_resolve_account_from_initial() {
	local initial="$1"
	local ai
	for ((ai = 0; ai < ${#_ACCOUNT_NAMES[@]}; ai++)); do
		local name_initial="${_ACCOUNT_NAMES[$ai]:0:1}"
		name_initial="$(echo "$name_initial" | tr '[:lower:]' '[:upper:]')"
		if [[ "$name_initial" == "$initial" ]]; then
			echo "${_ACCOUNT_NAMES[$ai]}"
			return 0
		fi
	done
	echo "unknown"
	return 1
}

claude_config_dir_for_account() {
	local account="$1"
	local config_dir
	config_dir="$(_account_dir "$account")" || {
		err "Unknown account '$account'. Available: $(_all_account_names)."
		return 1
	}
	echo "$config_dir"
}
