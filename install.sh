#!/usr/bin/env bash
# Builds webssh, asks how its interface should open, and registers it in the
# Linux application menu.
#
#   ./install.sh                  ask interactively (default)
#   ./install.sh --ui app         pick the mode up front, no questions
#   ./install.sh --yes            take the best available mode, never prompt
#   ./install.sh --run            start the server when done
#
# Modes: browser (a tab in your default browser), app (chromeless chromium
#        window with its own profile), webview (native window built into the
#        binary).
set -euo pipefail

cd "$(dirname -- "${BASH_SOURCE[0]}")"

# webview_go pins gtk+-3.0 + webkit2gtk-4.0 in its cgo pkg-config line.
WEBVIEW_PKGS="libgtk-3-dev libwebkit2gtk-4.0-dev"

run_after=0
assume_yes=0
mode=""

while (($#)); do
	case "$1" in
	--run) run_after=1 ;;
	-y | --yes) assume_yes=1 ;;
	--ui)
		[[ $# -ge 2 ]] || {
			echo "install.sh: --ui needs a value (browser|app|webview)" >&2
			exit 2
		}
		mode=$2
		shift
		;;
	--ui=*) mode=${1#--ui=} ;;
	-h | --help)
		sed -n '2,12p' "${BASH_SOURCE[0]}" | cut -c3-
		exit 0
		;;
	*)
		echo "install.sh: unknown option '$1' (try --help)" >&2
		exit 2
		;;
	esac
	shift
done

case "${mode:-}" in
"" | browser | app | webview) ;;
*)
	echo "install.sh: --ui must be browser, app or webview (got '$mode')" >&2
	exit 2
	;;
esac

# --- required tools ---------------------------------------------------------
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

# --- what this machine can actually do --------------------------------------
# Mirrors chromiumNames/chromiumPaths in internal/appwin/chromium.go: only
# chromium-family browsers support the --app window.
find_chromium() {
	local c
	for c in google-chrome google-chrome-stable chromium chromium-browser \
		brave-browser microsoft-edge vivaldi-stable thorium-browser; do
		if command -v "$c" >/dev/null 2>&1; then
			command -v "$c"
			return 0
		fi
	done
	for c in /opt/google/chrome/google-chrome /opt/microsoft/msedge/microsoft-edge \
		/opt/brave.com/brave/brave-browser /opt/vivaldi/vivaldi; do
		if [[ -x $c ]]; then
			echo "$c"
			return 0
		fi
	done
	return 1
}

chromium=$(find_chromium || true)
webkit_ready=0
pkg-config --exists gtk+-3.0 webkit2gtk-4.0 2>/dev/null && webkit_ready=1

# --- choose the mode --------------------------------------------------------
choose_mode() {
	# Non-interactive: an app window when one is possible, else a plain browser.
	if ((assume_yes)) || [[ ! -t 0 ]]; then
		[[ -n $chromium ]] && echo app || echo browser
		return
	fi

	{
		echo
		echo "How should webssh open its interface?"
		echo
		echo "  1) Default browser   a normal tab, the way it works today"
		if [[ -n $chromium ]]; then
			echo "  2) App window        separate window, no tabs or address bar  [recommended]"
			echo "                       using: $chromium"
		else
			echo "  2) App window        UNAVAILABLE - no chromium-based browser found"
		fi
		if ((webkit_ready)); then
			echo "  3) Native webview    built into the binary, no browser at all"
		else
			echo "  3) Native webview    needs: sudo apt install $WEBVIEW_PKGS"
		fi
		echo
	} >&2

	local default=1 reply
	[[ -n $chromium ]] && default=2
	while :; do
		read -r -p "Choice [1-3, default $default]: " reply >&2 || reply=""
		reply=${reply:-$default}
		case "$reply" in
		1)
			echo browser
			return
			;;
		2)
			if [[ -z $chromium ]]; then
				echo "  No chromium-based browser is installed; pick 1 or 3." >&2
				continue
			fi
			echo app
			return
			;;
		3)
			echo webview
			return
			;;
		*) echo "  Enter 1, 2 or 3." >&2 ;;
		esac
	done
}

[[ -n $mode ]] || mode=$(choose_mode)

# --- webview needs its headers before anything can be built -----------------
if [[ $mode == webview ]] && ((!webkit_ready)); then
	echo
	echo "The webview build needs: $WEBVIEW_PKGS"
	if ((assume_yes)) || [[ ! -t 0 ]]; then
		echo "Install it and re-run, or choose another mode:" >&2
		echo "    sudo apt install $WEBVIEW_PKGS" >&2
		exit 1
	fi
	read -r -p "Run 'sudo apt install $WEBVIEW_PKGS' now? [y/N] " reply
	if [[ $reply == [yY] || $reply == [yY][eE][sS] ]]; then
		sudo apt install -y $WEBVIEW_PKGS
		pkg-config --exists gtk+-3.0 webkit2gtk-4.0 || {
			echo "install.sh: the webview packages are still not detected; aborting" >&2
			exit 1
		}
	else
		echo "install.sh: cannot build the webview without them; aborting" >&2
		exit 1
	fi
fi

# --- build ------------------------------------------------------------------
# The SPA is embedded into the binary, so its dependencies must exist first.
if [[ ! -d web/node_modules ]]; then
	echo "==> installing frontend dependencies"
	make deps
fi

if [[ $mode == webview ]]; then
	echo "==> building webssh (with the embedded webview)"
	make build-webview
else
	echo "==> building webssh"
	make build
fi

echo "==> installing desktop entry and icons"
make install-desktop

# Recorded in the database — the same place the Settings panel writes it, so
# the launcher and the UI can never disagree about the mode.
echo "==> saving the interface mode"
./webssh --set-ui-mode "$mode"

case "$mode" in
browser) how="a tab in your default browser" ;;
app) how="an app window via $chromium" ;;
webview) how="a native webview window" ;;
esac

cat <<EOF

webssh is installed.
  binary   $PWD/webssh
  launcher ${XDG_DATA_HOME:-$HOME/.local/share}/applications/webssh.desktop
  opens as $how

It should now show up in the application menu; some desktops need a
re-login before a newly added launcher appears. The mode can be changed
later in Settings, or per run with '--ui browser|app|webview'.
Remove everything again with './uninstall.sh'.
EOF

if ((run_after)); then
	echo
	echo "==> starting webssh"
	exec ./webssh
fi
