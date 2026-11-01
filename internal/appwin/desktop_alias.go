package appwin

import (
	"bytes"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// On Wayland, chromium ignores --class: the xdg app_id of an --app window is
// derived from the URL instead, e.g. "chrome-127.0.0.1__-Default" (Chromium)
// or "msedge-127.0.0.1__-Default" (Edge). Panels and docks find a window's
// icon by looking up a .desktop file named after its app_id, so without one
// the webssh window shows a generic placeholder icon. Chrome solves this the
// same way for its installed web apps: it writes <app_id>.desktop. webssh does
// likewise, as a hidden alias carrying the webssh name and icon.

// aliasMarker identifies alias entries webssh wrote, so it only ever rewrites
// (and the uninstallers only ever remove) its own files.
const aliasMarker = "X-Webssh-Alias=true"

// xdgPrefix returns the app_id prefix a chromium-family browser uses, judged
// from its executable name. Unknown builds fall back to Chromium's own prefix.
func xdgPrefix(exe string) string {
	base := strings.ToLower(filepath.Base(exe))
	switch {
	case strings.Contains(base, "edge"):
		return "msedge"
	case strings.Contains(base, "brave"):
		return "brave"
	case strings.Contains(base, "vivaldi"):
		return "vivaldi"
	default: // chromium, chromium-browser, google-chrome*, thorium, ...
		return "chrome"
	}
}

// waylandAppID reproduces chromium's app_id for an --app=rawURL window in the
// "Default" profile of its user-data-dir: <prefix>-<host>_<path>-Default, with
// the path's slashes turned into underscores (the port is not part of it).
func waylandAppID(exe, rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	name := strings.ReplaceAll(u.Hostname()+"_"+path, "/", "_")
	return xdgPrefix(exe) + "-" + name + "-Default", true
}

// desktopDir is where per-user .desktop files live.
func desktopDir() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "applications"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "applications"), nil
}

// aliasEntry renders the hidden alias for appID.
func aliasEntry(appID, execPath string) []byte {
	return []byte(fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=webssh
Comment=Local SSH control panel with terminal, key and mount management
Exec=%s
Icon=webssh
Terminal=false
NoDisplay=true
StartupWMClass=%s
%s
`, execPath, appID, aliasMarker))
}

// ensureDesktopAlias writes <app_id>.desktop for the window chromium is about
// to open, so it gets the webssh icon on Wayland. It never touches a file of
// that name it did not write itself, and failures only cost the icon.
func ensureDesktopAlias(exe, rawURL string) {
	if runtime.GOOS != "linux" {
		return
	}
	appID, ok := waylandAppID(exe, rawURL)
	if !ok {
		return
	}
	dir, err := desktopDir()
	if err != nil {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	if err := writeDesktopAlias(dir, appID, self); err != nil {
		log.Printf("app window icon: %v", err)
	}
}

func writeDesktopAlias(dir, appID, execPath string) error {
	path := filepath.Join(dir, appID+".desktop")
	want := aliasEntry(appID, execPath)
	if cur, err := os.ReadFile(path); err == nil {
		if bytes.Equal(cur, want) {
			return nil
		}
		if !bytes.Contains(cur, []byte(aliasMarker)) {
			return nil // someone else's entry (e.g. a real installed web app)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".webssh-alias-")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(want); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
