#!/usr/bin/env sh
#
# Run the AgentMuxTests XCTest suite: generate macos/AgentMux.xcodeproj
# (generate-project.sh), then `xcodebuild test`. The tests are hosted in the
# app, so they build it too; the app sees XCTest in its environment and starts
# no daemon work of its own.
#
# Usage: test-app.sh [Debug|Release]   (default Debug)
#
# Release runs the same suite against the optimized app, the build that ships.
# Its products go to their own DerivedData, because `@testable import` needs
# the app compiled with testability and build-app.sh's Release must not be.
#
# The suite launches the app, so it can be sent to another Mac: with
# AMX_MAC_TEST_HOST set it runs there (remote-ui.sh) and this Mac shows no UI.
# With AMX_UI_TEST_REMOTE_ONLY=1 it never runs here, whatever goes wrong.
#
# A run that executes no test fails: a test class missing from the generated
# project would otherwise run nothing and report success.
#
# Environment:
#   AMX_MACOS_CACHE         cache root (default macos/.cache, gitignored)
#   AMX_CODESIGN_IDENTITY   see sign.sh (default Apple Development, else ad-hoc)
#   AMX_MAC_TEST_HOST       see remote-ui.sh
#   AMX_UI_TEST_REMOTE_ONLY see remote-ui.sh

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
macos_dir=$(dirname -- "$script_dir")

case "$("$script_dir/remote-ui.sh" mode)" in
remote)
	exec "$script_dir/remote-ui.sh" run macos/scripts/test-app.sh "$@"
	;;
local) ;;
*)
	echo "the hosted tests were not run" >&2
	exit 1
	;;
esac

configuration="${1:-Debug}"
case "$configuration" in
Debug)
	set -- -derivedDataPath "$macos_dir/DerivedData"
	;;
Release)
	set -- -derivedDataPath "$macos_dir/DerivedData/ReleaseTests" ENABLE_TESTABILITY=YES
	;;
*)
	echo "configuration must be Debug or Release, not $configuration" >&2
	exit 1
	;;
esac

# The test host shares the installed app's bundle identifier, so it is signed
# with the identity sign.sh uses rather than ad-hoc (signing-lib.sh).
# shellcheck disable=SC1091
. "$script_dir/signing-lib.sh"
signing_settings=$(xcode_signing_settings)
# Intentional splitting: identity is a fingerprint and team contains no spaces.
# shellcheck disable=SC2086
set -- "$@" $signing_settings

"$script_dir/generate-project.sh"

log="$macos_dir/DerivedData/test-$configuration.log"
mkdir -p "$macos_dir/DerivedData"
status=0
xcodebuild test \
	-project "$macos_dir/AgentMux.xcodeproj" \
	-scheme AgentMux \
	-configuration "$configuration" \
	-destination "platform=macOS,arch=$(uname -m)" \
	"$@" >"$log" 2>&1 || status=$?

grep -E '\.swift:[0-9]+: error|Test Case .* failed|\*\* TEST' "$log" || true
summary=$(grep -E 'Executed [0-9]+ tests?, with' "$log" | tail -1 || true)
echo "${summary:-no test summary in $log}"
echo "on $(scutil --get ComputerName 2>/dev/null || hostname); full log: $log"
if [ "$status" -ne 0 ]; then
	tail -30 "$log"
	exit "$status"
fi
case "$summary" in
*"Executed 0 tests"* | "")
	echo "no test was executed" >&2
	exit 1
	;;
esac
