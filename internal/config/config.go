// Package config resolves filesystem paths and default settings for webssh.
package config

import (
	"os"
	"path/filepath"
)

// Setting keys stored in the DB.
const (
	KeyTerminalCmd  = "terminal_cmd"
	KeyFilesCmd     = "files_cmd"
	KeyBrowserCmd   = "browser_cmd"
	KeyMountBaseDir = "mount_base_dir"
	KeyTheme        = "theme"

	// KeyTelnetBackspace selects what the Backspace key sends in a telnet
	// terminal: "bs" (Ctrl-H, 0x08) or "del" (0x7F). Telnet has no way to
	// negotiate the erase character, so the far end simply expects one of the
	// two — network gear and older Unix want BS, which is why that is the
	// default; a host that wants DEL is one toggle away in the terminal bar.
	// SSH is unaffected: its PTY carries the real terminal settings.
	KeyTelnetBackspace = "telnet_backspace"

	// KeyAuthDisabled ("1" = on) turns off the API-key check. Not a secret; the
	// loopback bind and Origin check on mutations still apply.
	KeyAuthDisabled = "auth_disabled"

	// KeyExitOnClose ("1" = on, the default) shuts the daemon down shortly after
	// the last browser tab goes away, so it does not linger in the background.
	KeyExitOnClose = "exit_on_close"

	// KeyUIMode selects how the interface is opened: "browser" (system default
	// handler), "app" (chromeless chromium window, the default) or "webview"
	// (native window, only in a binary built with the webview tag).
	KeyUIMode = "ui_mode"

	// KeyAppBrowser pins which browser the app window uses. Empty (the default)
	// means "detect one"; a path that no longer exists falls back to detection
	// rather than leaving the user with no window.
	KeyAppBrowser = "app_browser"

	// KeyMasterPassword stores the argon2id hash of the master password. It is
	// reserved: never returned to the SPA nor writable via PUT /api/settings.
	KeyMasterPassword = "master_pw"
)

// Defaults returns the default value for a setting key.
func Defaults(home string) map[string]string {
	return map[string]string{
		// {{alias}} {{user}} {{host}} {{port}} {{mountpoint}} are substituted at spawn time.
		KeyTerminalCmd: "gnome-terminal -- ssh {{alias}}",
		KeyFilesCmd:    "xdg-open {{mountpoint}}",
		// Empty means "use the system default browser" (xdg-open / open / rundll32).
		// A custom command may use the {{url}} placeholder, else the URL is appended.
		KeyBrowserCmd:      "",
		KeyMountBaseDir:    filepath.Join(home, "mnt", "webssh"),
		KeyTheme:           "system",
		KeyTelnetBackspace: "bs",
		KeyAuthDisabled:    "",
		KeyExitOnClose:     "1",
		KeyUIMode:          "app",
		KeyAppBrowser:      "",
	}
}

// Paths holds the resolved locations webssh reads and writes.
type Paths struct {
	Home          string // user home dir
	DataDir       string // ~/.local/share/webssh
	DBPath        string // <DataDir>/webssh.db
	BrowserDir    string // <DataDir>/browser — private profile for the app window
	SSHDir        string // ~/.ssh
	SSHConfig     string // ~/.ssh/config
	ManagedConfig string // ~/.ssh/config.d/inventory
}

// Resolve builds the standard paths for the current user.
func Resolve() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	dataDir := filepath.Join(home, ".local", "share", "webssh")
	sshDir := filepath.Join(home, ".ssh")
	return Paths{
		Home:          home,
		DataDir:       dataDir,
		DBPath:        filepath.Join(dataDir, "webssh.db"),
		BrowserDir:    filepath.Join(dataDir, "browser"),
		SSHDir:        sshDir,
		SSHConfig:     filepath.Join(sshDir, "config"),
		ManagedConfig: filepath.Join(sshDir, "config.d", "inventory"),
	}, nil
}

// EnsureDirs creates the data directory if missing.
func (p Paths) EnsureDirs() error {
	return os.MkdirAll(p.DataDir, 0o700)
}
