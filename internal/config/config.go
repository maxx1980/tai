// Package config resolves filesystem paths and default settings for webssh.
package config

import (
	"os"
	"path/filepath"
	"strings"
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

	// KeyTerminalMode selects which terminal buttons a host card offers: "web"
	// (in-browser xterm.js), "system" (the native emulator from terminal_cmd),
	// "both" (the default) or "off". Purely a UI preference — the launch
	// endpoints stay available either way. The Telnet button is unaffected:
	// telnet is a service of its own with no native counterpart.
	KeyTerminalMode = "terminal_mode"

	// KeyFilesMode selects which file buttons a host card offers: "web" (the
	// SFTP browser), "system" (sshfs mount + the file manager from files_cmd),
	// "both" (the default) or "off". Mount/Unmount follows "system", since the
	// mount exists to serve the native file manager.
	KeyFilesMode = "files_mode"

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

// runningUnderWSL reports whether the process is inside a WSL VM, checked via
// the kernel release string (e.g. "5.15.90.1-microsoft-standard-WSL2") rather
// than an env var, so it works regardless of how the process was launched.
func runningUnderWSL() bool {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// Defaults returns the default value for a setting key.
func Defaults(home string) map[string]string {
	// {{alias}} {{user}} {{host}} {{port}} {{mountpoint}} are substituted at spawn time.
	terminalCmd := "gnome-terminal -- ssh {{alias}}"
	filesCmd := "xdg-open {{mountpoint}}"
	if runningUnderWSL() {
		// No desktop environment inside WSL — reach out to the Windows host
		// instead via WSL2 interop (Win32 binaries are on PATH there). Assumes
		// an apt-based distro with sshfs installed (webssh-setup.exe does
		// this) and that {{mountpoint}} never contains spaces.
		distro := os.Getenv("WSL_DISTRO_NAME")
		if distro == "" {
			distro = "Ubuntu"
		}
		// Prefer PowerShell's window (stays open after ssh exits, nicer to work
		// in) and fall back to the classic console host if powershell.exe is
		// ever missing (locked-down images, etc). Both are plain Win32 console
		// apps, so launching either directly from a console-less WSL process
		// gives it a fresh window with no wrapper needed — cmd.exe's own `start`
		// is only there to give the fallback the same "own window" behavior.
		//
		// -EncodedCommand rather than -Command: WSL interop rebuilds a Win32
		// command line from the argv it hands the target .exe, and does not
		// reliably re-quote an argument that itself contains spaces — a plain
		// -Command "wsl.exe -d Ubuntu ... ssh {{alias}}" arrives at PowerShell
		// with the quoted argument dropped entirely ("Command must follow the
		// -Command parameter"), confirmed live. Base64 of UTF-16LE has no
		// whitespace or shell metacharacters, so nothing downstream can mangle
		// it. cmd.exe's fallback keeps plain args since `start` never showed
		// this problem.
		wslSSH := "wsl.exe -d " + distro + " -u root -- ssh {{alias}}"
		terminalCmd = "bash -c 'enc=$(printf %s \"" + wslSSH + "\" | iconv -t UTF-16LE | base64 -w0); " +
			"command -v powershell.exe >/dev/null 2>&1 && exec powershell.exe -NoExit -EncodedCommand \"$enc\" || " +
			"exec cmd.exe /c start " + wslSSH + "'"
		filesCmd = `bash -c 'explorer.exe "$(wslpath -w {{mountpoint}})"'`
	}
	return map[string]string{
		KeyTerminalCmd: terminalCmd,
		KeyFilesCmd:    filesCmd,
		// Empty means "use the system default browser" (xdg-open / open / rundll32).
		// A custom command may use the {{url}} placeholder, else the URL is appended.
		KeyBrowserCmd:      "",
		KeyMountBaseDir:    filepath.Join(home, "mnt", "webssh"),
		KeyTheme:           "system",
		KeyTelnetBackspace: "bs",
		KeyTerminalMode:    "both",
		KeyFilesMode:       "both",
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
