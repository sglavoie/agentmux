#!/usr/bin/env sh
#
# Build the tmux runtime Agent Mux.app bundles, from the sources pinned in
# macos/TMUX_VERSION, into macos/Vendor/tmux/. tmux is linked statically
# against libevent, ncurses, utf8proc, and jemalloc, so the runtime is one executable
# that needs nothing but the system libraries: no Homebrew, and no library
# that could be replaced under a running server. Idempotent: a matching stamp
# skips the whole build. Every tarball is checked against its pinned sha256.
#
# The runtime is built for this Mac's architecture only, like the bundled amx.
#
#   Vendor/tmux/bin/tmux      the executable
#   Vendor/tmux/VERSION       what was built, one name=value per line
#   Vendor/tmux/licenses/     the license of tmux and of each library in it
#
# sign.sh signs the executable and then writes Vendor-independent checksums
# into the bundle (SHA256SUMS), which amx verifies before it runs the runtime.
#
# Environment:
#   AMX_MACOS_CACHE         cache root (default macos/.cache, gitignored)
#   TMUX_RUNTIME_FORCE=1    rebuild even when the stamp matches

set -eu

require_tool() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "required tool not found: $1" >&2
		exit 1
	fi
}

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
macos_dir=$(dirname -- "$script_dir")
out_dir="$macos_dir/Vendor/tmux"
version_file="$macos_dir/TMUX_VERSION"

# shellcheck disable=SC1090
. "$version_file"
for name in TMUX LIBEVENT NCURSES UTF8PROC JEMALLOC; do
	for field in VERSION URL SHA256; do
		eval ": \"\${${name}_${field}:?missing in TMUX_VERSION}\""
	done
done
: "${TMUX_MIN_MACOS:?missing in TMUX_VERSION}"

if [ "$(uname -s)" != Darwin ]; then
	echo "the bundled tmux runtime builds on macOS only" >&2
	exit 1
fi
arch=$(uname -m)

stamp="tmux=$TMUX_VERSION libevent=$LIBEVENT_VERSION ncurses=$NCURSES_VERSION utf8proc=$UTF8PROC_VERSION jemalloc=$JEMALLOC_VERSION arch=$arch min_macos=$TMUX_MIN_MACOS pins=$(shasum -a 256 "$version_file" | cut -c1-16) recipe=$(shasum -a 256 "$0" | cut -c1-16)"
stamp_file="$out_dir/.stamp"

# The checks a finished runtime must pass, whether just built or found by its
# stamp: it reports the pinned version and loads nothing outside the system.
check_runtime() {
	reported=$("$out_dir/bin/tmux" -V 2>/dev/null || true)
	if [ "$reported" != "tmux $TMUX_VERSION" ]; then
		echo "$out_dir/bin/tmux reports '$reported', not 'tmux $TMUX_VERSION'" >&2
		return 1
	fi
	foreign=$(otool -L "$out_dir/bin/tmux" | tail -n +2 | awk '{print $1}' | grep -v '^/usr/lib/' || true)
	if [ -n "$foreign" ]; then
		echo "$out_dir/bin/tmux loads libraries outside the system:" >&2
		echo "$foreign" >&2
		return 1
	fi
}

if [ "${TMUX_RUNTIME_FORCE:-0}" != 1 ] &&
	[ -x "$out_dir/bin/tmux" ] &&
	[ -f "$stamp_file" ] &&
	[ "$(cat "$stamp_file")" = "$stamp" ] &&
	check_runtime; then
	echo "tmux runtime up to date ($TMUX_VERSION, $arch)"
	exit 0
fi

require_tool curl
require_tool shasum
require_tool tar
require_tool make
require_tool cc
require_tool yacc
require_tool otool

cache_root="${AMX_MACOS_CACHE:-$macos_dir/.cache}"
downloads="$cache_root/tmux-sources"
work="$cache_root/tmux-build"
prefix="$work/prefix"
mkdir -p "$downloads"
rm -rf "$work"
mkdir -p "$work" "$prefix"

# fetch NAME URL SHA256: a checksummed tarball in the cache, unpacked into
# $work/NAME.
fetch() {
	archive="$downloads/$1-$3.tar"
	if [ ! -f "$archive" ] || [ "$(shasum -a 256 "$archive" | awk '{print $1}')" != "$3" ]; then
		echo "Downloading $1..." >&2
		curl -fL --retry 3 -o "$archive.part" "$2" >&2
		actual=$(shasum -a 256 "$archive.part" | awk '{print $1}')
		if [ "$actual" != "$3" ]; then
			rm -f "$archive.part"
			echo "$1 checksum mismatch: expected $3, got $actual" >&2
			exit 1
		fi
		mv "$archive.part" "$archive"
	fi
	mkdir -p "$work/$1"
	tar -xf "$archive" -C "$work/$1" --strip-components 1
}

# Nothing outside the pinned sources may be linked: no pkg-config lookup, and
# no search path a package manager added.
unset PKG_CONFIG_PATH PKG_CONFIG_LIBDIR CPATH LIBRARY_PATH CPPFLAGS LDFLAGS LIBS
export PKG_CONFIG=/usr/bin/false
export PATH="/usr/bin:/bin:/usr/sbin:/sbin"
export MACOSX_DEPLOYMENT_TARGET="$TMUX_MIN_MACOS"
export CC=cc
cflags="-O2 -arch $arch -mmacosx-version-min=$TMUX_MIN_MACOS"
jobs=$(sysctl -n hw.ncpu 2>/dev/null || echo 4)

log="$work/build.log"
step() {
	name=$1
	shift
	echo "Building $name..." >&2
	if ! ("$@") >>"$log" 2>&1; then
		tail -40 "$log" >&2
		echo "building $name failed; full log: $log" >&2
		exit 1
	fi
}

build_libevent() {
	cd "$work/libevent"
	./configure --prefix="$prefix" --disable-shared --enable-static --disable-openssl \
		--disable-samples --disable-libevent-regress --disable-debug-mode \
		--disable-dependency-tracking CFLAGS="$cflags"
	make -j"$jobs"
	make install
}

# Terminal descriptions come from the system's terminfo database, which has
# had tmux-256color since macOS 14; a client's own (xterm-ghostty, xterm-kitty)
# arrives through the TERMINFO its terminal sets.
build_ncurses() {
	cd "$work/ncurses"
	./configure --prefix="$prefix" --without-shared --with-normal --without-debug \
		--without-ada --without-cxx --without-cxx-binding --without-manpages \
		--without-tests --without-progs --enable-widec --disable-db-install \
		--disable-pc-files --disable-stripping \
		--with-default-terminfo-dir=/usr/share/terminfo \
		--with-terminfo-dirs=/usr/share/terminfo \
		CFLAGS="$cflags"
	make -j"$jobs"
	make install
}

build_utf8proc() {
	cd "$work/utf8proc"
	make -j"$jobs" libutf8proc.a CFLAGS="$cflags"
	mkdir -p "$prefix/lib" "$prefix/include"
	cp libutf8proc.a "$prefix/lib/"
	cp utf8proc.h "$prefix/include/"
}

# tmux asks for jemalloc on macOS, whose own calloc(3) it found not to zero
# memory reliably. The whole archive is loaded so that the allocator's zone
# registration, which nothing in tmux refers to, is linked in. The empty
# prefix gives the allocator its plain names (mallctl), which tmux calls.
build_jemalloc() {
	cd "$work/jemalloc"
	./configure --prefix="$prefix" --disable-shared --enable-static --disable-cxx \
		--disable-doc --disable-stats --with-jemalloc-prefix= CFLAGS="$cflags"
	make -j"$jobs" build_lib_static
	make install_lib_static install_include
}

build_tmux() {
	cd "$work/tmux"
	./configure --prefix="$prefix" --enable-utf8proc --enable-jemalloc --disable-dependency-tracking \
		CFLAGS="$cflags" \
		LIBEVENT_CORE_CFLAGS="-I$prefix/include" LIBEVENT_CORE_LIBS="-L$prefix/lib -levent_core" \
		LIBEVENT_CFLAGS="-I$prefix/include" LIBEVENT_LIBS="-L$prefix/lib -levent_core" \
		LIBTINFO_CFLAGS="-I$prefix/include/ncursesw -I$prefix/include" LIBTINFO_LIBS="-L$prefix/lib -lncursesw" \
		LIBNCURSES_CFLAGS="-I$prefix/include/ncursesw -I$prefix/include" LIBNCURSES_LIBS="-L$prefix/lib -lncursesw" \
		LIBNCURSESW_CFLAGS="-I$prefix/include/ncursesw -I$prefix/include" LIBNCURSESW_LIBS="-L$prefix/lib -lncursesw" \
		LIBUTF8PROC_CFLAGS="-I$prefix/include" LIBUTF8PROC_LIBS="-L$prefix/lib -lutf8proc" \
		JEMALLOC_CFLAGS="-I$prefix/include" JEMALLOC_LIBS="-Wl,-force_load,$prefix/lib/libjemalloc.a"
	make -j"$jobs"
}

fetch libevent "$LIBEVENT_URL" "$LIBEVENT_SHA256"
fetch ncurses "$NCURSES_URL" "$NCURSES_SHA256"
fetch utf8proc "$UTF8PROC_URL" "$UTF8PROC_SHA256"
fetch jemalloc "$JEMALLOC_URL" "$JEMALLOC_SHA256"
fetch tmux "$TMUX_URL" "$TMUX_SHA256"

: >"$log"
step libevent build_libevent
step ncurses build_ncurses
step utf8proc build_utf8proc
step jemalloc build_jemalloc
step tmux build_tmux

# --- Vendor --------------------------------------------------------------------

rm -rf "$out_dir"
mkdir -p "$out_dir/bin" "$out_dir/licenses"
cp "$work/tmux/tmux" "$out_dir/bin/tmux"
chmod 755 "$out_dir/bin/tmux"
strip -x "$out_dir/bin/tmux"
cp "$work/tmux/COPYING" "$out_dir/licenses/tmux"
cp "$work/libevent/LICENSE" "$out_dir/licenses/libevent"
cp "$work/ncurses/COPYING" "$out_dir/licenses/ncurses"
cp "$work/utf8proc/LICENSE.md" "$out_dir/licenses/utf8proc"
cp "$work/jemalloc/COPYING" "$out_dir/licenses/jemalloc"
cat >"$out_dir/VERSION" <<VERSION
tmux=$TMUX_VERSION
libevent=$LIBEVENT_VERSION
ncurses=$NCURSES_VERSION
utf8proc=$UTF8PROC_VERSION
jemalloc=$JEMALLOC_VERSION
arch=$arch
min_macos=$TMUX_MIN_MACOS
VERSION

check_runtime
echo "$stamp" >"$stamp_file"
rm -rf "$work"
echo "Built $out_dir (tmux $TMUX_VERSION, $arch)"
