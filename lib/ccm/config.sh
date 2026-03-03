#!/usr/bin/env bash

[[ -n "${_CCM_CONFIG_LOADED:-}" ]] && return 0
_CCM_CONFIG_LOADED=1

cmd_config_edit() {
	local editor
	editor="$(resolve_editor)"

	if [[ $# -eq 0 ]]; then
		local f1 f2 f3 f4
		f1="$(resolve_config_file accounts)"
		f2="$(resolve_config_file sessions)"
		f3="$(resolve_config_file defaults)"
		f4="$(resolve_config_file hooks)"
		# Unquoted $editor to support compound values like "code --wait"
		$editor "$f1" "$f2" "$f3" "$f4"
	else
		local f
		f="$(resolve_config_file "$1")"
		if [[ ! -f "$f" ]]; then
			echo "Warning: $f does not exist yet. Run 'ccm config init' to create it." >&2
		fi
		$editor "$f"
	fi
}

cmd_config_show() {
	if [[ $# -eq 0 ]]; then
		local f
		for key in accounts sessions defaults hooks; do
			f="$(resolve_config_file "$key")"
			echo "==> $f <=="
			if [[ -f "$f" ]]; then
				cat "$f"
			else
				echo "(file not found)"
			fi
			echo
		done
	else
		local f
		f="$(resolve_config_file "$1")"
		if [[ -f "$f" ]]; then
			cat "$f"
		else
			echo "Error: $f not found. Run 'ccm config init' to create default configs." >&2
			return 1
		fi
	fi
}

cmd_config_path() {
	if [[ $# -eq 0 ]]; then
		resolve_config_file accounts
		resolve_config_file sessions
		resolve_config_file defaults
		resolve_config_file hooks
	else
		resolve_config_file "$1"
	fi
}

cmd_config_init() {
	local config_dir
	config_dir="$(resolve_config_dir)"
	mkdir -p "$config_dir"

	local copied=0

	_init_config_file "$REPO_DIR/config/accounts.example.toml" "$config_dir/accounts.toml" && copied=$((copied + 1))
	_init_config_file "$REPO_DIR/config/sessions.example.toml" "$config_dir/sessions.toml" && copied=$((copied + 1))
	_init_config_file "$REPO_DIR/config/defaults.example.toml" "$config_dir/defaults.toml" && copied=$((copied + 1))
	_init_config_file "$REPO_DIR/config/hooks.example.json" "$config_dir/hooks.json" && copied=$((copied + 1))

	if [[ $copied -eq 0 ]]; then
		echo "All config files already exist."
	fi
}

cmd_config() {
	case "${1:-}" in
	edit)
		shift
		cmd_config_edit "$@"
		;;
	show)
		shift
		cmd_config_show "$@"
		;;
	path)
		shift
		cmd_config_path "$@"
		;;
	init)
		cmd_config_init
		;;
	sync)
		bash "$REPO_DIR/lib/sync-preambles.sh" "$REPO_DIR"
		;;
	"")
		echo "Usage: ccm config <edit|show|path|init|sync> [FILE]" >&2
		echo "FILE: accounts, sessions, defaults, hooks" >&2
		return 1
		;;
	*)
		echo "Error: Unknown config subcommand '$1'. Use: edit, show, path, init, sync" >&2
		return 1
		;;
	esac
}
