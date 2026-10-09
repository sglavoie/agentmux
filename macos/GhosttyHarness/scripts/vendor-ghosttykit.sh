#!/usr/bin/env sh
#
# Copy the GhosttyKit build sglavoie/tt vendored into this harness's cache,
# read-only with respect to tt. The copy happens only when tt's Vendor/.stamp
# names exactly the pins in ../GHOSTTY_VERSION and tt's patches still hash to
# GHOSTTY_PATCHES_DIGEST; otherwise build GhosttyKit in tt first
# (`just mac-ghosttykit` there). Idempotent: a matching local stamp skips it.
#
# Environment:
#   TT_REPO                 sglavoie/tt checkout (default ../sglavoie_tt beside this repo)
#   AMX_MACOS_CACHE         cache root (default macos/.cache, gitignored)

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
harness_dir=$(dirname -- "$script_dir")
macos_dir=$(dirname -- "$harness_dir")
repo_dir=$(dirname -- "$macos_dir")

# shellcheck disable=SC1091
. "$harness_dir/GHOSTTY_VERSION"
: "${GHOSTTY_TAG:?missing in GHOSTTY_VERSION}"
: "${ZIG_VERSION:?missing in GHOSTTY_VERSION}"
: "${GHOSTTYKIT_TARGET:?missing in GHOSTTY_VERSION}"
: "${GHOSTTY_PATCHES_DIGEST:?missing in GHOSTTY_VERSION}"

tt_repo="${TT_REPO:-$(dirname -- "$repo_dir")/sglavoie_tt}"
tt_vendor="$tt_repo/macos/Vendor"
tt_patches="$tt_repo/macos/patches/ghostty"
cache_root="${AMX_MACOS_CACHE:-$macos_dir/.cache}"
vendor_dir="$cache_root/ghostty-harness/Vendor"

stamp="ghostty=$GHOSTTY_TAG zig=$ZIG_VERSION target=$GHOSTTYKIT_TARGET patches=$GHOSTTY_PATCHES_DIGEST"

if [ -f "$vendor_dir/.stamp" ] && [ "$(cat "$vendor_dir/.stamp")" = "$stamp" ] &&
	[ -d "$vendor_dir/GhosttyKit.xcframework" ]; then
	echo "GhosttyKit up to date ($stamp)"
	exit 0
fi

if [ ! -f "$tt_vendor/.stamp" ]; then
	echo "no vendored GhosttyKit in $tt_vendor; run 'just mac-ghosttykit' in $tt_repo" >&2
	exit 1
fi
actual_digest=$(cat "$tt_patches"/*.patch | shasum -a 256 | cut -c1-16)
if [ "$actual_digest" != "$GHOSTTY_PATCHES_DIGEST" ]; then
	echo "tt's Ghostty patches hash to $actual_digest, pinned $GHOSTTY_PATCHES_DIGEST" >&2
	exit 1
fi
if [ "$(cat "$tt_vendor/.stamp")" != "$stamp" ]; then
	echo "tt's GhosttyKit is '$(cat "$tt_vendor/.stamp")', pinned '$stamp'" >&2
	exit 1
fi

rm -rf "$vendor_dir"
mkdir -p "$vendor_dir"
# -X: no extended attributes, so a quarantine bit cannot follow the copy.
cp -RX "$tt_vendor/GhosttyKit.xcframework" "$tt_vendor/ghostty.h" "$tt_vendor/resources" "$vendor_dir/"
printf '%s\n' "$stamp" >"$vendor_dir/.stamp"
echo "Vendored GhosttyKit ($stamp) from $tt_vendor"
