#!/usr/bin/env sh
#
# Build Agent Mux.app: generate macos/AgentMux.xcodeproj (generate-project.sh),
# build it, copy the product to macos/build/Agent Mux.app, bundle amx, and sign
# (sign.sh).
#
# Usage: build-app.sh [Debug|Release]   (default Debug)
#
# Environment:
#   AMX_BINARY              amx to bundle at Contents/Resources/bin/amx;
#                           required for Release (`just mac-app Release` builds
#                           it with `just build`), optional for Debug, which
#                           otherwise resolves ~/.local/bin/amx then PATH at
#                           run time
#   AMX_MACOS_CACHE         cache root (default macos/.cache, gitignored)
#   AMX_CODESIGN_IDENTITY   see sign.sh (default Apple Development, else ad-hoc)

set -eu

require_tool() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "required tool not found: $1" >&2
		exit 1
	fi
}

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
macos_dir=$(dirname -- "$script_dir")

configuration="${1:-Debug}"
case "$configuration" in
Debug | Release) ;;
*)
	echo "configuration must be Debug or Release, not $configuration" >&2
	exit 1
	;;
esac

amx_binary="${AMX_BINARY:-}"
if [ "$configuration" = Release ] && [ -z "$amx_binary" ]; then
	echo "a Release build bundles amx: set AMX_BINARY, or run \`just mac-app Release\`" >&2
	exit 1
fi
if [ -n "$amx_binary" ] && [ ! -x "$amx_binary" ]; then
	echo "AMX_BINARY is not an executable file: $amx_binary" >&2
	exit 1
fi

"$script_dir/generate-project.sh"
require_tool ditto

# --- Build -----------------------------------------------------------------------

derived_data="$macos_dir/DerivedData"
xcodebuild build \
	-project "$macos_dir/AgentMux.xcodeproj" \
	-scheme AgentMux \
	-configuration "$configuration" \
	-destination "platform=macOS,arch=$(uname -m)" \
	-derivedDataPath "$derived_data" \
	-quiet

product="$derived_data/Build/Products/$configuration/Agent Mux.app"
if [ ! -f "$product/Contents/Info.plist" ]; then
	echo "xcodebuild reported success but $product is missing" >&2
	exit 1
fi

out="$macos_dir/build/Agent Mux.app"
mkdir -p "$macos_dir/build"
rm -rf "$out"
ditto "$product" "$out"

# --- Bundle amx and sign ---------------------------------------------------------
#
# Copying amx in breaks the seal xcodebuild wrote, so the bundle is always
# re-signed afterwards, inside out. The copy keeps the name `amx`: the dashboard
# singleton check recognizes a dashboard by an argv[0] basename of exactly amx.

if [ -n "$amx_binary" ]; then
	mkdir -p "$out/Contents/Resources/bin"
	cp "$amx_binary" "$out/Contents/Resources/bin/amx"
	chmod 755 "$out/Contents/Resources/bin/amx"
fi
"$script_dir/sign.sh" "$out"

echo "Built $out ($configuration)"
