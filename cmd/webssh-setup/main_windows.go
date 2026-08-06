//go:build windows

// Command webssh-setup is a native double-click installer for Windows: it
// gets WSL and a Linux distro ready (installing whichever is missing,
// self-elevating via UAC only when that's actually needed), runs the normal
// get.sh installer inside that distro, then installs webssh-launcher.exe
// (embedded below) and points a Desktop and Start Menu shortcut at it, so
// the app is reachable like any other installed program from then on. It
// exists because webssh has no native Windows build yet — internal/pty has
// no ConPTY backend, so the web terminal cannot start a shell outside WSL —
// and because get.bat/get.ps1 need a console and, on a stock system, fight
// PowerShell's script ExecutionPolicy. A compiled .exe sidesteps both.
package main

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"webssh/internal/wslutil"
)

const (
	defaultDistro = "Ubuntu"
	getShURL      = "https://raw.githubusercontent.com/maxx1980/tai/main/get.sh"
	appName       = "webssh"
)

//go:embed assets/webssh-launcher.exe
var launcherExe []byte

func main() {
	fmt.Println("webssh installer for Windows (via WSL)")
	fmt.Println()

	if !wslutil.Available() {
		fail("wsl.exe was not found. This needs Windows 10 build 1607+ (2004+ recommended) — update Windows and try again.")
	}

	if !wslutil.Ready() {
		if !wslutil.IsElevated() {
			requestElevationAndExit()
			return
		}
		fmt.Printf("Installing WSL and %s (this can take a few minutes)...\n", defaultDistro)
		wslutil.InstallDistro(defaultDistro)
		fmt.Println()
		fmt.Println("WSL was just installed. Windows usually needs a reboot before it can run it.")
		fmt.Println("Reboot, then run this installer again to finish installing webssh.")
		pause()
		return
	}

	// Probe the default distro directly instead of parsing `wsl -l -q`'s text
	// output to find it: that output needs UTF-16 decoding, which is more
	// fragile than just trying the name we expect and checking the exit code.
	distro := defaultDistro
	if !wslutil.DistroReady(distro) {
		distros := wslutil.Distros()
		if len(distros) == 0 {
			if !wslutil.IsElevated() {
				requestElevationAndExit()
				return
			}
			fmt.Printf("WSL is present but has no Linux distro yet. Installing %s...\n", defaultDistro)
			wslutil.InstallDistro(defaultDistro)
			fmt.Println("Installed. Run this installer again to continue installing webssh.")
			pause()
			return
		}
		distro = distros[0]
		fmt.Printf("Using existing distro '%s' instead of the default.\n", distro)
	}

	fmt.Printf("WSL is ready (%s). Installing webssh inside it...\n", distro)

	// -u root sidesteps the interactive first-launch username/password wizard
	// a brand-new distro would otherwise show, and webssh needs nothing a
	// non-root user would give it that root does not already have.
	installCmd := fmt.Sprintf("curl -fsSL %s | bash", getShURL)
	if err := wslutil.RunChecked("wsl.exe", "-d", distro, "-u", "root", "--", "bash", "-lc", installCmd); err != nil {
		fail(fmt.Sprintf("webssh install failed inside WSL: %v", err))
	}

	fmt.Println()
	fmt.Println("Setting up the Desktop/Start Menu shortcut...")
	launcherPath, err := installLauncher()
	if err != nil {
		fail(fmt.Sprintf("could not install the launcher: %v", err))
	}
	if err := createShortcuts(launcherPath); err != nil {
		// Not fatal - webssh is fully installed either way, and the manual
		// start command below still works.
		fmt.Printf("Warning: could not create shortcuts: %v\n", err)
	} else {
		fmt.Println("Added a 'webssh' shortcut to your Desktop and Start Menu.")
	}

	fmt.Println()
	fmt.Println("Done. Starting webssh now — it'll open in your browser shortly.")
	fmt.Println("Next time, just use the webssh shortcut.")
	pause()

	_ = exec.Command(launcherPath).Start()
}

// installLauncher writes the embedded webssh-launcher.exe to a stable,
// permanent location - it can't stay wherever webssh-setup.exe was
// downloaded to (e.g. Downloads), since a shortcut needs a path that lasts.
func installLauncher() (string, error) {
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), appName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "webssh-launcher.exe")
	if err := os.WriteFile(path, launcherExe, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// createShortcuts creates a Desktop and Start Menu .lnk pointing at target,
// via the decades-old WScript.Shell COM approach - there's no Win32 API for
// this that doesn't mean writing the Shell Link binary format by hand, and
// this one-liner is about as well-trodden as Windows scripting gets.
func createShortcuts(target string) error {
	locations := []string{
		filepath.Join(os.Getenv("USERPROFILE"), "Desktop", appName+".lnk"),
		filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", appName+".lnk"),
	}
	var firstErr error
	for _, path := range locations {
		if err := createShortcut(path, target); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func createShortcut(shortcutPath, target string) error {
	script := fmt.Sprintf(
		`$sh = New-Object -ComObject WScript.Shell; $s = $sh.CreateShortcut(%s); $s.TargetPath = %s; $s.IconLocation = %s; $s.Description = %s; $s.Save()`,
		psQuote(shortcutPath), psQuote(target), psQuote(target+",0"), psQuote("webssh - local SSH control panel"),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
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

func requestElevationAndExit() {
	fmt.Println("Administrator rights are needed to install WSL. Requesting elevation...")
	exePath, err := os.Executable()
	if err != nil {
		fail("could not determine my own path to elevate: " + err.Error())
	}
	if err := wslutil.RelaunchElevated(exePath); err != nil {
		fail(fmt.Sprintf("could not request elevation (%v) — right-click this program and choose \"Run as administrator\" instead.", err))
	}
}

func fail(msg string) {
	fmt.Println("Error: " + msg)
	pause()
	os.Exit(1)
}

func pause() {
	fmt.Println()
	fmt.Print("Press Enter to close this window...")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
