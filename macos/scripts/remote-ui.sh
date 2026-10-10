#!/usr/bin/env sh
#
# Run something that shows UI on another Mac instead of this one. The hosted
# XCTest suite launches the app and acceptance drives it, and neither belongs
# on a machine somebody is working at.
#
# Usage:
#   remote-ui.sh mode            prints local, remote, or refuse (and why, on stderr)
#   remote-ui.sh sync            copies this checkout to the remote Mac
#   remote-ui.sh run PATH ARG... syncs, then runs the repo-relative PATH there
#   remote-ui.sh sh COMMAND      syncs nothing; runs a shell command in the remote copy
#   remote-ui.sh dir             prints the remote copy's path, relative to the remote home
#
# Environment:
#   AMX_MAC_TEST_HOST         user@host of the Mac that runs UI. Unset: UI runs here.
#   AMX_UI_TEST_REMOTE_ONLY   1 = never run UI here. With no host, or a host that
#                             cannot be reached or turns out to be this Mac,
#                             everything is refused rather than run locally.
#
# The remote copy is this checkout alone, in a directory of its own under the
# remote user's Library/Caches, keyed by this Mac and this checkout: nothing
# else on the remote Mac is read or written. The command runs there with both
# variables removed, so it cannot route again. ssh is given no terminal and
# takes its credentials from the caller's agent or askpass helper.

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(dirname -- "$(dirname -- "$script_dir")")

host="${AMX_MAC_TEST_HOST:-}"
only="${AMX_UI_TEST_REMOTE_ONLY:-}"

fail() {
	echo "remote-ui: $*" >&2
	exit 1
}

mode() {
	if [ -n "${AMX_UI_REMOTE_EXECUTION:-}" ]; then
		[ -z "$host" ] || fail "already running remotely, and AMX_MAC_TEST_HOST is set again: refusing to route twice"
		echo local
	elif [ -n "$host" ]; then
		echo remote
	elif [ "$only" = 1 ]; then
		echo "remote-ui: AMX_UI_TEST_REMOTE_ONLY=1 and AMX_MAC_TEST_HOST is unset: UI may not run on this Mac" >&2
		echo refuse
	else
		echo local
	fi
}

remote() {
	ssh -o StrictHostKeyChecking=yes -o ConnectTimeout=20 -o ServerAliveInterval=30 "$host" "$@" </dev/null
}

hardware_id() {
	ioreg -rd1 -c IOPlatformExpertDevice | awk -F'"' '/IOPlatformUUID/ {print $4}'
}

# The remote Mac must answer, and must not be this one: a host name that
# resolves back here would run the UI on the very machine being protected.
verify() {
	[ -n "$host" ] || fail "AMX_MAC_TEST_HOST is unset"
	here=$(hardware_id)
	[ -n "$here" ] || fail "cannot read this Mac's hardware identifier"
	there=$(remote "ioreg -rd1 -c IOPlatformExpertDevice | awk -F'\"' '/IOPlatformUUID/ {print \$4}'") ||
		fail "cannot reach $host; nothing was run"
	[ -n "$there" ] || fail "$host did not report a hardware identifier; nothing was run"
	[ "$there" != "$here" ] || fail "$host is this Mac; nothing was run"
}

remote_dir() {
	key=$(printf '%s:%s' "$(hardware_id)" "$repo_dir" | shasum -a 256 | cut -c1-12)
	echo "Library/Caches/agent-mux-remote-ui/$key/repo"
}

sync() {
	verify
	dir=$(remote_dir)
	remote "mkdir -p '$dir'"
	rsh="ssh -o StrictHostKeyChecking=yes -o ConnectTimeout=20"
	# Build products and caches stay on each side; the vendored GhosttyKit
	# travels, so the remote Mac does not build it again.
	rsync -a --delete -e "$rsh" \
		--exclude '/.git/' --exclude '/amx' \
		--exclude '/macos/DerivedData/' --exclude '/macos/build/' --exclude '/macos/.cache/' \
		"$repo_dir/" "$host:$dir/" </dev/null
	for tool in "$repo_dir"/macos/.cache/xcodegen-*; do
		[ -d "$tool" ] || continue
		remote "mkdir -p '$dir/macos/.cache'"
		rsync -a -e "$rsh" "$tool" "$host:$dir/macos/.cache/" </dev/null
	done
}

# Quotes one word for the remote shell.
quote() {
	printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

run_shell() {
	dir=$(remote_dir)
	# A login shell, for the remote user's PATH; the routing variables are
	# removed so the command runs there and nowhere else.
	remote "cd '$dir' && exec env -u AMX_MAC_TEST_HOST -u AMX_UI_TEST_REMOTE_ONLY AMX_UI_REMOTE_EXECUTION=1 /bin/zsh -lc $(quote "$1")"
}

command="${1:-}"
[ $# -gt 0 ] && shift
case "$command" in
mode)
	mode
	;;
dir)
	remote_dir
	;;
sync)
	sync
	;;
run)
	[ $# -ge 1 ] || fail "usage: remote-ui.sh run PATH ARG..."
	sync
	line=""
	for word in "$@"; do line="$line $(quote "$word")"; done
	echo "remote-ui: running$line on $host" >&2
	run_shell "./${line# }"
	;;
sh)
	[ $# -eq 1 ] || fail "usage: remote-ui.sh sh COMMAND"
	verify
	run_shell "$1"
	;;
*)
	fail "usage: remote-ui.sh mode|dir|sync|run PATH ARG...|sh COMMAND"
	;;
esac
