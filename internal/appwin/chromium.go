package appwin

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
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
// PATH, which is how Chrome and Edge package themselves on Debian — plus the
// macOS .app bundle executables, which are never on PATH at all.
var chromiumPaths = []string{
	"/opt/google/chrome/google-chrome",
	"/opt/microsoft/msedge/microsoft-edge",
	"/opt/brave.com/brave/brave-browser",
	"/opt/vivaldi/vivaldi",
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
}

// windowsChromiumPaths mirrors chromiumPaths for Windows: chrome.exe/
// msedge.exe and friends are never added to PATH by their installers (there
// is no Linux-style packaging convention for that here), so chromiumNames'
// PATH lookup alone would never find them - only explicit paths do. Edge in
// particular ships inbox with every modern Windows install, which is what
// makes app-window mode available there by default without anything extra.
func windowsChromiumPaths() []string {
	dir := func(envVar, rel string) string {
		if v := os.Getenv(envVar); v != "" {
			return filepath.Join(v, rel)
		}
		return ""
	}
	var out []string
	for _, p := range []string{
		dir("ProgramFiles", `Google\Chrome\Application\chrome.exe`),
		dir("ProgramFiles(x86)", `Google\Chrome\Application\chrome.exe`),
		dir("LocalAppData", `Google\Chrome\Application\chrome.exe`),
		dir("ProgramFiles", `BraveSoftware\Brave-Browser\Application\brave.exe`),
		dir("LocalAppData", `BraveSoftware\Brave-Browser\Application\brave.exe`),
		// Edge installs 32-bit under Program Files (x86) even on 64-bit
		// Windows - a known quirk, and the entry most likely to actually hit.
		dir("ProgramFiles(x86)", `Microsoft\Edge\Application\msedge.exe`),
		dir("ProgramFiles", `Microsoft\Edge\Application\msedge.exe`),
		dir("ProgramFiles", `Vivaldi\Application\vivaldi.exe`),
		dir("LocalAppData", `Vivaldi\Application\vivaldi.exe`),
	} {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isExecutableFile reports whether p is a runnable file (not a directory).
// Windows has no POSIX executable bit to check - FileInfo.Mode() never sets
// any of the 0o111 bits there, so existence is the only signal available
// (and enough, since every candidate path already ends in .exe).
func isExecutableFile(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return st.Mode()&0o111 != 0
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
	paths := chromiumPaths
	if runtime.GOOS == "windows" {
		paths = windowsChromiumPaths()
	}
	for _, p := range paths {
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
	proc       *os.Process // the instance Open launched, if any
}

func (c *chromiumUI) Blocking() bool { return false }
func (c *chromiumUI) Mode() Mode     { return ModeApp }

func (c *chromiumUI) Open(rawURL string) error {
	killStaleProfileHolder(c.profileDir)
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
	cmd := exec.Command(c.exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	c.proc = cmd.Process
	return nil
}

// Close terminates the chromium instance this UI launched, if it is still
// running. The daemon calls this on every shutdown path, not just closing the
// window: without it, the process this Open call started outlives the daemon
// indefinitely on macOS (see killStaleProfileHolder), which contradicts the
// "quits with the browser... does not linger in the background" promise —
// the browser instance kept lingering even though the daemon itself had
// already exited on schedule.
func (c *chromiumUI) Close() {
	if c.proc == nil {
		return
	}
	terminate(c.proc)
}

// terminate asks proc to exit via SIGTERM, falling back to Kill (the only
// signal Go supports on every OS) on platforms where SIGTERM is not
// implemented (Windows) or the process ignores it.
func terminate(proc *os.Process) {
	if proc.Signal(syscall.SIGTERM) != nil {
		_ = proc.Kill()
		return
	}
	done := make(chan struct{})
	go func() { _, _ = proc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = proc.Kill()
	}
}

// killStaleProfileHolder terminates whatever process still holds profileDir's
// Chromium SingletonLock, if any.
//
// Closing the last window of an --app instance quits the chromium process on
// Linux, so the profile is free by the time webssh is launched again. macOS
// follows normal app-lifecycle rules instead: the process lingers with no
// window after Cmd+W/the close button. A later launch against the same
// --user-data-dir then finds the lock already held, and chromium's own
// process-singleton behaviour forwards the new --app=URL to that background
// instance as an ordinary tab rather than starting a fresh chromeless window —
// which is what "relaunching opens an empty/wrong Chrome window" turned out
// to be, confirmed live: the forwarded tab connects to the daemon fine (so
// the app-window-didn't-connect fallback below never fires either), it just
// never gets the --app treatment. profileDir is a browser profile webssh
// creates and uses exclusively for this window, never the user's real Chrome
// profile, so killing whatever holds its lock is always safe.
func killStaleProfileHolder(profileDir string) {
	lock := filepath.Join(profileDir, "SingletonLock")
	target, err := os.Readlink(lock)
	if err != nil {
		return
	}
	// The symlink target is "<hostname>-<pid>"; the hostname itself may
	// contain hyphens, so split on the last one.
	i := strings.LastIndexByte(target, '-')
	if i < 0 {
		return
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	// SIGTERM lets chromium run its own shutdown, which is what actually
	// removes the lock; Kill (the only signal Go supports on every OS) covers
	// platforms where SIGTERM is not implemented (Windows).
	if proc.Signal(syscall.SIGTERM) != nil {
		_ = proc.Kill()
	}
	for i := 0; i < 40; i++ {
		if _, err := os.Lstat(lock); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Still there after 2s (a hung process, or Kill's cleanup never ran) — a
	// stale lock left behind blocks every future launch, so drop it directly;
	// it is only ever a symlink webssh's own chromium instances create.
	_ = os.Remove(lock)
}

// killStaleProfileHolder terminates whatever process still holds profileDir's
// Chromium SingletonLock, if any.
//
// Closing the last window of an --app instance quits the chromium process on
// Linux, so the profile is free by the time webssh is launched again. macOS
// follows normal app-lifecycle rules instead: the process lingers with no
// window after Cmd+W/the close button. A later launch against the same
// --user-data-dir then finds the lock already held, and chromium's own
// process-singleton behaviour forwards the new --app=URL to that background
// instance as an ordinary tab rather than starting a fresh chromeless window —
// which is what "relaunching opens an empty/wrong Chrome window" turned out
// to be, confirmed live: the forwarded tab connects to the daemon fine (so
// the app-window-didn't-connect fallback below never fires either), it just
// never gets the --app treatment. profileDir is a browser profile webssh
// creates and uses exclusively for this window, never the user's real Chrome
// profile, so killing whatever holds its lock is always safe.
func killStaleProfileHolder(profileDir string) {
	lock := filepath.Join(profileDir, "SingletonLock")
	target, err := os.Readlink(lock)
	if err != nil {
		return
	}
	// The symlink target is "<hostname>-<pid>"; the hostname itself may
	// contain hyphens, so split on the last one.
	i := strings.LastIndexByte(target, '-')
	if i < 0 {
		return
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	// SIGTERM lets chromium run its own shutdown, which is what actually
	// removes the lock; Kill (the only signal Go supports on every OS) covers
	// platforms where SIGTERM is not implemented (Windows).
	if proc.Signal(syscall.SIGTERM) != nil {
		_ = proc.Kill()
	}
	for i := 0; i < 40; i++ {
		if _, err := os.Lstat(lock); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Still there after 2s (a hung process, or Kill's cleanup never ran) — a
	// stale lock left behind blocks every future launch, so drop it directly;
	// it is only ever a symlink webssh's own chromium instances create.
	_ = os.Remove(lock)
}
