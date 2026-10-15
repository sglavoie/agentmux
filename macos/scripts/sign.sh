#!/usr/bin/env sh
#
# Sign Agent Mux.app inside out: the bundled amx and tmux, then the app. Once
# tmux carries its final signature its runtime's checksums are written
# (Resources/tmux/SHA256SUMS), which amx verifies before it installs or runs
# the runtime, and which the app's own seal then covers. By default with
# this Mac's first Apple Development identity, else ad-hoc; either runs on this
# Mac only. A certificate gives the app a designated requirement that survives a
# rebuild, so privacy grants (notifications) are asked for once
# rather than after every install; an ad-hoc signature is its cdhash, which
# macOS treats as a new app each build. build-app.sh and test-app.sh have
# xcodebuild sign with the same identity (signing-lib.sh), so the DerivedData
# copies do not take those grants away from the installed app. With a Developer ID the signature
# carries the hardened runtime and a secure timestamp, which notarization
# requires, and a notarytool keychain profile then notarizes and staples the app.
#
# Usage: sign.sh APP
#
# Environment:
#   AMX_CODESIGN_IDENTITY   codesign identity, e.g.
#                           "Developer ID Application: Name (TEAMID)";
#                           unset picks the first Apple Development
#                           identity, else ad-hoc; "-" forces ad-hoc
#   AMX_NOTARY_PROFILE      `xcrun notarytool store-credentials` profile name;
#                           requires a Developer ID identity

set -eu

require_tool() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "required tool not found: $1" >&2
		exit 1
	fi
}

if [ "$#" -ne 1 ] || [ ! -d "$1/Contents" ]; then
	echo "usage: sign.sh APP" >&2
	exit 1
fi
app=$1

require_tool codesign

# shellcheck disable=SC1091
. "$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)/signing-lib.sh"

notary_profile="${AMX_NOTARY_PROFILE:-}"
identity=$(signing_identity)
if [ -n "${AMX_CODESIGN_IDENTITY:-}" ]; then
	development=
else
	development=$identity
fi

if [ "$identity" = - ] || [ -n "$development" ]; then
	if [ -n "$notary_profile" ]; then
		echo "AMX_NOTARY_PROFILE needs a Developer ID AMX_CODESIGN_IDENTITY" >&2
		exit 1
	fi
	set -- --force --sign "$identity"
else
	set -- --force --sign "$identity" --options runtime --timestamp
fi

bundled="$app/Contents/Resources/bin/amx"
if [ -f "$bundled" ]; then
	codesign "$@" --identifier io.github.sglavoie.agentmux.cli "$bundled"
fi
runtime="$app/Contents/Resources/tmux"
if [ -f "$runtime/bin/tmux" ]; then
	require_tool shasum
	codesign "$@" --identifier io.github.sglavoie.agentmux.tmux "$runtime/bin/tmux"
	sums=$(mktemp)
	(
		cd "$runtime"
		rm -f SHA256SUMS
		find . -type f | sed 's|^\./||' | LC_ALL=C sort | while IFS= read -r file; do
			shasum -a 256 "$file"
		done
	) >"$sums"
	cp "$sums" "$runtime/SHA256SUMS"
	chmod 644 "$runtime/SHA256SUMS"
	rm -f "$sums"
fi
codesign "$@" "$app"
codesign --verify --strict --deep --verbose=1 "$app"

if [ "$identity" = - ]; then
	echo "Signed $app ad-hoc"
	exit 0
fi
echo "Signed $app as $identity"

if [ -z "$notary_profile" ]; then
	exit 0
fi

require_tool xcrun
require_tool ditto
archive=$(mktemp -d)/AgentMux.zip
trap 'rm -rf "$(dirname -- "$archive")"' EXIT HUP INT TERM
ditto -c -k --keepParent "$app" "$archive"
echo "Submitting $app for notarization..."
xcrun notarytool submit "$archive" --keychain-profile "$notary_profile" --wait
xcrun stapler staple "$app"
xcrun stapler validate "$app"
echo "Notarized and stapled $app"
