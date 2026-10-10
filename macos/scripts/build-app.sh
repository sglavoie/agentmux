#!/usr/bin/env sh
#
# Build Agent Mux.app: generate macos/AgentMux.xcodeproj (generate-project.sh),
# build it, copy the product to macos/build/Agent Mux.app, bundle amx and the
# pinned tmux runtime (build-tmux.sh), and sign (sign.sh).
#
# With AMX_MAC_TEST_HOST set, Xcode runs on that Mac instead (remote-ui.sh):
# amx and the tmux runtime built here travel with the checkout, and the signed
# product is brought back to macos/build/, so the result is in the same place
# either way. With AMX_UI_TEST_REMOTE_ONLY=1 nothing of Xcode's ever runs here.
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
#   AMX_MAC_TEST_HOST       see remote-ui.sh
#   AMX_UI_TEST_REMOTE_ONLY see remote-ui.sh

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

# The runtime is built before anything is sent anywhere, so a remote build
# finds it vendored and current.
"$script_dir/build-tmux.sh"

case "$("$script_dir/remote-ui.sh" mode)" in
remote)
	"$script_dir/remote-ui.sh" sync
	remote_command="./macos/scripts/build-app.sh $configuration"
	if [ -n "$amx_binary" ]; then
		"$script_dir/remote-ui.sh" put "$amx_binary" macos/build/.input/amx
		remote_command="AMX_BINARY=\"\$PWD/macos/build/.input/amx\" $remote_command"
	fi
	# The identity names a certificate in the keychain of the Mac that signs.
	if [ -n "${AMX_CODESIGN_IDENTITY:-}" ]; then
		remote_command="AMX_CODESIGN_IDENTITY='$AMX_CODESIGN_IDENTITY' $remote_command"
	fi
	echo "build-app: building on ${AMX_MAC_TEST_HOST}" >&2
	"$script_dir/remote-ui.sh" sh "$remote_command"
	rm -rf "$macos_dir/build/Agent Mux.app"
	"$script_dir/remote-ui.sh" get "macos/build/Agent Mux.app" "$macos_dir/build"
	codesign --verify --strict --deep "$macos_dir/build/Agent Mux.app"
	echo "Built $macos_dir/build/Agent Mux.app ($configuration, on ${AMX_MAC_TEST_HOST})"
	exit 0
	;;
local) ;;
*)
	echo "the app was not built" >&2
	exit 1
	;;
esac

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

# --- Bundle amx and tmux, and sign -----------------------------------------------
#
# Copying them in breaks the seal xcodebuild wrote, so the bundle is always
# re-signed afterwards, inside out. The copy keeps the name `amx`: the dashboard
# singleton check recognizes a dashboard by an argv[0] basename of exactly amx.
# The tmux runtime sits beside amx's directory (Resources/tmux next to
# Resources/bin), which is where a bundled amx looks for it; sign.sh records
# its checksums once it is signed.

if [ -n "$amx_binary" ]; then
	mkdir -p "$out/Contents/Resources/bin"
	cp "$amx_binary" "$out/Contents/Resources/bin/amx"
	chmod 755 "$out/Contents/Resources/bin/amx"
fi
runtime="$macos_dir/Vendor/tmux"
mkdir -p "$out/Contents/Resources/tmux"
ditto "$runtime/bin" "$out/Contents/Resources/tmux/bin"
ditto "$runtime/licenses" "$out/Contents/Resources/tmux/licenses"
cp "$runtime/VERSION" "$out/Contents/Resources/tmux/VERSION"
"$script_dir/sign.sh" "$out"

echo "Built $out ($configuration)"
