#!/usr/bin/env bash

[[ -n "${_CCM_NOTIFY_LOADED:-}" ]] && return 0
_CCM_NOTIFY_LOADED=1

cmd_notify() {
	local title="cc-manager"
	local subtitle="Test notification"
	local message="If you see this, notifications are working."
	local timeout=5

	while [[ $# -gt 0 ]]; do
		case "$1" in
		--title)
			title="${2:?--title requires a value}"
			shift 2
			;;
		--subtitle)
			subtitle="${2:?--subtitle requires a value}"
			shift 2
			;;
		--message)
			message="${2:?--message requires a value}"
			shift 2
			;;
		--timeout)
			timeout="${2:?--timeout requires a value}"
			shift 2
			;;
		*)
			err "Unknown flag '$1'. Run 'ccm help notify' for usage."
			return 1
			;;
		esac
	done

	if ! command -v alerter >/dev/null 2>&1; then
		err "alerter is not installed."
		echo "Install with: brew install vjeantet/tap/alerter" >&2
		return 1
	fi

	echo "Notification sent (waiting for dismissal or timeout)..."
	alerter --title "$title" --subtitle "$subtitle" \
		--message "$message" --timeout "$timeout" \
		>/dev/null 2>&1
}
