package appwin

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// chromiumNames are tried on PATH, most-preferred first.
var chromiumNames = []string{
	"google-chrome",
	"google-chrome-stable",
	"chromium",
	"chromium-browser",
	"brave-browser",
	"microsoft-edge",
	"vivaldi-stable",
	"thorium-browser",
}

// chromiumPaths cover installs that ship a .desktop file but never land on
// PATH, which is how Chrome and Edge package themselves on Debian.
var chromiumPaths = []string{
	"/opt/google/chrome/google-chrome",
	"/opt/microsoft/msedge/microsoft-edge",
	"/opt/brave.com/brave/brave-browser",
	"/opt/vivaldi/vivaldi",
}

// isExecutableFile reports whether p is a runnable file (not a directory).
func isExecutableFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// FindChromiumAll returns every chromium-family browser on this machine, in
// preference order and deduplicated by real path — /usr/bin/microsoft-edge is
// typically a symlink to the copy under /opt, and offering both would be noise.
// Exported so the installer can present the same list the daemon would use.
func FindChromiumAll() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			real = p
		}
		if seen[real] {
			return
		}
		seen[real] = true
		out = append(out, p)
	}
	for _, n := range chromiumNames {
		if p, err := exec.LookPath(n); err == nil {
			add(p)
		}
	}
	for _, p := range chromiumPaths {
		if isExecutableFile(p) {
			add(p)
		}
	}
	return out
}

// findChromium picks the browser to drive the app window: the pinned one when
// it is still usable, otherwise the first one detected. Firefox is deliberately
// absent from the lists — it dropped -app years ago and has no equivalent
// chromeless window.
func findChromium(pinned string) (string, bool) {
	if pinned != "" {
		if isExecutableFile(pinned) {
			return pinned, true
		}
		if p, err := exec.LookPath(pinned); err == nil {
			return p, true
		}
		log.Printf("configured app browser %q is not executable; detecting one instead", pinned)
	}
	if all := FindChromiumAll(); len(all) > 0 {
		return all[0], true
	}
	return "", false
}

// chromiumUI opens the interface in a chromeless window backed by its own
// browser profile.
type chromiumUI struct {
	exe        string
	profileDir string
}

func (c *chromiumUI) Blocking() bool { return false }
func (c *chromiumUI) Close()         {}

func (c *chromiumUI) Open(rawURL string) error {
	args := []string{
		"--app=" + rawURL,
		// A private profile is what makes this feel like an application rather
		// than a tab: its own cookie jar (the auth token lives in one), no
		// extensions, no interference with the user's browsing session.
		"--user-data-dir=" + c.profileDir,
		// Without this chromium invents a WM_CLASS from the URL, so the window
		// would not group under the webssh icon (see StartupWMClass in the
		// .desktop file).
		"--class=webssh",
		"--no-first-run",
		"--no-default-browser-check",
	}
	return exec.Command(c.exe, args...).Start()
}
