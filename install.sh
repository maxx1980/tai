#!/usr/bin/env bash
# Builds webssh and registers it in the Linux application menu.
#
#   ./install.sh            build + install the desktop entry
#   ./install.sh --run      ...and start the server afterwards
set -euo pipefail

cd "$(dirname -- "${BASH_SOURCE[0]}")"

run_after=0
for arg in "$@"; do
	case "$arg" in
	--run) run_after=1 ;;
	-h | --help)
		sed -n '2,5p' "${BASH_SOURCE[0]}" | cut -c3-
		exit 0
		;;
	*)
		echo "install.sh: unknown option '$arg' (try --help)" >&2
		exit 2
		;;
	esac
done

missing=()
for tool in go npm rsvg-convert make; do
	command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
done
if ((${#missing[@]})); then
	echo "install.sh: missing required tools: ${missing[*]}" >&2
	echo "  go           https://go.dev/dl/" >&2
	echo "  npm          nodejs" >&2
	echo "  rsvg-convert librsvg2-bin" >&2
	exit 1
fi

# The SPA is embedded into the binary, so its dependencies must exist first.
if [[ ! -d web/node_modules ]]; then
	echo "==> installing frontend dependencies"
	make deps
fi

echo "==> building webssh"
make build

echo "==> installing desktop entry and icons"
make install-desktop

cat <<EOF

webssh is installed.
  binary   $PWD/webssh
  launcher ${XDG_DATA_HOME:-$HOME/.local/share}/applications/webssh.desktop

It should now show up in the application menu; some desktops need a
re-login before a newly added launcher appears. Remove it again with
'make uninstall-desktop'.
EOF

if ((run_after)); then
	echo
	echo "==> starting webssh"
	exec ./webssh
fi
