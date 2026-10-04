#!/usr/bin/env sh
#
# Install macos/build/Agent Mux.app (built by `just mac-app Release`) into
# /Applications, replacing an earlier copy. A running copy is quit first and
# the installed one is launched afterwards: the app is a menu bar companion that
# holds no state of its own, so replacing it under a live process loses nothing
# a relaunch does not restore.
#
# Environment:
#   AMX_APP_INSTALL_DIR     destination directory (default /Applications)
#   AMX_APP_NO_LAUNCH=1     install without launching the new copy

set -eu

require_tool() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "required tool not found: $1" >&2
		exit 1
	fi
}

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
macos_dir=$(dirname -- "$script_dir")

require_tool ditto
require_tool codesign
require_tool osascript
require_tool open

app="$macos_dir/build/Agent Mux.app"
if [ ! -x "$app/Contents/Resources/bin/amx" ]; then
	echo "$app has no bundled amx; build it with \`just mac-app Release\`" >&2
	exit 1
fi
codesign --verify --strict "$app"

install_dir="${AMX_APP_INSTALL_DIR:-/Applications}"
dest="$install_dir/Agent Mux.app"
bundle_id=io.github.sglavoie.agentmux

# --- Quit a running copy ---------------------------------------------------------
#
# Asked politely through Apple Events, then waited for, so ditto never writes
# under a live process. Any copy is quit, not only the one at $dest: the app is
# single-instance, so a copy left running elsewhere would make the new one exit.

running() {
	pgrep -f "/Agent Mux.app/Contents/MacOS/Agent Mux" >/dev/null 2>&1
}

if running; then
	echo "Quitting the running Agent Mux.app..."
	osascript -e "tell application id \"$bundle_id\" to quit" >/dev/null 2>&1 || true
	i=0
	while running; do
		i=$((i + 1))
		if [ "$i" -ge 50 ]; then
			echo "Agent Mux.app did not quit within 10s; quit it from its menu and rerun" >&2
			exit 1
		fi
		sleep 0.2
	done
fi

# --- Install -------------------------------------------------------------------

mkdir -p "$install_dir"
rm -rf "$dest"
ditto "$app" "$dest"
lsregister=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
if [ -x "$lsregister" ]; then
	"$lsregister" -f "$dest"
fi
echo "Installed $dest"

if [ "${AMX_APP_NO_LAUNCH:-0}" != 1 ]; then
	open "$dest"
	echo "Launched $dest"
fi
