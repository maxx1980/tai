#!/bin/sh
# Builds a macOS .icns from already-rendered PNGs, using the system's iconutil
# (ships with macOS itself, nothing to install). Shared by `make
# install-app-macos` (source build, prefix assets/png/icon) and get.sh
# (prebuilt install, prefix <extracted-archive>/icons/webssh) — both name their
# per-size files "<prefix>-<N>.png", just with a different prefix.
#
#   make-icns.sh <png-prefix> <output.icns>
set -eu

prefix=${1:?usage: make-icns.sh <png-prefix> <output.icns>}
out=${2:?usage: make-icns.sh <png-prefix> <output.icns>}

command -v iconutil >/dev/null 2>&1 || {
	echo "make-icns: iconutil not found (this script is macOS-only)" >&2
	exit 1
}

for n in 16 32 64 128 256 512; do
	[ -f "$prefix-$n.png" ] || {
		echo "make-icns: missing $prefix-$n.png" >&2
		exit 1
	}
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
iconset="$work/webssh.iconset"
mkdir -p "$iconset"

cp "$prefix-16.png" "$iconset/icon_16x16.png"
cp "$prefix-32.png" "$iconset/icon_16x16@2x.png"
cp "$prefix-32.png" "$iconset/icon_32x32.png"
cp "$prefix-64.png" "$iconset/icon_32x32@2x.png"
cp "$prefix-128.png" "$iconset/icon_128x128.png"
cp "$prefix-256.png" "$iconset/icon_128x128@2x.png"
cp "$prefix-256.png" "$iconset/icon_256x256.png"
cp "$prefix-512.png" "$iconset/icon_256x256@2x.png"
cp "$prefix-512.png" "$iconset/icon_512x512.png"
# No 1024x1024 source (icon_512x512@2x.png) — iconutil accepts the gap fine,
# the bundle just has no super-retina representation.

mkdir -p "$(dirname "$out")"
iconutil -c icns "$iconset" -o "$out"
