package appwin

import (
	"os"
	"os/exec"
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

// findChromium locates a chromium-family browser, which is what --app requires.
// Firefox is deliberately absent: it dropped -app years ago and has no
// equivalent chromeless window.
func findChromium() (string, bool) {
	for _, n := range chromiumNames {
		if p, err := exec.LookPath(n); err == nil {
			return p, true
		}
	}
	for _, p := range chromiumPaths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
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
