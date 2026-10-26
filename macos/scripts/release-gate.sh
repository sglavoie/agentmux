#!/usr/bin/env sh
#
# The two things the desktop release gate (`just mac-release-gate`,
# internal/release) cannot learn from an exit status alone: whether there is
# anything to build with, and what the bundle it built is.
#
# Usage:
#   release-gate.sh toolchain [--notarized]
#   release-gate.sh bundle [--notarized] [APP]   (default macos/build/Agent Mux.app)
#
# Both print `key: value` lines the gate copies into its evidence record.
#
# toolchain asks the Mac that will run Xcode, which is this one unless
# AMX_MAC_TEST_HOST names another (remote-ui.sh). It exits 75 when there is
# nothing to build with: no Xcode, a remote Mac that does not answer, UI
# refused here with no remote Mac named, or, with --notarized, no Developer ID
# identity named (AMX_CODESIGN_IDENTITY) and in this Mac's keychain, or no
# notary profile (AMX_NOTARY_PROFILE) that Apple accepts: signing and
# notarization happen here even when another Mac builds. The gate records that as pending: a release
# that could not be judged, which is never a release that passed.
#
# bundle shows no UI, so it runs here on the bundle `just mac-app Release`
# left: the seal, the tmux runtime against its checksums, the bundled amx
# against the one `just build` wrote (by Go build ID, since signing changes the file), and how the bundle is
# signed. An ad-hoc
# or Apple Development signature runs on the Mac that made it and nowhere
# else; --notarized refuses anything but a notarized Developer ID bundle.

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
macos_dir=$(dirname -- "$script_dir")
repo_dir=$(dirname -- "$macos_dir")

missing=75

command="${1:-}"
[ $# -gt 0 ] && shift
notarized=no
if [ "${1:-}" = --notarized ]; then
	notarized=yes
	shift
fi

# What the Mac that builds says about itself. One line each, so the same text
# can be run here or sent to the other Mac.
probe='printf "build_host: %s\n" "$(scutil --get ComputerName 2>/dev/null || hostname)"
printf "build_os_version: %s\n" "$(sw_vers -productVersion)"
printf "build_arch: %s\n" "$(uname -m)"
xcode=$(xcodebuild -version 2>/dev/null | tr "\n" " " | sed "s/ *$//") || xcode=""
printf "xcode: %s\n" "$xcode"'

toolchain() {
	mode=$("$script_dir/remote-ui.sh" mode) || mode=refuse
	echo "route: $mode"
	case "$mode" in
	remote)
		echo "remote_host: ${AMX_MAC_TEST_HOST}"
		if ! report=$("$script_dir/remote-ui.sh" sh "$probe"); then
			echo "pending: ${AMX_MAC_TEST_HOST} could not be reached, so nothing can be built or tested" >&2
			exit "$missing"
		fi
		;;
	local)
		report=$(sh -c "$probe")
		;;
	*)
		echo "pending: UI may not run on this Mac and no other Mac is named (AMX_MAC_TEST_HOST)" >&2
		exit "$missing"
		;;
	esac
	echo "$report"
	if ! echo "$report" | grep -q '^xcode: Xcode '; then
		echo "pending: no Xcode on the Mac that builds" >&2
		exit "$missing"
	fi
	# Signing and notarization happen on this Mac whichever Mac builds
	# (build-app.sh), so these are asked of this keychain.
	identities=$(security find-identity -v -p codesigning 2>/dev/null | grep -c "Developer ID Application" || true)
	echo "developer_id_identities: $identities"
	if [ "$notarized" = no ]; then
		return
	fi
	if [ "$identities" = 0 ]; then
		echo "pending: a notarized release needs a Developer ID Application identity on this Mac, and it has none" >&2
		exit "$missing"
	fi
	if [ -z "${AMX_CODESIGN_IDENTITY:-}" ]; then
		echo "pending: a notarized release needs AMX_CODESIGN_IDENTITY to name the Developer ID Application identity" >&2
		exit "$missing"
	fi
	if [ -z "${AMX_NOTARY_PROFILE:-}" ]; then
		echo "pending: a notarized release needs AMX_NOTARY_PROFILE (xcrun notarytool store-credentials)" >&2
		exit "$missing"
	fi
	if ! xcrun notarytool history --keychain-profile "$AMX_NOTARY_PROFILE" >/dev/null 2>&1; then
		echo "pending: the notary profile $AMX_NOTARY_PROFILE is not in this keychain or Apple refused it" >&2
		exit "$missing"
	fi
	echo "notary_profile: $AMX_NOTARY_PROFILE"
}

bundle() {
	app="${1:-$macos_dir/build/Agent Mux.app}"
	# shellcheck source=macos/scripts/app-lib.sh
	. "$script_dir/app-lib.sh"
	check_bundle "$app"

	plist="$app/Contents/Info.plist"
	echo "bundle_version: $(plutil -extract CFBundleShortVersionString raw -o - "$plist")"
	echo "bundle_build: $(plutil -extract CFBundleVersion raw -o - "$plist")"
	echo "bundle_minimum_os: $(plutil -extract LSMinimumSystemVersion raw -o - "$plist")"
	echo "bundle_executable_sha256: $(shasum -a 256 "$app/Contents/MacOS/Agent Mux" | cut -d' ' -f1)"

	bundled="$app/Contents/Resources/bin/amx"
	bundled_sum=$(shasum -a 256 "$bundled" | cut -d' ' -f1)
	echo "amx_version: $("$bundled" --version | head -1)"
	echo "amx_sha256: $bundled_sum"
	# The amx in the bundle must be the one this checkout just built: a bundle
	# that carried another would be released under this revision's name.
	# sign.sh re-signs the copy it bundles, so the two files differ as files;
	# what they share is the build ID the Go linker gave them.
	if [ ! -f "$repo_dir/amx" ]; then
		echo "no amx at $repo_dir/amx to compare the bundled one with; run \`just mac-app Release\`" >&2
		exit 1
	fi
	bundled_id=$(go tool buildid "$bundled")
	built_id=$(go tool buildid "$repo_dir/amx")
	echo "amx_go_build_id: $bundled_id"
	if [ -z "$bundled_id" ] || [ "$bundled_id" != "$built_id" ]; then
		echo "the bundled amx is not the amx this checkout built (build ID $bundled_id, built $built_id)" >&2
		exit 1
	fi

	runtime="$app/Contents/Resources/tmux"
	echo "tmux_version: $("$runtime/bin/tmux" -V)"
	echo "tmux_sha256: $(shasum -a 256 "$runtime/bin/tmux" | cut -d' ' -f1)"
	echo "tmux_checksums_sha256: $(shasum -a 256 "$runtime/SHA256SUMS" | cut -d' ' -f1)"

	details=$(codesign -dvv "$app" 2>&1)
	authority=$(echo "$details" | sed -n 's/^Authority=//p' | head -1)
	if echo "$details" | grep -q '^Signature=adhoc'; then
		signature="ad-hoc"
	elif [ -n "$authority" ]; then
		signature="$authority"
	else
		signature="unknown"
	fi
	echo "signature: $signature"
	if xcrun stapler validate "$app" >/dev/null 2>&1; then
		stapled=yes
	else
		stapled=no
	fi
	echo "notarization_stapled: $stapled"
	case "$signature" in
	"Developer ID Application:"*)
		if [ "$stapled" = yes ]; then
			echo "distribution: notarized"
		else
			echo "distribution: developer-id-not-notarized"
		fi
		;;
	*)
		echo "distribution: this-mac-only"
		;;
	esac
	if [ "$notarized" = yes ]; then
		case "$signature" in
		"Developer ID Application:"*) ;;
		*)
			echo "a notarized release was asked for and the bundle is signed $signature" >&2
			exit 1
			;;
		esac
		if [ "$stapled" != yes ]; then
			echo "a notarized release was asked for and the bundle carries no notarization ticket" >&2
			exit 1
		fi
	fi
}

case "$command" in
toolchain)
	toolchain
	;;
bundle)
	bundle "$@"
	;;
*)
	echo "usage: release-gate.sh toolchain [--notarized] | bundle [--notarized] [APP]" >&2
	exit 1
	;;
esac
