// Package mount manages sshfs mounts of remote home directories.
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
func Mount(baseDir string, h store.Host, password string) (string, error) {
	point := pointFor(baseDir, h)
	if isMounted(point) {
		return point, nil
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
	// reaches the distro as a different uid than the one that ran sshfs, and
	// the kernel's FUSE layer denies access to anyone but the mounting user
	// unless allow_other is set — without it the mount looks empty (and
	// writes silently go nowhere) from Windows while `ls` inside WSL is fine.
	// Harmless on native Linux, where the mounting user is the only viewer anyway.
	args := []string{remote + ":", point,
		"-o", "reconnect,ServerAliveInterval=15,ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "allow_other"}
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

func looksLikeAuthFailure(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "permission denied") ||
		strings.Contains(m, "publickey") ||
		strings.Contains(m, "authentication") ||
		strings.Contains(m, "read: connection reset")
}

// Unmount unmounts the sshfs mount for h.
func Unmount(baseDir string, h store.Host) error {
	point := pointFor(baseDir, h)
	cmd := exec.Command("fusermount", "-u", point)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fusermount: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// List returns all active sshfs mounts under baseDir.
func List(baseDir string) ([]Active, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Active
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		source, point, fstype := fields[0], unescape(fields[1]), fields[2]
		if !strings.HasPrefix(fstype, "fuse.sshfs") {
			continue
		}
		if baseDir != "" && !strings.HasPrefix(point, baseDir) {
			continue
		}
		out = append(out, Active{Mountpoint: point, Source: source})
	}
	return out, sc.Err()
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

// unescape decodes octal escapes (\040 etc.) used in /proc/mounts paths.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			var n int
			if _, err := fmt.Sscanf(s[i+1:i+4], "%o", &n); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
