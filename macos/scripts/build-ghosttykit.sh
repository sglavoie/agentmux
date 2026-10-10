#!/usr/bin/env sh
#
# Build GhosttyKit.xcframework from the Ghostty tag pinned in
# macos/GHOSTTY_VERSION and vendor it, with ghostty.h and the resources
# Agent Mux.app bundles (themes, shell integration, terminfo), into
# macos/Vendor/. Idempotent: a matching stamp in Vendor/ skips the whole build.
# Everything it needs is fetched and checked here; no other checkout is read.
#
# Environment:
#   AMX_MACOS_CACHE         cache root (default macos/.cache, gitignored)
#   GHOSTTYKIT_TARGET       xcframework target: universal (default) or native
#   GHOSTTYKIT_FORCE=1      rebuild even when the stamp matches

set -eu

require_tool() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "required tool not found: $1" >&2
		exit 1
	fi
}

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
macos_dir=$(dirname -- "$script_dir")
vendor_dir="$macos_dir/Vendor"
version_file="$macos_dir/GHOSTTY_VERSION"

# shellcheck disable=SC1090
. "$version_file"
: "${GHOSTTY_REPO:?missing in GHOSTTY_VERSION}"
: "${GHOSTTY_TAG:?missing in GHOSTTY_VERSION}"
: "${GHOSTTY_COMMIT:?missing in GHOSTTY_VERSION}"
: "${ZIG_VERSION:?missing in GHOSTTY_VERSION}"

if [ "$(uname -s)" != Darwin ]; then
	echo "GhosttyKit builds on macOS only" >&2
	exit 1
fi

target="${GHOSTTYKIT_TARGET:-universal}"
case "$target" in
universal | native) ;;
*)
	echo "GHOSTTYKIT_TARGET must be universal or native, not $target" >&2
	exit 1
	;;
esac

patch_dir="$macos_dir/patches/ghostty"
patches_digest=none
if ls "$patch_dir"/*.patch >/dev/null 2>&1; then
	patches_digest=$(cat "$patch_dir"/*.patch | shasum -a 256 | cut -c1-16)
fi

stamp="ghostty=$GHOSTTY_TAG commit=$GHOSTTY_COMMIT zig=$ZIG_VERSION target=$target patches=$patches_digest"
stamp_file="$vendor_dir/.stamp"
if [ "${GHOSTTYKIT_FORCE:-0}" != 1 ] &&
	[ -d "$vendor_dir/GhosttyKit.xcframework" ] &&
	[ -f "$vendor_dir/ghostty.h" ] &&
	[ -f "$vendor_dir/resources/terminfo/78/xterm-ghostty" ] &&
	[ -f "$stamp_file" ] &&
	[ "$(cat "$stamp_file")" = "$stamp" ]; then
	echo "GhosttyKit up to date ($stamp)"
	exit 0
fi

require_tool git
require_tool curl
require_tool shasum
require_tool tar
require_tool xcrun
require_tool xcodebuild

cache_root="${AMX_MACOS_CACHE:-$macos_dir/.cache}"
mkdir -p "$cache_root"

# --- Metal toolchain -----------------------------------------------------------
#
# Ghostty compiles its shaders with `metal`, which recent Xcode ships as a
# separate component rather than inside the app bundle.

if ! xcrun -sdk macosx metal --version >/dev/null 2>&1; then
	echo "Metal Toolchain missing; downloading it through xcodebuild..."
	xcodebuild -downloadComponent MetalToolchain
	if ! xcrun -sdk macosx metal --version >/dev/null 2>&1; then
		echo "metal is still unavailable after the Metal Toolchain download" >&2
		exit 1
	fi
fi

# --- Zig ---------------------------------------------------------------------

zig_matches() {
	[ -n "$1" ] && [ "$("$1" version 2>/dev/null || true)" = "$ZIG_VERSION" ]
}

resolve_zig() {
	if zig_matches "$(command -v zig 2>/dev/null || true)"; then
		command -v zig
		return
	fi
	if command -v mise >/dev/null 2>&1; then
		if mise install "zig@$ZIG_VERSION" >&2; then
			candidate=$(mise which --tool "zig@$ZIG_VERSION" zig 2>/dev/null || true)
			if zig_matches "$candidate"; then
				echo "$candidate"
				return
			fi
		fi
		echo "mise could not provide zig $ZIG_VERSION; using the checksummed tarball" >&2
	fi

	case "$(uname -m)" in
	arm64 | aarch64)
		zig_arch=aarch64
		zig_sha="${ZIG_SHA256_AARCH64_MACOS:?missing in GHOSTTY_VERSION}"
		;;
	x86_64)
		zig_arch=x86_64
		zig_sha="${ZIG_SHA256_X86_64_MACOS:?missing in GHOSTTY_VERSION}"
		;;
	*)
		echo "unsupported architecture: $(uname -m)" >&2
		exit 1
		;;
	esac

	zig_name="zig-$zig_arch-macos-$ZIG_VERSION"
	zig_dir="$cache_root/zig/$zig_name"
	if zig_matches "$zig_dir/zig"; then
		echo "$zig_dir/zig"
		return
	fi

	mkdir -p "$cache_root/zig"
	archive="$cache_root/zig/$zig_name.tar.xz"
	echo "Downloading $zig_name..." >&2
	curl -fL --retry 3 -o "$archive.part" \
		"https://ziglang.org/download/$ZIG_VERSION/$zig_name.tar.xz" >&2
	actual=$(shasum -a 256 "$archive.part" | awk '{print $1}')
	if [ "$actual" != "$zig_sha" ]; then
		rm -f "$archive.part"
		echo "zig checksum mismatch: expected $zig_sha, got $actual" >&2
		exit 1
	fi
	mv "$archive.part" "$archive"
	rm -rf "$zig_dir"
	tar -xJf "$archive" -C "$cache_root/zig"
	rm -f "$archive"
	if ! zig_matches "$zig_dir/zig"; then
		echo "extracted zig at $zig_dir does not report $ZIG_VERSION" >&2
		exit 1
	fi
	echo "$zig_dir/zig"
}

zig=$(resolve_zig)
echo "Using zig $ZIG_VERSION at $zig"

# --- Host SDK shim -------------------------------------------------------------
#
# Zig links its native build runner against the Xcode SDK. The macOS 27 SDK's
# .tbd stubs list arm64e but not arm64, so Zig's linker finds no libSystem
# symbols and `zig build` fails before Ghostty's build script runs. A probe
# decides whether this host needs help; when it does, an `xcrun` shim hands Zig
# a mirror of the SDK whose usr/lib stubs also claim arm64. Everything Ghostty
# ships is built for explicit targets, so the shim only reaches host tools.

probe_native_link() {
	probe_dir="$cache_root/probe"
	rm -rf "$probe_dir"
	mkdir -p "$probe_dir"
	printf 'pub fn main() void {}\n' >"$probe_dir/probe.zig"
	(cd "$probe_dir" && PATH="$1" "$zig" build-exe probe.zig -lc \
		--global-cache-dir "$cache_root/zig-global-cache" >/dev/null 2>&1)
}

build_sdk_shim() {
	real_sdk=$(/usr/bin/xcrun --sdk macosx --show-sdk-path)
	shim_root="$cache_root/sdk-shim"
	shim_sdk="$shim_root/MacOSX.sdk"
	shim_stamp="$shim_root/.source"
	if [ -f "$shim_stamp" ] && [ "$(cat "$shim_stamp")" = "$real_sdk" ]; then
		return
	fi
	rm -rf "$shim_root"
	mkdir -p "$shim_sdk/usr"
	for entry in "$real_sdk"/* "$real_sdk"/usr/*; do
		rel=${entry#"$real_sdk"/}
		case "$rel" in usr | usr/lib) continue ;; esac
		ln -s "$entry" "$shim_sdk/$rel"
	done
	(
		cd "$real_sdk"
		find usr/lib -type d -exec mkdir -p "$shim_sdk/{}" \;
		find usr/lib ! -type d | while IFS= read -r f; do
			if [ -L "$f" ]; then
				ln -s "$(readlink "$f")" "$shim_sdk/$f"
			elif [ "${f%.tbd}" != "$f" ]; then
				sed 's/arm64e-macos/arm64-macos, arm64e-macos/g' "$f" >"$shim_sdk/$f"
			else
				ln -s "$real_sdk/$f" "$shim_sdk/$f"
			fi
		done
	)
	mkdir -p "$shim_root/bin"
	cat >"$shim_root/bin/xcrun" <<SHIM
#!/bin/sh
case "\$*" in
"--sdk macosx --show-sdk-path" | "--show-sdk-path") echo "$shim_sdk" ;;
*) exec /usr/bin/xcrun "\$@" ;;
esac
SHIM
	chmod +x "$shim_root/bin/xcrun"
	printf '%s\n' "$real_sdk" >"$shim_stamp"
}

build_path="$PATH"
if ! probe_native_link "$build_path"; then
	echo "Zig cannot link against this host's SDK; using the arm64 SDK shim"
	build_sdk_shim
	build_path="$cache_root/sdk-shim/bin:$PATH"
	if ! probe_native_link "$build_path"; then
		echo "zig $ZIG_VERSION cannot link a native binary even with the SDK shim" >&2
		exit 1
	fi
fi

# --- Ghostty source ----------------------------------------------------------

src_dir="$cache_root/ghostty-$GHOSTTY_TAG"
# A cached clone is reused only while it sits on the pinned commit; the commit,
# not the tag, is what is built, so a moved tag is refused rather than followed.
if [ ! -d "$src_dir/.git" ] || [ "$(git -C "$src_dir" rev-parse HEAD 2>/dev/null || true)" != "$GHOSTTY_COMMIT" ]; then
	echo "Cloning Ghostty $GHOSTTY_TAG..."
	rm -rf "$src_dir"
	git clone --quiet --depth 1 --branch "$GHOSTTY_TAG" \
		-c advice.detachedHead=false "$GHOSTTY_REPO" "$src_dir"
fi
actual_commit=$(git -C "$src_dir" rev-parse HEAD)
if [ "$actual_commit" != "$GHOSTTY_COMMIT" ]; then
	echo "$GHOSTTY_TAG resolves to $actual_commit, pinned $GHOSTTY_COMMIT" >&2
	exit 1
fi

# Patches in macos/patches/ghostty/ are written against GHOSTTY_TAG: each one
# names why the pinned tag needs it and is dropped once an upstream tag does.
# Tracked files are restored, and files a patch created are removed, first so a
# changed patch set never stacks. Ignored build output survives the clean.
git -C "$src_dir" checkout --quiet -- .
git -C "$src_dir" clean --quiet -fd
for patch in "$patch_dir"/*.patch; do
	[ -f "$patch" ] || continue
	echo "Applying $(basename "$patch")"
	git -C "$src_dir" apply "$patch"
done

# --- Build -------------------------------------------------------------------

echo "Building GhosttyKit.xcframework ($target)..."
(
	cd "$src_dir"
	PATH="$build_path" ZIG_GLOBAL_CACHE_DIR="$cache_root/zig-global-cache" \
		"$zig" build \
		-Doptimize=ReleaseFast \
		-Demit-xcframework=true \
		-Demit-macos-app=false \
		-Dxcframework-target="$target"
)

built="$src_dir/macos/GhosttyKit.xcframework"
header="$src_dir/include/ghostty.h"
share="$src_dir/zig-out/share"
if [ ! -d "$built" ] || [ ! -f "$header" ] || [ ! -f "$share/terminfo/78/xterm-ghostty" ]; then
	echo "build finished without $built, $header, or $share/terminfo" >&2
	exit 1
fi

# --- Vendor ------------------------------------------------------------------

mkdir -p "$vendor_dir"
rm -rf "$vendor_dir/GhosttyKit.xcframework" "$vendor_dir/ghostty.h" "$vendor_dir/resources" "$stamp_file"
cp -R "$built" "$vendor_dir/GhosttyKit.xcframework"
cp "$header" "$vendor_dir/ghostty.h"

# libghostty finds its resources beside the terminfo sentinel under the app's
# Contents/Resources, so Agent Mux.app bundles both directories as Ghostty.app does.
# The man pages and HTML docs are Ghostty.app's alone.
mkdir -p "$vendor_dir/resources/ghostty"
cp -R "$share/terminfo" "$vendor_dir/resources/terminfo"
cp -R "$share/ghostty/themes" "$share/ghostty/shell-integration" "$vendor_dir/resources/ghostty/"
printf '%s\n' "$stamp" >"$stamp_file"
echo "Vendored GhosttyKit into $vendor_dir ($stamp)"
