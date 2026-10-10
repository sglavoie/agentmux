#!/usr/bin/env sh
#
# Remove Agent Mux.app. By default that is all it does: the app is quit and
# its bundle removed, and everything else stays exactly as it is.
#
#   - Running agents keep running. They live in a private tmux server that
#     does not need the app; `--end-sessions` ends them, and it is the only
#     thing here that does.
#   - Your data stays: config.toml, conversations and their indexes, handoffs,
#     worktrees, the runtime-session registry, and the app's own preferences.
#     `--remove-app-data` removes what belongs to the app alone (its
#     preferences, its saved window state, and the tmux runtime it installed);
#     nothing removes the data amx shares with the command line.
#
# Usage: uninstall-app.sh [--end-sessions] [--remove-app-data] [--yes]
#
#   --end-sessions      end every agent running on the private tmux server
#   --remove-app-data   remove the app's preferences and its tmux runtime;
#                       refused while agents run, unless --end-sessions
#   --yes               do not ask before ending agents or removing data
#
# Environment:
#   AMX_APP_INSTALL_DIR     where the app is installed (default /Applications)

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
# shellcheck disable=SC1091
. "$script_dir/app-lib.sh"

end_sessions=
remove_data=
assume_yes=
for arg in "$@"; do
	case "$arg" in
	--end-sessions) end_sessions=1 ;;
	--remove-app-data) remove_data=1 ;;
	--yes) assume_yes=1 ;;
	*)
		echo "usage: uninstall-app.sh [--end-sessions] [--remove-app-data] [--yes]" >&2
		exit 2
		;;
	esac
done

require_tool plutil
require_tool osascript

install_dir="${AMX_APP_INSTALL_DIR:-/Applications}"
dest="$install_dir/Agent Mux.app"
data_dir="$HOME/.local/share/agent-mux"
runtime_dir="$data_dir/runtime"

confirm() {
	[ -z "$assume_yes" ] || return 0
	if [ ! -t 0 ]; then
		echo "$1 needs confirmation: rerun with --yes" >&2
		exit 1
	fi
	printf '%s [y/N] ' "$1"
	read -r answer
	case "$answer" in
	y | Y | yes | YES) ;;
	*)
		echo "Nothing was changed."
		exit 1
		;;
	esac
}

# --- What is running ---------------------------------------------------------------
#
# Asked of the installed app's own amx while it is still there. Without one
# (the app is already gone) the server is asked directly, with the tmux it
# records having been started with: the question must not go unanswered, or
# --remove-app-data would take a runtime out from under running agents.

amx="$dest/Contents/Resources/bin/amx"
status=
if [ -x "$amx" ]; then
	status=$(backend_status "$amx")
fi
running=$(status_field "$status" server.running)
agents=$(status_field "$status" server.agents)
server_tmux=$(status_field "$status" server.binary)
server_socket=$(status_field "$status" server.socket)
if [ -z "$status" ]; then
	amx=
	tmp=${TMPDIR:-/tmp}
	server_dir="${tmp%/}/agent-mux-$(id -u)"
	server_socket="$server_dir/tmux.sock"
	server_tmux=$(cat "$server_dir/tmux.binary" 2>/dev/null || true)
	running=false
	if [ -n "$server_tmux" ] && [ -x "$server_tmux" ] && [ -S "$server_socket" ] &&
		panes=$("$server_tmux" -S "$server_socket" list-panes -a -F '#{pane_dead}' 2>/dev/null); then
		running=true
		agents=$(printf '%s\n' "$panes" | grep -c '^0$' || true)
	elif [ -S "$server_socket" ] && [ -z "$server_tmux" ]; then
		echo "There is a session server socket at $server_socket but no record of its tmux; cannot tell whether agents are running." >&2
		echo "Install the app again and rerun, or check with your own tmux: tmux -S '$server_socket' list-sessions" >&2
		exit 1
	fi
fi
agents=${agents:-0}

if [ -n "$remove_data" ] && [ "$running" = true ] && [ -z "$end_sessions" ]; then
	echo "A session server is running with $agents agent(s), started from the tmux runtime --remove-app-data would remove." >&2
	echo "End them too with --end-sessions, or uninstall without --remove-app-data." >&2
	exit 1
fi

if [ -n "$end_sessions" ] && [ "$running" = true ]; then
	confirm "End the session server and its $agents running agent(s)?"
	if [ -n "$amx" ]; then
		"$amx" backend stop --yes
	else
		"$server_tmux" -S "$server_socket" kill-server
		echo "Stopped the session server and its $agents running agent(s)."
	fi
fi
if [ -n "$remove_data" ]; then
	confirm "Remove Agent Mux.app's preferences and its tmux runtime?"
fi

# --- Remove the app ----------------------------------------------------------------

quit_app
if [ -e "$dest" ]; then
	lsregister=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
	if [ -x "$lsregister" ]; then
		"$lsregister" -u "$dest" >/dev/null 2>&1 || true
	fi
	rm -rf "$dest"
	echo "Removed $dest"
else
	echo "No app at $dest"
fi

# --- What is left --------------------------------------------------------------------

if [ -n "$remove_data" ]; then
	# The file goes too: deleting the domain can leave an empty one behind.
	defaults delete "$bundle_id" >/dev/null 2>&1 || true
	rm -f "$HOME/Library/Preferences/$bundle_id.plist"
	rm -rf "$HOME/Library/Saved Application State/$bundle_id.savedState"
	rm -rf "$runtime_dir"
	echo "Removed the app's preferences ($bundle_id) and $runtime_dir"
else
	echo "Kept the app's preferences ($bundle_id) and its tmux runtime ($runtime_dir); --remove-app-data removes them."
fi

if [ "$running" = true ] && [ -z "$end_sessions" ]; then
	echo "Left running: the session server and its $agents agent(s)."
	echo "  Reach them again by installing the app, or with any amx: amx attach <runtime-session-id>"
	if [ -n "$server_tmux" ] && [ -n "$server_socket" ]; then
		echo "  End them without amx: '$server_tmux' -S '$server_socket' kill-server"
	fi
fi
echo "Kept: your configuration (~/.config/agent-mux) and data ($data_dir): conversations' indexes, handoffs, worktrees, and the session registry."
