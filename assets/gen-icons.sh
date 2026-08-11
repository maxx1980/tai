#!/bin/sh
# Renders every raster size webssh needs from the single master assets/icon.svg.
# Requires rsvg-convert (librsvg2-bin).
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
src="$root/assets/icon.svg"
png="$root/assets/png"
pub="$root/web/public"

command -v rsvg-convert >/dev/null 2>&1 || {
	echo "gen-icons: rsvg-convert not found (install librsvg2-bin)" >&2
	exit 1
}

render() { rsvg-convert -w "$1" -h "$1" "$src" -o "$2"; }

mkdir -p "$png" "$pub"

# sizes for the Windows .ico and the hicolor icon theme
for n in 16 24 32 48 64 128 256 512; do
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
