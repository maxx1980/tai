//go:build windows

// Package wininstall holds what webssh-setup and webssh-uninstall share:
// where webssh's Windows-side files live, the Desktop/Start Menu shortcuts,
// and the "Apps & features" (Settings > Apps) registration that gives it a
// working Uninstall button there. Windows-only, since none of it makes
// sense anywhere else.
package wininstall

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	// AppName names the shortcut, the install directory under %LOCALAPPDATA%,
	// and the registry key - kept in one place so setup and uninstall can
	// never disagree on where things are.
	AppName = "webssh"

	uninstallKeyPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + AppName
)

// Dir returns the directory webssh's Windows-side files (the launcher, the
// uninstaller) are installed into - always %LOCALAPPDATA%\webssh, a stable
// location independent of wherever webssh-setup.exe itself was downloaded to.
func Dir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), AppName)
}

// ShortcutPaths lists where the Desktop and Start Menu shortcuts live.
func ShortcutPaths() []string {
	return []string{
		filepath.Join(os.Getenv("USERPROFILE"), "Desktop", AppName+".lnk"),
		filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", AppName+".lnk"),
	}
}

// CreateShortcuts points a Desktop and Start Menu .lnk at target, via the
// decades-old WScript.Shell COM approach - there's no Win32 API for this
// short of writing the Shell Link binary format by hand, and this one-liner
// is about as well-trodden as Windows scripting gets.
func CreateShortcuts(target string) error {
	var firstErr error
	for _, path := range ShortcutPaths() {
		if err := createShortcut(path, target); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RemoveShortcuts deletes both shortcuts, ignoring ones that are already gone.
func RemoveShortcuts() {
	for _, path := range ShortcutPaths() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Printf("Warning: could not remove %s: %v\n", path, err)
		}
	}
}

func createShortcut(shortcutPath, target string) error {
	script := fmt.Sprintf(
		`$sh = New-Object -ComObject WScript.Shell; $s = $sh.CreateShortcut(%s); $s.TargetPath = %s; $s.IconLocation = %s; $s.Description = %s; $s.Save()`,
		psQuote(shortcutPath), psQuote(target), psQuote(target+",0"), psQuote("webssh - local SSH control panel"),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	// Belt-and-braces: the caller has a console of its own, so this child
	// shouldn't need a second one, but its I/O is fully redirected either way
	// (CombinedOutput), which is exactly the condition under which a stray
	// console window has been seen to flash briefly for some Windows/console
	// host combinations.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s: %w", msg, err)
		}
		return err
	}
	return nil
}

// psQuote wraps s in single quotes for a PowerShell command line. Single
// quotes are used deliberately: PowerShell does no escape processing inside
// them (unlike double quotes, where a literal backslash - unavoidable in any
// Windows path - would need its own escaping rules), so the only character
// that needs handling is a literal single quote, doubled per PowerShell's rule.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// RegisterUninstall adds the "webssh" entry Settings > Apps reads, pointing
// its Uninstall button at uninstallerPath. HKEY_CURRENT_USER is used so no
// admin rights are needed (installing webssh itself never needs them either,
// past the one-time WSL setup).
func RegisterUninstall(uninstallerPath, iconPath, version string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	set := func(name, value string) {
		if err == nil {
			err = key.SetStringValue(name, value)
		}
	}
	set("DisplayName", AppName)
	set("Publisher", AppName)
	set("DisplayVersion", version)
	set("UninstallString", `"`+uninstallerPath+`"`)
	set("DisplayIcon", iconPath)
	set("InstallLocation", Dir())
	if err == nil {
		err = key.SetDWordValue("NoModify", 1)
	}
	if err == nil {
		err = key.SetDWordValue("NoRepair", 1)
	}
	return err
}

// UnregisterUninstall removes the Settings > Apps entry.
func UnregisterUninstall() error {
	err := registry.DeleteKey(registry.CURRENT_USER, uninstallKeyPath)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}
