#!/usr/bin/env sh
#
# Install macos/build/Agent Mux.app (built by `just mac-app Release`) into
# /Applications, as a first install or as an update over an earlier copy.
#
# Agents do not run in the app: they run in a private tmux server that
# outlives it, started from a copy of the tmux runtime kept outside the bundle
# (~/.local/share/agent-mux/runtime). So an update quits and replaces the app
# and nothing else:
#
#   - the new bundle is checked first (signature, bundled amx, tmux runtime
#     against its checksums) and its runtime is installed beside any earlier
#     one, never over it;
#   - the app is staged next to its destination and renamed into place, so the
#     installed copy is always a whole bundle;
#   - a running tmux server is never stopped, restarted, or handed another
#     executable. Its identity is read before and after, and an update that
#     finds it changed fails loudly rather than report success.
#
# Agents that were running keep running and are there when the new app opens.
#
# Environment:
#   AMX_APP_SOURCE          the bundle to install (default macos/build/Agent Mux.app)
#   AMX_APP_INSTALL_DIR     destination directory (default /Applications)
#   AMX_APP_NO_LAUNCH=1     install without launching the new copy

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
macos_dir=$(dirname -- "$script_dir")
# shellcheck disable=SC1091
. "$script_dir/app-lib.sh"

require_tool ditto
require_tool codesign
require_tool shasum
require_tool plutil
require_tool osascript
require_tool open

app="${AMX_APP_SOURCE:-$macos_dir/build/Agent Mux.app}"
install_dir="${AMX_APP_INSTALL_DIR:-/Applications}"
dest="$install_dir/Agent Mux.app"

# --- Check the new bundle, and make its runtime ready -----------------------------
#
# Asking the new amx for the backend status verifies the bundled runtime and
# installs it under the data directory, before the app that carries it is in
# place. A bundle whose runtime cannot be used is not installed at all.

check_bundle "$app"
before=$(backend_status "$app/Contents/Resources/bin/amx")
if [ "$(status_field "$before" tmux.available)" != true ]; then
	echo "the tmux runtime in $app cannot be used on this Mac:" >&2
	"$app/Contents/Resources/bin/amx" backend status >&2 || true
	exit 1
fi
server_before=$(status_field "$before" server.generation)
agents_before=$(status_field "$before" server.agents)
if [ -n "$server_before" ]; then
	echo "A session server is running with ${agents_before:-0} agent(s); it is left running."
fi

quit_app

# --- Install -------------------------------------------------------------------

mkdir -p "$install_dir"
staged="$install_dir/.Agent Mux.app.installing"
previous="$install_dir/.Agent Mux.app.previous"
rm -rf "$staged" "$previous"
ditto "$app" "$staged"
if ! codesign --verify --strict --deep "$staged"; then
	rm -rf "$staged"
	echo "the staged copy at $staged does not pass signature verification; nothing was replaced" >&2
	exit 1
fi
updated=
if [ -e "$dest" ]; then
	updated=1
	mv "$dest" "$previous"
fi
if ! mv "$staged" "$dest"; then
	[ -z "$updated" ] || mv "$previous" "$dest"
	rm -rf "$staged"
	echo "could not move the new copy into $dest; the earlier copy is back in place" >&2
	exit 1
fi
rm -rf "$previous"
lsregister=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
if [ -x "$lsregister" ]; then
	"$lsregister" -f "$dest"
fi
if [ -n "$updated" ]; then
	echo "Updated $dest"
else
	echo "Installed $dest"
fi

# --- The session server is the one that was running -----------------------------

if [ -n "$server_before" ]; then
	after=$(backend_status "$dest/Contents/Resources/bin/amx")
	server_after=$(status_field "$after" server.generation)
	if [ "$server_after" != "$server_before" ]; then
		echo "the session server changed during the update (was $server_before, now ${server_after:-not running});" >&2
		echo "agents that were running may have been lost. This is a bug: please report it." >&2
		exit 1
	fi
	echo "The session server is unchanged ($server_after); $(status_field "$after" server.agents) agent(s) running."
fi

if [ "${AMX_APP_NO_LAUNCH:-0}" != 1 ]; then
	open "$dest"
	echo "Launched $dest"
fi
