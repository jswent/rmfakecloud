#!/bin/sh
# Build librm_lines as a static library from pinned, checksummed sources.
#
# Usage:
#   build.sh fetch <srcdir>           download, verify (SHA256SUMS) and unpack all sources
#   build.sh build <srcdir> <outdir>  patch, configure offline, build, install into <outdir>
#   build.sh all <outdir>             fetch into <outdir>/src, then build into <outdir>
#
# Output layout:
#   <outdir>/lib/librm_lines.a libharfbuzz.a libfreetype.a libsimdutf.a  (link in this order)
#   <outdir>/include/library.h      upstream C++ API header (reference; cgo uses its own C header)
#   <outdir>/licenses/<project>/... licence files of everything linked in
#   <outdir>/VERSION
#
# Requirements: POSIX sh, curl or wget, tar with gzip and xz support, patch,
# CMake >= 3.30, a C++20 compiler (GCC >= 13 or Apple clang >= 15), and
# Python 3 with fontTools (librm_lines splits its variable fonts into static
# weights at configure time). Ninja is used when available.
#
# Environment:
#   PYTHON3   Python interpreter with fontTools (default: python3). On macOS:
#               python3 -m venv .build/rmlines/venv
#               .build/rmlines/venv/bin/pip install fonttools
#               PYTHON3=.build/rmlines/venv/bin/python build/rmlines/build.sh all .build/rmlines
#   CMAKE     cmake executable (default: cmake)
#   JOBS      parallel build jobs (default: number of CPUs)
#   RMLINES_BUILD_DIR  CMake build directory (default: <outdir>/cmake-build)
#   RMLINES_DL_DIR     download cache for the archives (default: <srcdir>/../dl)
#
# The local patches in patches/ are tracked for upstreaming in
# https://github.com/jswent/rmfakecloud/issues/1
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
VERSION=$(cat "$HERE/VERSION")

die() { echo "build.sh: $*" >&2; exit 1; }
log() { echo "build.sh: $*" >&2; }

abspath() { mkdir -p "$1" && (cd "$1" && pwd); }

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

download() { # url dest
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$2.part" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2.part" "$1"
	else
		die "need curl or wget to download $1"
	fi
	mv "$2.part" "$2"
}

# sources: print "<file> <url> <fcname>" lines from SOURCES
sources() { grep -v '^[[:space:]]*#' "$HERE/SOURCES" | grep -v '^[[:space:]]*$'; }

# unpack name: librm_lines for the main tarball, lower-case FetchContent name otherwise
unpack_name() { if [ "$1" = "-" ]; then echo librm_lines; else echo "$1" | tr 'A-Z' 'a-z'; fi; }

cmd_fetch() {
	[ $# -eq 1 ] || die "usage: build.sh fetch <srcdir>"
	src=$(abspath "$1")
	dl=${RMLINES_DL_DIR:-$src/../dl}
	dl=$(abspath "$dl")
	sources | grep -q "^librm_lines-$VERSION\\.tar\\.gz " ||
		die "SOURCES does not list librm_lines-$VERSION.tar.gz (VERSION=$VERSION)"
	sources | while read -r file url fc; do
		want=$(awk -v f="$file" '$2 == f { print $1 }' "$HERE/SHA256SUMS")
		[ -n "$want" ] || die "no SHA256SUMS entry for $file"
		if [ -f "$dl/$file" ] && [ "$(sha256_of "$dl/$file")" = "$want" ]; then
			log "cached  $file"
		else
			log "fetch   $url"
			download "$url" "$dl/$file"
		fi
		got=$(sha256_of "$dl/$file")
		if [ "$got" != "$want" ]; then
			rm -f "$dl/$file"
			die "checksum mismatch for $file: got $got, want $want"
		fi
		name=$(unpack_name "$fc")
		rm -rf "$src/$name"
		mkdir -p "$src/$name"
		tar -xf "$dl/$file" -C "$src/$name" --strip-components=1
		log "ok      $file -> $src/$name"
	done
}

version_ge() { # a b: a >= b for dotted versions
	[ "$(printf '%s\n%s\n' "$2" "$1" | sort -t. -k1,1n -k2,2n -k3,3n | head -1)" = "$2" ]
}

cmd_build() {
	[ $# -eq 2 ] || die "usage: build.sh build <srcdir> <outdir>"
	src=$(abspath "$1")
	out=$(abspath "$2")
	bld=${RMLINES_BUILD_DIR:-$out/cmake-build}
	cmake=${CMAKE:-cmake}
	py=${PYTHON3:-python3}

	for d in librm_lines json cppcodec utf8cpp simdutf freetype harfbuzz; do
		[ -d "$src/$d" ] || die "missing $src/$d; run: build.sh fetch $src"
	done
	command -v "$cmake" >/dev/null 2>&1 || die "cmake not found"
	cv=$("$cmake" --version | head -1 | sed 's/[^0-9.]*\([0-9][0-9.]*\).*/\1/')
	version_ge "$cv" 3.30 || die "CMake >= 3.30 required (found $cv)"
	"$py" -c 'import fontTools' 2>/dev/null || die "$py cannot import fontTools.
  Alpine: apk add py3-fonttools   Debian/Ubuntu: apt install python3-fonttools
  macOS:  python3 -m venv .build/rmlines/venv && .build/rmlines/venv/bin/pip install fonttools
          then set PYTHON3=.build/rmlines/venv/bin/python"
	py=$(command -v "$py" || echo "$py")

	# Patch a fresh copy so the unpacked source stays pristine and re-runs are idempotent.
	work=$bld.src
	rm -rf "$work"
	cp -R "$src/librm_lines" "$work"
	for p in "$HERE"/patches/*.patch; do
		log "patch   $(basename "$p")"
		patch -d "$work" -p1 -s < "$p"
	done
	# Never embed reMarkable's proprietary fonts: upstream switches to them
	# automatically when they are present in rm_lines/assets/fonts.
	if ls "$work"/rm_lines/assets/fonts/reMarkable* >/dev/null 2>&1; then
		die "proprietary reMarkable fonts found in source tree; refusing to build"
	fi

	gen=
	if command -v ninja >/dev/null 2>&1 || command -v samu >/dev/null 2>&1; then
		gen="-G Ninja"
		command -v ninja >/dev/null 2>&1 || gen="$gen -DCMAKE_MAKE_PROGRAM=$(command -v samu)"
	fi
	jobs=${JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)}

	t0=$(date +%s)
	# shellcheck disable=SC2086
	"$cmake" -S "$work" -B "$bld" $gen \
		-DCMAKE_BUILD_TYPE=Release \
		-DCMAKE_POSITION_INDEPENDENT_CODE=ON \
		-DRM_LINES_STATIC=ON \
		-DPython3_EXECUTABLE="$py" \
		-DFETCHCONTENT_FULLY_DISCONNECTED=ON \
		-DFETCHCONTENT_SOURCE_DIR_JSON="$src/json" \
		-DFETCHCONTENT_SOURCE_DIR_CPPCODEC="$src/cppcodec" \
		-DFETCHCONTENT_SOURCE_DIR_UTF8CPP="$src/utf8cpp" \
		-DFETCHCONTENT_SOURCE_DIR_SIMDUTF="$src/simdutf" \
		-DFETCHCONTENT_SOURCE_DIR_FREETYPE="$src/freetype" \
		-DFETCHCONTENT_SOURCE_DIR_HARFBUZZ="$src/harfbuzz"
	t1=$(date +%s)
	"$cmake" --build "$bld" --target rm_lines --parallel "$jobs"
	t2=$(date +%s)

	rm -rf "$out/lib" "$out/include" "$out/licenses"
	mkdir -p "$out/lib" "$out/include"
	cp "$bld/librm_lines.a" "$out/lib/"
	for a in libharfbuzz.a libfreetype.a libsimdutf.a; do
		f=$(find "$bld/_deps" -name "$a" -type f | head -1)
		[ -n "$f" ] || die "$a not found under $bld/_deps"
		cp "$f" "$out/lib/"
	done
	cp "$work/rm_lines/headers/library.h" "$out/include/"

	cplic() { mkdir -p "$out/licenses/$1"; cp "$2" "$out/licenses/$1/"; }
	cplic librm_lines "$work/LICENSE.md"
	cplic librm_lines "$work/THIRD-PARTY-NOTICES.md"
	cplic rm_lines_stroker "$work/rm_lines/src/renderer/rm_lines_stroker/LICENSE"
	cplic json "$src/json/LICENSE.MIT"
	cplic cppcodec "$src/cppcodec/LICENSE"
	cplic utf8cpp "$src/utf8cpp/LICENSE"
	cplic simdutf "$src/simdutf/LICENSE-MIT"
	cplic simdutf "$src/simdutf/LICENSE-APACHE"
	cplic freetype "$src/freetype/LICENSE.TXT"
	cplic freetype "$src/freetype/docs/FTL.TXT"
	cplic harfbuzz "$src/harfbuzz/COPYING"
	echo "$VERSION" > "$out/VERSION"

	log "configure $((t1 - t0))s, build $((t2 - t1))s ($jobs jobs, $(uname -sm))"
	ls -l "$out/lib" >&2
}

cmd_all() {
	[ $# -eq 1 ] || die "usage: build.sh all <outdir>"
	out=$(abspath "$1")
	cmd_fetch "$out/src"
	cmd_build "$out/src" "$out"
}

[ $# -ge 1 ] || die "usage: build.sh fetch|build|all ... (see header)"
cmd=$1
shift
case $cmd in
fetch) cmd_fetch "$@" ;;
build) cmd_build "$@" ;;
all) cmd_all "$@" ;;
*) die "unknown command: $cmd" ;;
esac
