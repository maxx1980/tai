#!/data/data/com.termux/files/usr/bin/bash
# One-tap launcher for the Termux:Widget app. install.sh already copies this
# to ~/.shortcuts/webssh.sh; long-press the Android home screen, add a
# Termux:Widget widget, and pick "webssh" to use it. Starts the daemon in the
# background, waits for the token URL it prints on startup, and opens it with
# termux-open-url (from the optional termux-api package) — or a toast if that
# package isn't installed, since a widget tap has no terminal to print the
# URL to. Assumes `webssh` is on PATH (install.sh puts it in $PREFIX/bin).
set -euo pipefail

log="${PREFIX:-/data/data/com.termux/files/usr}/tmp/webssh-widget.log"
: >"$log"

nohup webssh >"$log" 2>&1 &
disown

url=""
for _ in $(seq 1 50); do
	url=$(grep -oE 'https?://[^ ]+\?token=[^ ]+' "$log" | head -1 || true)
	[ -n "$url" ] && break
	sleep 0.2
done

if [ -z "$url" ]; then
	command -v termux-toast >/dev/null 2>&1 && termux-toast "webssh: no URL yet — check $log"
	exit 1
fi

if command -v termux-open-url >/dev/null 2>&1; then
	termux-open-url "$url"
else
	command -v termux-toast >/dev/null 2>&1 && termux-toast "webssh ready: $url"
fi
