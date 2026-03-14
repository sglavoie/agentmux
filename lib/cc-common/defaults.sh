#!/usr/bin/env bash

[[ -n "${_CC_COMMON_DEFAULTS_LOADED:-}" ]] && return 0
_CC_COMMON_DEFAULTS_LOADED=1

_DEFAULT_CLAUDE_FLAGS=""
_DEFAULT_PROJECT_WINDOW=""
_DEFAULT_SORT_SESSIONS=""
_DEFAULT_DASHBOARD_REFRESH=""
_DEFAULT_NOTIFY_ERROR=""
_DEFAULT_NOTIFY_PERMISSION=""
_DEFAULT_NOTIFY_STOP=""
_DEFAULT_IDLE_ALERT_MINUTES=""
_DEFAULT_NOTIFY_IDLE=""
_DEFAULT_NOTIFY_MIN_SECS=""
_DEFAULT_NOTIFY_IGNORE_PATTERNS=""
_DEFAULT_IDLE_POLL_INTERVAL=""
_DEFAULT_DIM_AFTER_MINUTES=""

load_defaults() {
	_DEFAULT_CLAUDE_FLAGS=""
	_DEFAULT_PROJECT_WINDOW=""
	_DEFAULT_SORT_SESSIONS=""
	_DEFAULT_DASHBOARD_REFRESH=""
	_DEFAULT_NOTIFY_ERROR=""
	_DEFAULT_NOTIFY_PERMISSION=""
	_DEFAULT_NOTIFY_STOP=""
	_DEFAULT_IDLE_ALERT_MINUTES=""
	_DEFAULT_NOTIFY_IDLE=""
	_DEFAULT_NOTIFY_MIN_SECS=""
	_DEFAULT_NOTIFY_IGNORE_PATTERNS=""
	_DEFAULT_IDLE_POLL_INTERVAL=""
	_DEFAULT_DIM_AFTER_MINUTES=""

	[[ -f "$DEFAULTS_CONFIG" ]] || return 0

	# Use a cached sourceable file to avoid python3 overhead on every call.
	local cache="${_CCM_TMPDIR}/ccm-defaults-cache-$(id -u).sh"
	local config_mtime
	config_mtime="$(_file_mtime "$DEFAULTS_CONFIG" || echo 0)"

	if [[ -f "$cache" ]]; then
		local cache_mtime
		cache_mtime="$(head -1 "$cache")"
		if [[ "$cache_mtime" == "# mtime=$config_mtime" ]]; then
			# shellcheck disable=SC1090
			source "$cache"
			return 0
		fi
	fi

	# Regenerate cache from TOML via the Python helper.
	_require_python3
	local key value cache_tmp="${cache}.tmp"
	trap 'rm -f "$cache_tmp"' RETURN
	{
		echo "# mtime=$config_mtime"
		while IFS=$'\t' read -r key value; do
			[[ -z "$key" ]] && continue
			_assign_default "$key" "$value"
			# Emit the assignment for the cache file.
			printf '_DEFAULT_%s=%q\n' "$(echo "$key" | tr '[:lower:]' '[:upper:]')" "$value"
		done < <(python3 "$_CC_COMMON_DIR/toml-helper.py" read-defaults "$DEFAULTS_CONFIG" 2>/dev/null || {
			echo "Warning: failed to parse defaults config '$DEFAULTS_CONFIG'" >&2
		})
	} >"$cache_tmp" || {
		echo "Error: failed to write defaults cache" >&2
		return 1
	}
	mv "$cache_tmp" "$cache" || {
		echo "Error: failed to update defaults cache" >&2
		return 1
	}
}

_is_positive_int() {
	[[ "$1" =~ ^[0-9]+$ ]] && [[ "$1" -gt 0 ]]
}

_assign_default() {
	local key="$1" value="$2"
	case "$key" in
	claude_flags) _DEFAULT_CLAUDE_FLAGS="$value" ;;
	project_window) _DEFAULT_PROJECT_WINDOW="$value" ;;
	sort_sessions) _DEFAULT_SORT_SESSIONS="$value" ;;
	dashboard_refresh)
		if _is_positive_int "$value"; then
			_DEFAULT_DASHBOARD_REFRESH="$value"
		else
			echo "Warning: invalid dashboard_refresh '$value', using default" >&2
		fi
		;;
	notify_error) _DEFAULT_NOTIFY_ERROR="$value" ;;
	notify_permission) _DEFAULT_NOTIFY_PERMISSION="$value" ;;
	notify_stop) _DEFAULT_NOTIFY_STOP="$value" ;;
	idle_alert_minutes)
		if _is_positive_int "$value"; then
			_DEFAULT_IDLE_ALERT_MINUTES="$value"
		else
			echo "Warning: invalid idle_alert_minutes '$value', using default" >&2
		fi
		;;
	notify_idle) _DEFAULT_NOTIFY_IDLE="$value" ;;
	notify_min_secs)
		if _is_positive_int "$value"; then
			_DEFAULT_NOTIFY_MIN_SECS="$value"
		else
			echo "Warning: invalid notify_min_secs '$value', using default" >&2
		fi
		;;
	notify_ignore_patterns) _DEFAULT_NOTIFY_IGNORE_PATTERNS="$value" ;;
	idle_poll_interval)
		if _is_positive_int "$value"; then
			_DEFAULT_IDLE_POLL_INTERVAL="$value"
		else
			echo "Warning: invalid idle_poll_interval '$value', using default" >&2
		fi
		;;
	dim_after_minutes)
		if _is_positive_int "$value"; then
			_DEFAULT_DIM_AFTER_MINUTES="$value"
		else
			echo "Warning: invalid dim_after_minutes '$value', using default" >&2
		fi
		;;
	esac
}

resolve_claude_flags() {
	local override="${1:-}"
	if [[ -z "$override" ]]; then
		echo "$_DEFAULT_CLAUDE_FLAGS"
	elif [[ "$override" == "-" ]]; then
		echo ""
	else
		echo "$override"
	fi
}

resolve_project_window() {
	local override="${1:-}"
	if [[ -z "$override" ]]; then
		echo "$_DEFAULT_PROJECT_WINDOW"
	elif [[ "$override" == "-" ]]; then
		echo ""
	else
		echo "$override"
	fi
}
