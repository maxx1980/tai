#!/usr/bin/env bash
# Installs a prebuilt webssh from GitHub releases. On Linux it registers it in
# the application menu; on macOS it assembles a webssh.app bundle in
# ~/Applications, so it shows up in Launchpad/Spotlight/Dock too. Everything
# lands under $HOME — no root, no system paths. Run it with --help for the
# options; see usage() below for the same text.
#
# This script is normally piped into bash, which means it has no file behind it:
# ${BASH_SOURCE[0]} is unset, so nothing here may read the script itself.
set -euo pipefail

REPO=${WEBSSH_REPO:-maxx1980/tai}
# Overridable so the download path can be pointed at a mirror — or at a local
# server, which is how it gets tested without publishing a release first.
API_BASE=${WEBSSH_API_BASE:-https://api.github.com}
DL_BASE=${WEBSSH_DL_BASE:-https://github.com}
BINARY=webssh
ICON_SIZES="16 24 32 48 64 128 256 512"

data_home=${XDG_DATA_HOME:-$HOME/.local/share}
prefix=${WEBSSH_PREFIX:-$HOME/.local}
version=""
mode=""
app_browser=""
from_dir=""
run_after=0
do_uninstall=0
skip_verify=0

die() {
	echo "get.sh: $*" >&2
	exit 1
}

usage() {
	cat <<EOF
Installs a prebuilt webssh. On Linux it registers it in the application menu;
on macOS it builds a webssh.app bundle in ~/Applications (Launchpad/Dock).

  curl -fsSL https://raw.githubusercontent.com/$REPO/main/get.sh | bash

Options come after '-s --', because the script is piped into bash:

  ... | bash -s -- --ui browser|app     how the interface opens (default: app
                                        when a chromium browser is installed)
  ... | bash -s -- --app-browser PATH   which browser hosts the app window
  ... | bash -s -- --version vX.Y.Z     install one specific release
  ... | bash -s -- --prefix DIR         install into DIR/bin (default ~/.local)
  ... | bash -s -- --run                start webssh once it is installed
  ... | bash -s -- --uninstall          remove binary, icons and launcher
  ... | bash -s -- --skip-verify        install without checking the checksum

Each release archive ships this script, so running it from an unpacked archive
installs the files next to it instead of downloading anything (--from DIR names
that directory explicitly).
EOF
	exit 0
}

need_value() {
	(($2 >= 2)) || die "$1 needs a value"
}

while (($#)); do
	case "$1" in
	--version) need_value "$1" $# && version=$2 && shift ;;
	--version=*) version=${1#--version=} ;;
	--prefix) need_value "$1" $# && prefix=$2 && shift ;;
	--prefix=*) prefix=${1#--prefix=} ;;
	--ui) need_value "$1" $# && mode=$2 && shift ;;
	--ui=*) mode=${1#--ui=} ;;
	--app-browser) need_value "$1" $# && app_browser=$2 && shift ;;
	--app-browser=*) app_browser=${1#--app-browser=} ;;
	--from) need_value "$1" $# && from_dir=$2 && shift ;;
	--from=*) from_dir=${1#--from=} ;;
	--run) run_after=1 ;;
	--uninstall) do_uninstall=1 ;;
	--skip-verify) skip_verify=1 ;;
	-h | --help) usage ;;
	*) die "unknown option '$1' (try --help)" ;;
	esac
	shift
done

bindir=$prefix/bin
icondir=$data_home/icons/hicolor
appdir=$data_home/applications
desktop=$appdir/$BINARY.desktop
# macOS .app bundle — a real Finder/Launchpad/Spotlight location, not tied to
# --prefix (that one only affects where the bare binary goes).
app_bundle=$HOME/Applications/$BINARY.app
lsregister=/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister

case "$(uname -s)" in
Linux) os_name=linux ;;
Darwin) os_name=darwin ;;
*) die "no prebuilt binary for $(uname -s) — build from source (see the README)" ;;
esac

# exe_of_pid resolves the path of a running process's executable. Linux has
# /proc; macOS does not, so `ps -o comm=` stands in — it prints the absolute
# path there, unlike Linux's ps.
exe_of_pid() {
	if [[ $os_name == linux ]]; then
		readlink "/proc/$1/exe" 2>/dev/null
	else
		ps -p "$1" -o comm= 2>/dev/null
	fi
}

# --- removal ----------------------------------------------------------------
# Kept here rather than in uninstall.sh: that one drives `make`, which a
# prebuilt install has no checkout for.
if ((do_uninstall)); then
	# Match on the executable, so someone else's "webssh" is never touched.
	for pid in $(pgrep -x "$BINARY" 2>/dev/null || true); do
		exe=$(exe_of_pid "$pid")
		[[ ${exe%% (deleted)} == "$bindir/$BINARY" ]] || continue
		echo "==> stopping webssh (pid $pid)"
		kill "$pid" 2>/dev/null || true
	done

	rm -f "$bindir/$BINARY" "$desktop" "$icondir/scalable/apps/$BINARY.svg"
	for n in $ICON_SIZES; do
		rm -f "$icondir/${n}x${n}/apps/$BINARY.png"
	done
	command -v gtk-update-icon-cache >/dev/null 2>&1 &&
		gtk-update-icon-cache -f -t "$icondir" >/dev/null 2>&1 || true
	command -v update-desktop-database >/dev/null 2>&1 &&
		update-desktop-database "$appdir" >/dev/null 2>&1 || true

	if [[ $os_name == darwin && -d $app_bundle ]]; then
		[[ -x $lsregister ]] && "$lsregister" -u "$app_bundle" >/dev/null 2>&1 || true
		rm -rf "$app_bundle"
	fi

	echo "webssh removed."
	echo "Your data is still in $data_home/$BINARY (inventory, keys, API key)."
	echo "Delete it with: rm -rf $data_home/$BINARY"
	exit 0
fi

# --- what we can install ----------------------------------------------------
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "no prebuilt binary for $(uname -m) — build from source (see the README)" ;;
esac

case "${mode:-}" in
"" | browser | app) ;;
webview)
	die "the prebuilt binary has no webview: it needs cgo and native GUI
       libraries, so it is built from source only — see the README's Build
       section ('./install.sh --ui webview' or 'make build-webview')."
	;;
*) die "--ui must be browser or app (got '$mode')" ;;
esac

if [[ -n $app_browser && $app_browser != auto && ! -x $app_browser ]]; then
	die "--app-browser '$app_browser' is not an executable file"
fi

for tool in tar install sed; do
	command -v "$tool" >/dev/null 2>&1 || die "missing required tool: $tool"
done

# --- fetching ---------------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
	fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
	fetch_stdout() { wget -qO- "$1"; }
else
	fetch() { die "need curl or wget to download"; }
	fetch_stdout() { die "need curl or wget to download"; }
fi

# Auto-detect an unpacked release archive next to this script, so the archive is
# installable offline. When the script is piped from curl its "directory" holds
# no payload and this correctly finds nothing.
if [[ -z $from_dir ]]; then
	# Empty when piped from curl — there is no script file to sit next to.
	self=${BASH_SOURCE[0]:-}
	here=""
	[[ -n $self ]] && here=$(CDPATH= cd -- "$(dirname -- "$self")" 2>/dev/null && pwd)
	if [[ -n $here && -f $here/$BINARY && -d $here/icons ]]; then
		from_dir=$here
	fi
fi

tmp=""
# The trailing `|| true` matters: an EXIT trap's own exit status overrides the
# script's under `set -e`, and "$tmp" is empty on the --from / unpacked-archive
# path (nothing downloaded), so the bare `[[ -n $tmp ]]` would make every such
# install report failure even though it fully succeeded.
cleanup() { [[ -n $tmp ]] && rm -rf "$tmp" || true; }
trap cleanup EXIT

if [[ -n $from_dir ]]; then
	src=$(CDPATH= cd -- "$from_dir" && pwd)
	[[ -f $src/$BINARY ]] || die "$src holds no webssh binary"
	echo "==> installing from $src"
else
	if [[ -z $version ]]; then
		echo "==> looking up the newest release of $REPO"
		# No jq on a stock desktop, and the tag name is the only field needed.
		version=$(fetch_stdout "$API_BASE/repos/$REPO/releases/latest" |
			sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1) ||
			die "could not reach the GitHub API"
		[[ -n $version ]] ||
			die "no published release found for $REPO — build from source (see the README)"
	fi

	name=$BINARY-$version-$os_name-$arch
	base=$DL_BASE/$REPO/releases/download/$version
	tmp=$(mktemp -d)

	echo "==> downloading $name.tar.gz"
	fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz" ||
		die "$version has no $arch archive — check https://github.com/$REPO/releases"

	if ((skip_verify)); then
		echo "==> skipping checksum verification (--skip-verify)"
	else
		# A checksum is worth having on a pipe-to-shell install: it is the one
		# check that the bytes arriving are the bytes that were released.
		sums=""
		for c in sha256sum "shasum -a 256"; do
			command -v "${c%% *}" >/dev/null 2>&1 && sums=$c && break
		done
		[[ -n $sums ]] || die "need sha256sum (coreutils) to verify the download; pass --skip-verify to install anyway"
		fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" ||
			die "$version publishes no SHA256SUMS; pass --skip-verify to install anyway"
		(cd "$tmp" && grep -F " $name.tar.gz" SHA256SUMS | $sums -c --status -) ||
			die "checksum mismatch on $name.tar.gz — the download is corrupt or tampered with"
		echo "==> checksum ok"
	fi

	tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
	src=$tmp/$name
	[[ -f $src/$BINARY ]] || die "the archive does not look like a webssh release"
fi

# --- install ----------------------------------------------------------------
echo "==> installing the binary into $bindir"
mkdir -p "$bindir"
# Write beside the target and rename: overwriting a file that is currently
# executing fails with ETXTBSY, and rename replaces it even while it runs.
install -m755 "$src/$BINARY" "$bindir/$BINARY.new"
mv -f "$bindir/$BINARY.new" "$bindir/$BINARY"

echo "==> installing icons"
# Not install -D: BSD install (macOS) lacks that GNU extension, so the target
# directory is created explicitly instead.
for n in $ICON_SIZES; do
	dst="$icondir/${n}x${n}/apps/$BINARY.png"
	mkdir -p "$(dirname "$dst")"
	install -m644 "$src/icons/$BINARY-$n.png" "$dst"
done
dst="$icondir/scalable/apps/$BINARY.svg"
mkdir -p "$(dirname "$dst")"
install -m644 "$src/icons/$BINARY.svg" "$dst"

if [[ $os_name == linux ]]; then
	echo "==> registering the application-menu launcher"
	mkdir -p "$appdir"
	# Exec must be an absolute path in a .desktop file.
	sed "s|@EXEC@|$bindir/$BINARY|" "$src/$BINARY.desktop.in" >"$desktop"
	chmod 644 "$desktop"

	command -v gtk-update-icon-cache >/dev/null 2>&1 &&
		gtk-update-icon-cache -f -t "$icondir" >/dev/null 2>&1 || true
	command -v update-desktop-database >/dev/null 2>&1 &&
		update-desktop-database "$appdir" >/dev/null 2>&1 || true
fi

# A release built before this bundle existed ships none of these three, so an
# older/pinned --version or an offline --from dir degrades to a plain binary
# instead of failing — same spirit as the "-" prefixes above ignoring missing
# gtk tools.
app_bundle_built=0
if [[ $os_name == darwin && -f $src/Info.plist.in && -f $src/macos-launcher.sh.in && -x $src/make-icns.sh ]]; then
	echo "==> building webssh.app"
	rm -rf "$app_bundle"
	mkdir -p "$app_bundle/Contents/MacOS" "$app_bundle/Contents/Resources"
	sed "s|@EXEC@|$bindir/$BINARY|" "$src/macos-launcher.sh.in" >"$app_bundle/Contents/MacOS/$BINARY"
	chmod 755 "$app_bundle/Contents/MacOS/$BINARY"
	ver=$("$bindir/$BINARY" --version | awk '{print $2}')
	sed "s|@VERSION@|$ver|" "$src/Info.plist.in" >"$app_bundle/Contents/Info.plist"
	"$src/make-icns.sh" "$src/icons/$BINARY" "$app_bundle/Contents/Resources/$BINARY.icns" ||
		echo "==> icns generation failed; webssh.app has no icon" >&2
	[[ -x $lsregister ]] && "$lsregister" -f "$app_bundle" >/dev/null 2>&1 || true
	app_bundle_built=1
fi

# --- how the interface opens ------------------------------------------------
# Recorded in the database, the same place the Settings panel writes it, so the
# launcher and the UI can never disagree. A pipe has no terminal to ask on, so
# the default is decided here and can be changed later in Settings.
find_chromium() {
	local c
	for c in google-chrome google-chrome-stable chromium chromium-browser \
		brave-browser microsoft-edge vivaldi-stable thorium-browser; do
		c=$(command -v "$c" 2>/dev/null) && echo "$c" && return 0
	done
	for c in /opt/google/chrome/google-chrome /opt/microsoft/msedge/microsoft-edge \
		/opt/brave.com/brave/brave-browser /opt/vivaldi/vivaldi \
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge" \
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser" \
		"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi" \
		"/Applications/Chromium.app/Contents/MacOS/Chromium"; do
		[[ -x $c ]] && echo "$c" && return 0
	done
	return 1
}

if [[ -z $mode ]]; then
	if found=$(find_chromium); then
		mode=app
		[[ -n $app_browser ]] || app_browser=$found
	else
		mode=browser
	fi
fi
if [[ $mode == app && -z $app_browser ]]; then
	app_browser=$(find_chromium) ||
		die "--ui app needs a chromium-based browser, none found"
fi

echo "==> saving the interface mode"
if [[ $mode == app ]]; then
	"$bindir/$BINARY" --set-ui-mode "$mode" --set-app-browser "$app_browser" >/dev/null
else
	"$bindir/$BINARY" --set-ui-mode "$mode" >/dev/null
fi

case "$mode" in
browser) how="a tab in your default browser" ;;
app) how="an app window via ${app_browser/#auto/a browser detected at startup}" ;;
esac

installed=$("$bindir/$BINARY" --version)

echo
echo "${installed} is installed."
echo "  binary   $bindir/$BINARY"
[[ $os_name == linux ]] && echo "  launcher $desktop"
[[ $os_name == darwin && $app_bundle_built == 1 ]] && echo "  app      $app_bundle"
echo "  icons    $icondir/<size>/apps/$BINARY.png"
echo "  data     $data_home/$BINARY"
echo "  opens as $how"
echo

if [[ $os_name == linux ]]; then
	cat <<EOF
It should now show up in the application menu; some desktops need a re-login
before a newly added launcher appears. The mode can be changed in Settings, or
per run with '--ui browser|app'. Remove everything again with:

  curl -fsSL https://raw.githubusercontent.com/$REPO/main/get.sh | bash -s -- --uninstall
EOF
elif ((app_bundle_built)); then
	cat <<EOF
It should now show up in Launchpad and Spotlight, and can be pinned to the
Dock like any other app. The mode can be changed in Settings, or per run with
'--ui browser|app'. Remove everything again with:

  curl -fsSL https://raw.githubusercontent.com/$REPO/main/get.sh | bash -s -- --uninstall
EOF
else
	cat <<EOF
This release predates the webssh.app bundle, so there is no Launchpad/Dock
integration yet — run it from a shell:

  $bindir/$BINARY

Re-run this installer once a newer release is published to get the app
bundle. The mode can be changed in Settings, or per run with
'--ui browser|app'. Remove everything again with:

  curl -fsSL https://raw.githubusercontent.com/$REPO/main/get.sh | bash -s -- --uninstall
EOF
fi

case ":$PATH:" in
*":$bindir:"*) ;;
*)
	cat <<EOF

Note: $bindir is not on your PATH, so 'webssh' will not work as a command.
The launcher does not care (it uses the absolute path), but to run it from a
shell add this to your ~/.profile or ~/.zshrc:

  export PATH="$bindir:\$PATH"
EOF
	;;
esac

# An already-running copy keeps executing the old binary until it is restarted.
for pid in $(pgrep -x "$BINARY" 2>/dev/null || true); do
	exe=$(exe_of_pid "$pid")
	if [[ ${exe%% (deleted)} == "$bindir/$BINARY" ]]; then
		echo
		echo "An older webssh is still running (pid $pid); restart it to pick this one up."
		break
	fi
done

if ((run_after)); then
	echo
	echo "==> starting webssh"
	exec "$bindir/$BINARY"
fi
