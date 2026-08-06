//go:build darwin

package mount

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"

	"webssh/internal/store"
)

// Unmount unmounts the sshfs mount for h. A macFUSE mount responds to the
// plain umount(8) like any other filesystem; diskutil is only needed for
// mounts the Finder itself is holding open, which an sshfs mount never is.
func Unmount(baseDir string, h store.Host) error {
	point := pointFor(baseDir, h)
	cmd := exec.Command("umount", point)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("umount: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// List returns all active sshfs mounts under baseDir, parsed from the BSD
// `mount` command's text output — macOS has no /proc, and macFUSE mounts show
// up in it with "macfuse" (older installs: "osxfuse") among the parenthesized
// options, e.g.:
//
//	user@host: on /Users/x/mnt/webssh/box1 (macfuse, nodev, nosuid, mounted by x)
func List(baseDir string) ([]Active, error) {
	out, err := exec.Command("mount").Output()
	if err != nil {
		return nil, err
	}
	var res []Active
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		onIdx := strings.Index(line, " on ")
		openParen := strings.LastIndex(line, " (")
		if onIdx < 0 || openParen < onIdx {
			continue
		}
		source := line[:onIdx]
		point := line[onIdx+len(" on ") : openParen]
		opts := strings.ToLower(line[openParen:])
		if !strings.Contains(opts, "fuse") {
			continue
		}
		if baseDir != "" && !strings.HasPrefix(point, baseDir) {
			continue
		}
		res = append(res, Active{Mountpoint: point, Source: source})
	}
	return res, sc.Err()
}
