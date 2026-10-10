# shellcheck shell=sh
# Sourced by install-app.sh and uninstall-app.sh: what both need to know about
# a bundle, the running app, and the private tmux server.

bundle_id=io.github.sglavoie.agentmux

require_tool() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "required tool not found: $1" >&2
		exit 1
	fi
}

# check_bundle APP: the bundle carries a signed amx and a tmux runtime whose
# files match their recorded checksums, and its seal is intact. Anything else
# is refused before a file is touched.
check_bundle() {
	if [ ! -x "$1/Contents/Resources/bin/amx" ]; then
		echo "$1 has no bundled amx; build it with \`just mac-app Release\`" >&2
		return 1
	fi
	if [ ! -x "$1/Contents/Resources/tmux/bin/tmux" ] || [ ! -f "$1/Contents/Resources/tmux/SHA256SUMS" ]; then
		echo "$1 has no bundled tmux runtime; build it with \`just mac-app Release\`" >&2
		return 1
	fi
	if ! (cd "$1/Contents/Resources/tmux" && shasum -a 256 -c SHA256SUMS >/dev/null 2>&1); then
		echo "the tmux runtime in $1 does not match its checksums" >&2
		return 1
	fi
	if ! codesign --verify --strict --deep "$1"; then
		echo "$1 does not pass signature verification" >&2
		return 1
	fi
}

# backend_status AMX: `amx backend status --json` from that amx, told where
# its own bundle's runtime is. Empty when the amx cannot say.
backend_status() {
	runtime="$(dirname -- "$(dirname -- "$1")")/tmux"
	AMX_TMUX_RUNTIME="$runtime" "$1" backend status --json 2>/dev/null || true
}

# status_field JSON KEYPATH: one value of the report, empty when absent.
status_field() {
	printf '%s' "$1" | plutil -extract "data.$2" raw -o - - 2>/dev/null || true
}

app_running() {
	pgrep -f "/Agent Mux.app/Contents/MacOS/Agent Mux" >/dev/null 2>&1
}

# quit_app: asked politely through Apple Events, then waited for, so nothing is
# ever written under a live process. Any copy is quit, not only the installed
# one: the app is single-instance, so a copy left running elsewhere would make
# a new one exit.
quit_app() {
	app_running || return 0
	echo "Quitting the running Agent Mux.app..."
	osascript -e "tell application id \"$bundle_id\" to quit" >/dev/null 2>&1 || true
	i=0
	while app_running; do
		i=$((i + 1))
		if [ "$i" -ge 50 ]; then
			echo "Agent Mux.app did not quit within 10s; quit it from its menu and rerun" >&2
			return 1
		fi
		sleep 0.2
	done
}
