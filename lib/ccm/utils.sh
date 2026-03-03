#!/usr/bin/env bash

[[ -n "${_CCM_UTILS_LOADED:-}" ]] && return 0
_CCM_UTILS_LOADED=1

resolve_repo_dir() {
	local script_path script_dir
	script_path="$(readlink "$0" || echo "$0")"
	script_dir="$(cd "$(dirname "$script_path")" && pwd)"
	cd "$script_dir/.." && pwd
}

resolve_config_dir() {
	if [[ -n "${CC_MANAGER_CONFIG:-}" ]]; then
		dirname "$CC_MANAGER_CONFIG"
	elif [[ -n "${CC_MANAGER_ACCOUNTS:-}" ]]; then
		dirname "$CC_MANAGER_ACCOUNTS"
	else
		echo "${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager"
	fi
}

resolve_config_file() {
	local key="$1"
	local config_dir
	config_dir="$(resolve_config_dir)"

	case "$key" in
	accounts)
		echo "${CC_MANAGER_ACCOUNTS:-$config_dir/accounts.toml}"
		;;
	sessions)
		echo "${CC_MANAGER_CONFIG:-$config_dir/sessions.toml}"
		;;
	defaults)
		echo "${CC_MANAGER_DEFAULTS:-$config_dir/defaults.toml}"
		;;
	hooks)
		echo "$config_dir/hooks.json"
		;;
	*)
		echo "Error: Unknown config file '$key'. Use: accounts, sessions, defaults, hooks" >&2
		return 1
		;;
	esac
}

resolve_editor() {
	if [[ -n "${VISUAL:-}" ]]; then
		echo "$VISUAL"
	elif [[ -n "${EDITOR:-}" ]]; then
		echo "$EDITOR"
	else
		echo "vi"
	fi
}

_init_config_file() {
	local src="$1" dest="$2"
	if [[ -f "$dest" ]]; then
		echo "Skipping $dest (already exists)"
		return 1
	fi
	if [[ -f "$src" ]]; then
		cp "$src" "$dest"
		echo "Created $dest"
		return 0
	else
		echo "Warning: $src not found in repository" >&2
		return 1
	fi
}
