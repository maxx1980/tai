#!/usr/bin/env bash
# Builds webssh, asks how its interface should open, and registers it — the
# Linux application menu, or a webssh.app bundle in ~/Applications on macOS.
#
#   ./install.sh                       ask interactively (default)
#   ./install.sh --ui app              pick the mode up front, no questions
#   ./install.sh --app-browser PATH    pin which browser hosts the app window
#   ./install.sh --yes                 take the best available mode, never prompt
#   ./install.sh --run                 start the server when done
#
# Modes: browser (a tab in your default browser), app (chromeless chromium
#        window with its own profile), webview (native window built into the
#        binary).
set -euo pipefail

cd "$(dirname -- "${BASH_SOURCE[0]}")"

case "$(uname -s)" in
Linux) os_name=linux ;;
Darwin) os_name=darwin ;;
*) os_name=other ;;
esac

# webview_go pins gtk+-3.0 + webkit2gtk-4.0 in its cgo pkg-config line — a
# Linux-only build path; macOS has no equivalent offered by this installer.
WEBVIEW_PKGS="libgtk-3-dev libwebkit2gtk-4.0-dev"

run_after=0
assume_yes=0
mode=""
app_browser=""

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
	--app-browser)
		[[ $# -ge 2 ]] || {
			echo "install.sh: --app-browser needs a path" >&2
			exit 2
		}
		app_browser=$2
		shift
		;;
	--app-browser=*) app_browser=${1#--app-browser=} ;;
	-h | --help)
		sed -n '2,13p' "${BASH_SOURCE[0]}" | cut -c3-
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

if [[ -n $app_browser && ! -x $app_browser ]]; then
	echo "install.sh: --app-browser '$app_browser' is not an executable file" >&2
	exit 2
fi

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
# Mirrors FindChromiumAll in internal/appwin/chromium.go: only chromium-family
# browsers support the --app window, and PATH entries are usually symlinks to
# the copy under /opt, so the list is deduplicated by real path.
find_chromium_all() {
	local c real
	declare -A seen=()
	for c in google-chrome google-chrome-stable chromium chromium-browser \
		brave-browser microsoft-edge vivaldi-stable thorium-browser; do
		c=$(command -v "$c" 2>/dev/null) || continue
		real=$(readlink -f "$c" 2>/dev/null || echo "$c")
		[[ -n ${seen[$real]:-} ]] && continue
		seen[$real]=1
		echo "$c"
	done
	for c in /opt/google/chrome/google-chrome /opt/microsoft/msedge/microsoft-edge \
		/opt/brave.com/brave/brave-browser /opt/vivaldi/vivaldi \
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge" \
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser" \
		"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi" \
		"/Applications/Chromium.app/Contents/MacOS/Chromium"; do
		[[ -x $c ]] || continue
		real=$(readlink -f "$c" 2>/dev/null || echo "$c")
		[[ -n ${seen[$real]:-} ]] && continue
		seen[$real]=1
		echo "$c"
	done
}

mapfile -t browsers < <(find_chromium_all)
webkit_ready=0
pkg-config --exists gtk+-3.0 webkit2gtk-4.0 2>/dev/null && webkit_ready=1

# --- interactive selection --------------------------------------------------
# Sets the globals $mode and $app_browser, so it must not run in a subshell.
print_menu() {
	echo
	echo "How should webssh open its interface?"
	echo
	echo "  1) Default browser   a normal tab, the way it works today"
	if ((${#browsers[@]})); then
		echo "  2) App window        separate window, no tabs or address bar  [recommended]"
		echo "                       ${#browsers[@]} usable browser(s) found"
	else
		echo "  2) App window        no chromium-based browser found"
	fi
	if ((webkit_ready)); then
		echo "  3) Native webview    built into the binary, no browser at all"
	elif [[ $os_name == darwin ]]; then
		echo "  3) Native webview    not offered on macOS by this installer (Linux/GTK only)"
	else
		echo "  3) Native webview    needs: sudo apt install $WEBVIEW_PKGS"
	fi
	echo
}

# choose_browser asks which browser hosts the app window. Returns 1 when the
# user backs out, so the caller can redisplay the main menu.
choose_browser() {
	local i reply
	echo
	echo "Which browser should host the app window?"
	echo
	for i in "${!browsers[@]}"; do
		printf '  %d) %s\n' $((i + 1)) "${browsers[i]}"
	done
	printf '  %d) Detect automatically at startup\n' $((${#browsers[@]} + 1))
	echo "  0) Back"
	echo
	while :; do
		read -r -p "Browser [1-$((${#browsers[@]} + 1)), default 1]: " reply || reply=""
		reply=${reply:-1}
		if [[ $reply == 0 ]]; then
			return 1
		fi
		if [[ $reply =~ ^[0-9]+$ ]]; then
			if ((reply >= 1 && reply <= ${#browsers[@]})); then
				app_browser=${browsers[reply - 1]}
				return 0
			fi
			if ((reply == ${#browsers[@]} + 1)); then
				app_browser=auto
				return 0
			fi
		fi
		echo "  Enter a number from the list, or 0 to go back." >&2
	done
}

choose_mode() {
	# Non-interactive: an app window when one is possible, else a plain browser.
	if ((assume_yes)) || [[ ! -t 0 ]]; then
		if ((${#browsers[@]})); then
			mode=app
			[[ -n $app_browser ]] || app_browser=${browsers[0]}
		else
			mode=browser
		fi
		return
	fi

	local reply default=1
	((${#browsers[@]})) && default=2
	while :; do
		print_menu
		read -r -p "Choice [1-3, default $default]: " reply || reply=""
		reply=${reply:-$default}
		case "$reply" in
		1)
			mode=browser
			return
			;;
		2)
			if ((${#browsers[@]} == 0)); then
				echo
				echo "  No chromium-based browser is installed, so the app window is not"
				echo "  available. Install one (chromium, google-chrome, brave, edge, …)"
				echo "  and re-run, or pick another option."
				continue # back to the 1/2/3 menu
			fi
			# Backing out of the browser list returns to this menu.
			if choose_browser; then
				mode=app
				return
			fi
			;;
		3)
			mode=webview
			return
			;;
		*) echo "  Enter 1, 2 or 3." >&2 ;;
		esac
	done
}

[[ -n $mode ]] || choose_mode

# An explicitly requested app mode still needs a browser to drive it.
if [[ $mode == app && ${#browsers[@]} -eq 0 && -z $app_browser ]]; then
	echo "install.sh: --ui app needs a chromium-based browser, none found" >&2
	exit 1
fi
if [[ $mode == app && -z $app_browser ]]; then
	app_browser=${browsers[0]}
fi

# --- webview needs its headers before anything can be built -----------------
if [[ $mode == webview && $os_name == darwin ]]; then
	echo "install.sh: --ui webview is Linux/GTK only, not offered on macOS" >&2
	exit 1
fi
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

if [[ $os_name == darwin ]]; then
	echo "==> installing webssh.app and icons"
	make install-app-macos
else
	echo "==> installing desktop entry and icons"
	make install-desktop
fi

# Recorded in the database — the same place the Settings panel writes it, so
# the launcher and the UI can never disagree.
echo "==> saving the interface mode"
if [[ $mode == app ]]; then
	./webssh --set-ui-mode "$mode" --set-app-browser "$app_browser"
else
	./webssh --set-ui-mode "$mode"
fi

case "$mode" in
browser) how="a tab in your default browser" ;;
app) how="an app window via ${app_browser/#auto/a browser detected at startup}" ;;
webview) how="a native webview window" ;;
esac

if [[ $os_name == darwin ]]; then
	cat <<EOF

webssh is installed.
  binary   $PWD/webssh
  app      $HOME/Applications/webssh.app
  opens as $how

It should now show up in Launchpad and Spotlight, and can be pinned to the
Dock like any other app. The mode and the browser can be changed later in
Settings, or per run with '--ui browser|app'. Remove everything again with
'./uninstall.sh'.
EOF
else
	cat <<EOF

webssh is installed.
  binary   $PWD/webssh
  launcher ${XDG_DATA_HOME:-$HOME/.local/share}/applications/webssh.desktop
  opens as $how

It should now show up in the application menu; some desktops need a
re-login before a newly added launcher appears. The mode and the browser
can be changed later in Settings, or per run with
'--ui browser|app|webview'. Remove everything again with './uninstall.sh'.
EOF
fi

if ((run_after)); then
	echo
	echo "==> starting webssh"
	exec ./webssh
fi
