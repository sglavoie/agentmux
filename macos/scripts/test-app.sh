#!/usr/bin/env sh
#
# Run the AgentMuxTests XCTest suite: generate macos/AgentMux.xcodeproj
# (generate-project.sh), then `xcodebuild test`. The tests are hosted in the
# app, so they build it too; the app sees XCTest in its environment and starts
# no daemon work of its own.
#
# Environment:
#   AMX_MACOS_CACHE         cache root (default macos/.cache, gitignored)

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
macos_dir=$(dirname -- "$script_dir")

"$script_dir/generate-project.sh"

xcodebuild test \
	-project "$macos_dir/AgentMux.xcodeproj" \
	-scheme AgentMux \
	-destination "platform=macOS,arch=$(uname -m)" \
	-derivedDataPath "$macos_dir/DerivedData" \
	-quiet
