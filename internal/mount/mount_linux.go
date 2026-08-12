//go:build linux

package mount

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"webssh/internal/store"
)

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
