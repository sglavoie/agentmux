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
# Environment:
#   AMX_MACOS_CACHE         cache root (default macos/.cache, gitignored)

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
macos_dir=$(dirname -- "$script_dir")

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

"$script_dir/generate-project.sh"

xcodebuild test \
	-project "$macos_dir/AgentMux.xcodeproj" \
	-scheme AgentMux \
	-configuration "$configuration" \
	-destination "platform=macOS,arch=$(uname -m)" \
	-quiet \
	"$@"
