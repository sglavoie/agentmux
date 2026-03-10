#!/usr/bin/env bash

[[ -n "${_CCM_MAIN_LOADED:-}" ]] && return 0
_CCM_MAIN_LOADED=1

CCM_VERSION="$(cat "$REPO_DIR/VERSION" 2>/dev/null || echo "0.2.0")"

main() {
	local SOCKET_ARGS=()

	# Parse global --socket option
	if [[ "${1:-}" == "--socket" ]]; then
		if [[ -z "${2:-}" ]]; then
			echo "Error: --socket requires a SOCKET argument." >&2
			exit 1
		fi
		SOCKET_ARGS=("--socket" "$2")
		shift 2
	fi

	if [[ "${1:-}" == "--debug" ]]; then
		export CCM_DEBUG=1
		shift
	fi

	case "${1:-}" in
	launch)
		shift
		exec cc-launch ${SOCKET_ARGS[@]+"${SOCKET_ARGS[@]}"} "$@"
		;;
	status)
		shift
		exec cc-status ${SOCKET_ARGS[@]+"${SOCKET_ARGS[@]}"} "$@"
		;;
	star)
		shift
		exec cc-toggle-star "$@"
		;;
	config)
		shift
		cmd_config "$@"
		;;
	dashboard)
		exec kitten claude_dashboard.py
		;;
	switch)
		exec kitten cc_quick_switch.py
		;;
	notify)
		shift
		cmd_notify "$@"
		;;
	hooks)
		shift
		cmd_hooks "$@"
		;;
	logs)
		shift
		cmd_logs "$@"
		;;
	audit)
		shift
		exec cc-audit "$@"
		;;
	idle-watch)
		shift
		exec cc-idle-watch "$@"
		;;
	help)
		shift
		cmd_help "$@"
		;;
	--version | -v)
		echo "ccm $CCM_VERSION"
		;;
	--help | -h)
		cmd_help
		;;
	"")
		cmd_help
		;;
	*)
		echo "Error: Unknown command '$1'. Run 'ccm help' for usage." >&2
		exit 1
		;;
	esac
}
