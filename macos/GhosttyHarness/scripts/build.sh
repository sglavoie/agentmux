#!/usr/bin/env sh
#
# Build GhosttyHarness.app (Debug) into macos/.cache/ghostty-harness/build:
# vendor the pinned GhosttyKit, generate the project with Agent Mux's pinned
# XcodeGen, and run xcodebuild. Nothing it writes is tracked.
#
# Environment:
#   AMX_MACOS_CACHE         cache root (default macos/.cache, gitignored)

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
harness_dir=$(dirname -- "$script_dir")
macos_dir=$(dirname -- "$harness_dir")
cache_root="${AMX_MACOS_CACHE:-$macos_dir/.cache}"
out="$cache_root/ghostty-harness"

"$script_dir/vendor-ghosttykit.sh"

# shellcheck disable=SC1091
. "$macos_dir/XCODEGEN_VERSION"
xcodegen="$cache_root/xcodegen-$XCODEGEN_VERSION/bin/xcodegen"
if [ ! -x "$xcodegen" ]; then
	# generate-project.sh provisions the pinned XcodeGen into the cache.
	"$macos_dir/scripts/generate-project.sh"
fi
"$xcodegen" generate --quiet --spec "$harness_dir/project.yml" --project "$harness_dir"

xcodebuild build \
	-project "$harness_dir/GhosttyHarness.xcodeproj" \
	-scheme GhosttyHarness \
	-configuration Debug \
	-destination "platform=macOS,arch=$(uname -m)" \
	-derivedDataPath "$out/DerivedData" \
	SYMROOT="$out/build" \
	-quiet
echo "$out/build/Debug/GhosttyHarness.app"
