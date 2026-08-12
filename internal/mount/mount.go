// Package mount manages sshfs mounts of remote home directories.
//
// List and Unmount are platform-specific (mount_linux.go, mount_darwin.go):
// Linux reads /proc/mounts and unmounts via fusermount, macOS parses the BSD
// `mount` command's text output and unmounts via umount.
package mount

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"webssh/internal/store"
)

// Active is an sshfs mount currently reported by the system.
type Active struct {
	Mountpoint string `json:"mountpoint"`
	Source     string `json:"source"`
}

// pointFor returns the mountpoint for a host under baseDir.
func pointFor(baseDir string, h store.Host) string {
	return filepath.Join(baseDir, h.Alias)
}

// Mountpoint exposes the computed mountpoint (used by the launcher for files).
func Mountpoint(baseDir string, h store.Host) string { return pointFor(baseDir, h) }

// ErrNeedPassword is returned when a passwordless mount fails to authenticate,
// signalling the caller to retry with a password supplied from the web UI.
var ErrNeedPassword = errors.New("authentication failed — a password or deployed key is required")

// Mount mounts the remote home directory of h at baseDir/<alias> and returns
// the mountpoint. If already mounted it is a no-op.
//
// password is optional: when empty, the mount runs non-interactively
// (BatchMode) using key auth and never prompts on the daemon's console; when
// set, it is fed to ssh via sshfs's password_stdin so the prompt stays in the web.
//
// On macOS this additionally needs macFUSE installed (`brew install
// macfuse sshfs`) — there is no way to detect that ahead of the call, so a
// missing macFUSE just surfaces as the sshfs command failing.
func Mount(baseDir string, h store.Host, password string) (string, error) {
	point := pointFor(baseDir, h)
	if isMounted(point) {
		return point, nil
	}
	if _, err := exec.LookPath("sshfs"); err != nil {
		return "", errors.New("sshfs is not available on this platform")
	}
	if err := os.MkdirAll(point, 0o700); err != nil {
		return "", err
	}
	remote := h.Hostname
	if remote == "" {
		remote = h.Alias // fall back to ssh_config alias
	}
	if h.User != "" {
		remote = h.User + "@" + remote
	}
	// ":" (empty path) mounts the remote login home directory.
	// accept-new avoids a blocking host-key prompt on the daemon console.
	// allow_other is needed under WSL: \\wsl$\ (and any drive mapped onto it)
	// reaches the distro as a different uid than the one that ran sshfs. FUSE
	// refuses that option for non-root users unless /etc/fuse.conf explicitly
	// enables user_allow_other, so only request it when the host permits it.
	args := []string{remote + ":", point,
		"-o", "reconnect,ServerAliveInterval=15,ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new"}
	if fuseAllowsOther("/etc/fuse.conf") {
		args = append(args, "-o", "allow_other")
	}
	if h.Port != 0 && h.Port != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", h.Port))
	}
	if h.IdentityFile != "" {
		args = append(args, "-o", "IdentityFile="+h.IdentityFile)
	}
	cmd := exec.Command("sshfs", args...)
	if password != "" {
		cmd.Args = append(cmd.Args, "-o", "password_stdin")
		cmd.Stdin = strings.NewReader(password + "\n")
	} else {
		// No password: never prompt on the console; fail fast if key auth can't work.
		cmd.Args = append(cmd.Args, "-o", "BatchMode=yes")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if password == "" && looksLikeAuthFailure(msg) {
			return "", ErrNeedPassword
		}
		return "", fmt.Errorf("sshfs: %v: %s", err, msg)
	}
	return point, nil
}

// fuseAllowsOther reports whether fuse.conf contains an active
// user_allow_other directive. Comments and surrounding whitespace are ignored.
func fuseAllowsOther(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if before, _, found := strings.Cut(line, "#"); found {
			line = before
		}
		if strings.TrimSpace(line) == "user_allow_other" {
			return true
		}
	}
	return false
}

func looksLikeAuthFailure(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "permission denied") ||
		strings.Contains(m, "publickey") ||
		strings.Contains(m, "authentication") ||
		strings.Contains(m, "read: connection reset")
}

func isMounted(point string) bool {
	mounts, err := List("")
	if err != nil {
		return false
	}
	for _, m := range mounts {
		if m.Mountpoint == point {
			return true
		}
	}
	return false
}
