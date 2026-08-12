#!/usr/bin/env bash
# Removes webssh from the Linux desktop.
#
#   ./uninstall.sh            stop the daemon, unmount sshfs, drop launcher + binary
#   ./uninstall.sh --purge    ...and delete the database, keys and ~/.ssh integration
#   ./uninstall.sh --yes      do not ask for confirmation (required for --purge in a pipe)
#
# Without --purge nothing you own is destroyed: the inventory database and the
# managed ssh config are left in place, so reinstalling picks up where you left off.
set -euo pipefail

cd "$(dirname -- "${BASH_SOURCE[0]}")"
repo=$PWD

case "$(uname -s)" in
Darwin) os_name=darwin ;;
*) os_name=linux ;;
esac

data_dir=${XDG_DATA_HOME:-$HOME/.local/share}/webssh
mount_base=${WEBSSH_MOUNT_BASE:-$HOME/mnt/webssh}
managed_config=$HOME/.ssh/config.d/inventory
ssh_config=$HOME/.ssh/config

purge=0
assume_yes=0
for arg in "$@"; do
	case "$arg" in
	--purge) purge=1 ;;
	-y | --yes) assume_yes=1 ;;
	-h | --help)
		sed -n '2,9p' "${BASH_SOURCE[0]}" | cut -c3-
		exit 0
		;;
	*)
		echo "uninstall.sh: unknown option '$arg' (try --help)" >&2
		exit 2
		;;
	esac
done

confirm() {
	((assume_yes)) && return 0
	if [[ ! -t 0 ]]; then
		echo "uninstall.sh: not a terminal, pass --yes to confirm" >&2
		exit 1
	fi
	local reply
	read -r -p "$1 [y/N] " reply
	[[ $reply == [yY] || $reply == [yY][eE][sS] ]]
}

# --- 1. stop the daemon -----------------------------------------------------
# Match on the executable rather than the name, so an unrelated "webssh" of
# someone else's is never touched. Linux has /proc; macOS does not, so
# `ps -o comm=` stands in there (it prints the absolute path, unlike Linux's ps).
exe_of_pid() {
	if [[ $os_name == darwin ]]; then
		ps -p "$1" -o comm= 2>/dev/null
	else
		readlink "/proc/$1/exe" 2>/dev/null
	fi
}

stopped=0
for pid in $(pgrep -x webssh 2>/dev/null || true); do
	exe=$(exe_of_pid "$pid")
	[[ ${exe%% (deleted)} == "$repo/webssh" ]] || continue
	echo "==> stopping webssh (pid $pid)"
	kill "$pid" 2>/dev/null || true
	for _ in $(seq 1 20); do
		kill -0 "$pid" 2>/dev/null || break
		sleep 0.25
	done
	kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null || true
	stopped=1
done
((stopped)) || echo "==> webssh is not running"

# --- 2. unmount sshfs -------------------------------------------------------
# Must happen before anything is deleted: rm -rf over a live sshfs mount would
# start deleting files on the remote host.
unmount_one() {
	fusermount3 -u "$1" 2>/dev/null ||
		fusermount -u "$1" 2>/dev/null ||
		umount "$1" 2>/dev/null
}

# find_sshfs_mounts lists active mounts under mount_base. Linux reads
# /proc/mounts directly; macOS has no /proc, so it parses the BSD `mount`
# command's text output instead (mirrors internal/mount/mount_darwin.go —
# macFUSE mounts show "macfuse"/"osxfuse" among the parenthesized options).
find_sshfs_mounts() {
	if [[ $os_name == darwin ]]; then
		mount | grep -i fuse | sed -n 's/.* on \(.*\) (.*/\1/p' |
			grep -F "$mount_base/" || true
	else
		awk -v base="$mount_base/" '$3 ~ /^fuse\.sshfs$/ && index($2, base) == 1 {print $2}' /proc/mounts
	fi
}

while read -r mp; do
	[[ -n $mp ]] || continue
	echo "==> unmounting $mp"
	unmount_one "$mp" || echo "    failed - unmount it yourself, then re-run" >&2
done < <(find_sshfs_mounts)

if [[ $os_name == darwin ]]; then
	mount | grep -qF " on $mount_base (" && {
		echo "==> unmounting $mount_base"
		unmount_one "$mount_base" || true
	}
elif mountpoint -q "$mount_base" 2>/dev/null; then
	echo "==> unmounting $mount_base"
	unmount_one "$mount_base" || true
fi

# --- 3. desktop entry / app bundle, icons, binary ---------------------------
if [[ $os_name == darwin ]]; then
	echo "==> removing webssh.app and icons"
	make uninstall-app-macos >/dev/null
else
	echo "==> removing launcher and icons"
	make uninstall-desktop >/dev/null
fi

echo "==> removing built binaries"
rm -f "$repo/webssh" "$repo/webssh.exe"

# --- 4. user data (only with --purge) --------------------------------------
if ((purge)); then
	echo
	echo "--purge will permanently delete:"
	[[ -d $data_dir ]] && echo "  $data_dir (inventory database, API key, deploy keys)"
	[[ -f $managed_config ]] && echo "  $managed_config"
	grep -qF "$managed_config" "$ssh_config" 2>/dev/null &&
		echo "  the 'Include $managed_config' line in $ssh_config"
	echo

	if confirm "Delete this data?"; then
		rm -rf "$data_dir"
		rm -f "$managed_config"

		# Drop the Include line webssh added, keeping a timestamped backup of the
		# config in case anything else lived on that line.
		if grep -qF "$managed_config" "$ssh_config" 2>/dev/null; then
			backup=$ssh_config.webssh-uninstall.$(date +%Y%m%d%H%M%S)
			cp -p "$ssh_config" "$backup"
			grep -vF "Include $managed_config" "$ssh_config" >"$ssh_config.tmp"
			cat "$ssh_config.tmp" >"$ssh_config"
			rm -f "$ssh_config.tmp"
			echo "==> removed the Include line (backup: $backup)"
		fi

		rmdir "$mount_base" 2>/dev/null || true
		echo "==> user data deleted"
	else
		echo "==> keeping user data"
	fi
fi

echo
echo "webssh uninstalled."
if ((purge)); then
	echo "The source checkout in $repo is untouched; delete it manually if you want it gone."
else
	echo "Your data is still in $data_dir - re-run with --purge to remove it."
fi
