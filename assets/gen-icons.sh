#!/bin/sh
# Renders every raster size webssh needs from the single master assets/icon.svg.
# Requires rsvg-convert (librsvg2-bin).
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
src="$root/assets/icon.svg"
png="$root/assets/png"
pub="$root/web/public"

sizes="16 24 32 48 64 128 256 512"

# Without rsvg-convert, the committed/previously rendered PNGs still do: `make
# run` and `make build` only need web/public/, which is in git. Fail only when
# a file is actually missing, so a fresh checkout without librsvg still builds.
command -v rsvg-convert >/dev/null 2>&1 || {
	missing=""
	for f in "$pub/favicon-32.png" "$pub/apple-touch-icon.png" "$pub/icon-192.png" "$pub/icon-512.png"; do
		[ -f "$f" ] || missing="$missing $f"
	done
	if [ -n "${GEN_ICONS_ALL:-}" ]; then
		for n in $sizes; do
			[ -f "$png/icon-$n.png" ] || missing="$missing $png/icon-$n.png"
		done
	fi
	if [ -n "$missing" ]; then
		echo "gen-icons: rsvg-convert not found (install librsvg2-bin); missing:$missing" >&2
		exit 1
	fi
	echo "gen-icons: rsvg-convert not found — keeping the existing icons (install librsvg2-bin to re-render)" >&2
	exit 0
}

render() { rsvg-convert -w "$1" -h "$1" "$src" -o "$2"; }

mkdir -p "$png" "$pub"

# sizes for the Windows .ico and the hicolor icon theme
for n in $sizes; do
	render "$n" "$png/icon-$n.png"
done

# web UI favicons (Vite copies web/public/ into web/dist/ verbatim)
cp "$src" "$pub/favicon.svg"
render 32 "$pub/favicon-32.png"
render 180 "$pub/apple-touch-icon.png"

# sizes manifest.json wants for the Android "Add to Home Screen" prompt
render 192 "$pub/icon-192.png"
render 512 "$pub/icon-512.png"

echo "gen-icons: wrote assets/png/*.png and web/public/{favicon.svg,favicon-32.png,apple-touch-icon.png,icon-192.png,icon-512.png}"
