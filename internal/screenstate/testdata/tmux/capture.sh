#!/bin/sh
# Takes the real tmux captures in this directory. Never edit a capture by hand:
# re-take it with this script against a live harness pane.
#
#   capture.sh start NAME COLS ROWS CMD...   start CMD in a fresh session NAME
#   capture.sh keys  NAME KEY...             tmux send-keys to NAME
#   capture.sh resize NAME COLS ROWS         resize NAME's window
#   capture.sh grab  NAME FIXTURE HARNESS VERSION
#                                            write FIXTURE.txt (capture-pane -p),
#                                            FIXTURE.ansi (capture-pane -p -e) and
#                                            FIXTURE.json (pane geometry/modes)
#   capture.sh stop
#
# The server is private (-S under AMX_CAPTURE_DIR) and started with the exact
# production config (internal/tmux.Config, extracted from server.go), so the
# captures are what the tmux backend's Capture returns. Agents run with Kitty,
# Claude Code, ASL, and AgentMux variables stripped so their hooks cannot report
# into a live Kitty window or daemon.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
dir=${AMX_CAPTURE_DIR:-/tmp/amx-capture-$(id -u)}
mkdir -p "$dir"
chmod 700 "$dir"
sock="$dir/tmux.sock"
conf="$dir/tmux.conf"
awk '/^const Config = `/{f=1; sub(/^const Config = `/,"")} f&&/^`$/{exit} f' \
	"$here/../../../tmux/server.go" >"$conf"

t() { tmux -S "$sock" -f "$conf" "$@"; }

cmd=$1
shift
case $cmd in
start)
	name=$1 cols=$2 rows=$3
	shift 3
	envs=$(env | grep -oE '^(KITTY_[A-Za-z0-9_]*|CLAUDECODE|CLAUDE_CODE_[A-Za-z0-9_]*|ASL_[A-Za-z0-9_]*|AMX_[A-Za-z0-9_]*|TMUX|TMUX_PANE)=' | tr -d =)
	unset_args=""
	for v in $envs; do unset_args="$unset_args -u $v"; done
	# shellcheck disable=SC2086
	env $unset_args TERM=xterm-256color tmux -S "$sock" -f "$conf" \
		new-session -d -s "$name" -x "$cols" -y "$rows" -c "${AMX_CAPTURE_CWD:-$PWD}" "$@"
	;;
keys)
	name=$1
	shift
	t send-keys -t "=$name:" "$@"
	;;
resize)
	name=$1
	t resize-window -t "=$name:" -x "$2" -y "$3"
	;;
grab)
	name=$1 fixture=$2 harness=$3 version=$4
	t capture-pane -p -t "=$name:" >"$here/$fixture.txt"
	t capture-pane -p -e -t "=$name:" >"$here/$fixture.ansi"
	t display-message -p -t "=$name:" \
		'{"harness":"'"$harness"'","version":"'"$version"'","tmux":"#{version}","cols":#{pane_width},"rows":#{pane_height},"alternate_on":#{alternate_on},"cursor_x":#{cursor_x},"cursor_y":#{cursor_y},"history_size":#{history_size},"capture":"capture-pane -p"}' \
		>"$here/$fixture.json"
	;;
stop)
	t kill-server 2>/dev/null || true
	;;
*)
	echo "unknown command $cmd" >&2
	exit 2
	;;
esac
