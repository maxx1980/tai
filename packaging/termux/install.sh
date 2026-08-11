#!/data/data/com.termux/files/usr/bin/bash
# Installs webssh inside Termux by building it from source with Termux's own
# Go toolchain. This is deliberate, not a fallback: no release *binary* runs
# under Termux's bionic dynamic linker on modern Android. A plain
# CGO_ENABLED=0 GOOS=linux build is rejected outright (non-PIE), and even a
# PIE rebuild fails bionic's TLS-segment alignment check. The only binary
# that works is one built with GOOS=android CGO_ENABLED=1 — exactly what
# `pkg install golang` defaults to inside Termux — so this script installs
# Go, openssh (the ssh client webssh's terminal shells out to), fetches the
# matching source tarball (web/dist is prebuilt in it, so Node is never
# needed here) and builds it on-device. A cold build compiles the whole
# module graph and can take a couple of minutes; later re-runs are faster
# (Go's build cache survives between them).
#
# This script is normally piped into bash, which means it has no file behind
# it: ${BASH_SOURCE[0]} is unset, so nothing here may read the script itself.
set -euo pipefail

REPO=${WEBSSH_REPO:-maxx1980/tai}
API_BASE=${WEBSSH_API_BASE:-https://api.github.com}
DL_BASE=${WEBSSH_DL_BASE:-https://github.com}
BINARY=webssh

version=""
from_dir=""
run_after=0
do_uninstall=0
skip_verify=0

die() {
	echo "install.sh: $*" >&2
	exit 1
}

usage() {
	cat <<EOF
Builds and installs webssh inside Termux from source.

  curl -fsSL https://raw.githubusercontent.com/$REPO/main/packaging/termux/install.sh | bash

Options come after '-s --', because the script is piped into bash:

  ... | bash -s -- --version vX.Y.Z     install one specific release
  ... | bash -s -- --run                start webssh once it is installed
  ... | bash -s -- --uninstall          remove the binary and widget shortcut
  ... | bash -s -- --skip-verify        install without checking the checksum

Each release ships a $BINARY-vX.Y.Z-termux-src.tar.gz; running this script
from an unpacked copy of it installs from there instead of downloading
(--from DIR names that directory explicitly).
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

[[ -n ${TERMUX_VERSION:-} || ${PREFIX:-} == *com.termux* ]] ||
	die "this installer is for Termux on Android — see the README for other platforms"

bindir="$PREFIX/bin"
shortcutdir="$HOME/.shortcuts"

# --- removal ------------------------------------------------------------
if ((do_uninstall)); then
	for pid in $(pgrep -x "$BINARY" 2>/dev/null || true); do
		echo "==> stopping webssh (pid $pid)"
		kill "$pid" 2>/dev/null || true
	done
	rm -f "$bindir/$BINARY" "$shortcutdir/$BINARY.sh"
	echo "webssh removed."
	echo "Your data is still in \$HOME/.local/share/$BINARY (inventory, keys, API key)."
	echo "Delete it with: rm -rf \$HOME/.local/share/$BINARY"
	exit 0
fi

# --- what we need -------------------------------------------------------
echo "==> installing openssh and golang (skipped if already present)"
pkg install -y openssh golang >/dev/null

for tool in tar go; do
	command -v "$tool" >/dev/null 2>&1 || die "missing required tool: $tool (pkg install failed?)"
done

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
	fetch_stdout() { curl -fsSL "$1"; }
else
	fetch() { die "need curl to download"; }
	fetch_stdout() { die "need curl to download"; }
fi

tmp=""
cleanup() { [[ -n $tmp ]] && rm -rf "$tmp"; }
trap cleanup EXIT

if [[ -z $from_dir ]]; then
	self=${BASH_SOURCE[0]:-}
	here=""
	[[ -n $self ]] && here=$(CDPATH= cd -- "$(dirname -- "$self")" 2>/dev/null && pwd)
	if [[ -n $here && -f $here/go.mod && -d $here/cmd/$BINARY ]]; then
		from_dir=$here
	fi
fi

if [[ -n $from_dir ]]; then
	src=$(CDPATH= cd -- "$from_dir" && pwd)
	[[ -f $src/go.mod ]] || die "$src holds no webssh source tree"
	echo "==> building from $src"
else
	if [[ -z $version ]]; then
		echo "==> looking up the newest release of $REPO"
		version=$(fetch_stdout "$API_BASE/repos/$REPO/releases/latest" |
			sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1) ||
			die "could not reach the GitHub API"
		[[ -n $version ]] ||
			die "no published release found for $REPO — build from source (see the README)"
	fi

	name=$BINARY-$version-termux-src
	base=$DL_BASE/$REPO/releases/download/$version
	tmp=$(mktemp -d)

	echo "==> downloading $name.tar.gz"
	fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz" ||
		die "$version has no termux-src archive — check https://github.com/$REPO/releases"

	if ((skip_verify)); then
		echo "==> skipping checksum verification (--skip-verify)"
	else
		command -v sha256sum >/dev/null 2>&1 || die "need sha256sum to verify the download; pass --skip-verify to install anyway"
		fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" ||
			die "$version publishes no SHA256SUMS; pass --skip-verify to install anyway"
		(cd "$tmp" && grep -F " $name.tar.gz" SHA256SUMS | sha256sum -c --status -) ||
			die "checksum mismatch on $name.tar.gz — the download is corrupt or tampered with"
		echo "==> checksum ok"
	fi

	tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
	src=$tmp/$name
	[[ -f $src/go.mod ]] || die "the archive does not look like a webssh source tree"
fi

# --- build ----------------------------------------------------------------
echo "==> building webssh (first build compiles everything — a couple of minutes)"
( cd "$src" && go build -trimpath -ldflags "-s -w" -o "$bindir/$BINARY.new" ./cmd/webssh )
mv -f "$bindir/$BINARY.new" "$bindir/$BINARY"

echo "==> installing the one-tap launcher"
mkdir -p "$shortcutdir"
install -m755 "$src/packaging/termux/webssh.sh" "$shortcutdir/$BINARY.sh"

installed=$("$bindir/$BINARY" --version)

cat <<EOF

$installed is installed.
  binary    $bindir/$BINARY
  launcher  $shortcutdir/$BINARY.sh
  data      \$HOME/.local/share/$BINARY

Run it with '$BINARY', or install the Termux:Widget app
(https://f-droid.org/packages/com.termux.widget/) and add a widget — it will
offer '$BINARY.sh' as a one-tap shortcut that starts the daemon and opens its
URL. Auto-opening needs 'pkg install termux-api' (and the Termux:API app);
without it the URL is printed instead — tap it in the terminal.

Remove everything again with:

  curl -fsSL https://raw.githubusercontent.com/$REPO/main/packaging/termux/install.sh | bash -s -- --uninstall
EOF

if ((run_after)); then
	echo
	echo "==> starting webssh"
	exec "$bindir/$BINARY"
fi
