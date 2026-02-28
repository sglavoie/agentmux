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

	[[ -f "$DEFAULTS_CONFIG" ]] || return 0

	while IFS= read -r line || [[ -n "$line" ]]; do
		[[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
		local key value
		IFS='=' read -r key value <<<"$line"
		key="$(_trim "$key")"
		value="$(_trim "$value")"
		case "$key" in
		claude_flags) _DEFAULT_CLAUDE_FLAGS="$value" ;;
		project_window) _DEFAULT_PROJECT_WINDOW="$value" ;;
		sort_sessions) _DEFAULT_SORT_SESSIONS="$value" ;;
		dashboard_refresh) _DEFAULT_DASHBOARD_REFRESH="$value" ;;
		notify_error) _DEFAULT_NOTIFY_ERROR="$value" ;;
		notify_permission) _DEFAULT_NOTIFY_PERMISSION="$value" ;;
		notify_stop) _DEFAULT_NOTIFY_STOP="$value" ;;
		idle_alert_minutes) _DEFAULT_IDLE_ALERT_MINUTES="$value" ;;
		notify_idle) _DEFAULT_NOTIFY_IDLE="$value" ;;
		notify_min_secs) _DEFAULT_NOTIFY_MIN_SECS="$value" ;;
		notify_ignore_patterns) _DEFAULT_NOTIFY_IGNORE_PATTERNS="$value" ;;
		esac
	done <"$DEFAULTS_CONFIG"
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
