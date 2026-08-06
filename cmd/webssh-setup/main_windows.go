//go:build windows

// Command webssh-setup is a native double-click installer for Windows: it
// gets WSL and a Linux distro ready (installing whichever is missing,
// self-elevating via UAC only when that's actually needed), runs the normal
// get.sh installer inside that distro, then installs webssh-launcher.exe and
// webssh-uninstall.exe (both embedded below), points a Desktop and Start
// Menu shortcut at the launcher, and registers webssh under Settings > Apps
// so it has a working Uninstall button there too. It exists because webssh
// has no native Windows build yet — internal/pty has no ConPTY backend, so
// the web terminal cannot start a shell outside WSL — and because
// get.bat/get.ps1 need a console and, on a stock system, fight PowerShell's
// script ExecutionPolicy. A compiled .exe sidesteps both.
package main

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"webssh/internal/version"
	"webssh/internal/wininstall"
	"webssh/internal/wslutil"
)

const (
	defaultDistro = "Ubuntu"
	getShURL      = "https://raw.githubusercontent.com/maxx1980/tai/main/get.sh"
)

//go:embed assets/webssh-launcher.exe
var launcherExe []byte

//go:embed assets/webssh-uninstall.exe
var uninstallExe []byte

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
		const msg = "WSL was just installed. Windows needs a reboot before it can run it.\n\n" +
			"After you reboot, run this installer again to finish installing webssh."
		fmt.Println()
		fmt.Println(msg)
		showInfo(msg)
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
	// Best-effort sshfs install (needed for the Files "system"/Mount button):
	// wrapped in `|| true` so a non-apt distro or a flaky mirror never blocks
	// the webssh install itself.
	// TODO(windows-support): --version pins installs to the windows-setup-test
	// prerelease, which is the only place the WSL-aware config.go defaults
	// have been published so far (the real "latest" release predates this
	// branch). Drop this pin once windows-support merges to main and a real
	// release carries the fix.
	// user_allow_other lets a non-root caller (which is how \\wsl$\ may reach
	// the distro) use sshfs's -o allow_other; root itself doesn't need this,
	// but it costs nothing to enable and removes one variable when \\wsl$\
	// shows a mount as empty.
	installCmd := fmt.Sprintf(
		"(command -v apt-get >/dev/null 2>&1 && apt-get update -qq && "+
			"DEBIAN_FRONTEND=noninteractive apt-get install -y -qq sshfs || true) && "+
			"(grep -q '^user_allow_other' /etc/fuse.conf 2>/dev/null || "+
			"echo user_allow_other >> /etc/fuse.conf) && "+
			"curl -fsSL %s | bash -s -- --version windows-setup-test",
		getShURL)
	if err := wslutil.RunChecked("wsl.exe", "-d", distro, "-u", "root", "--", "bash", "-lc", installCmd); err != nil {
		fail(fmt.Sprintf("webssh install failed inside WSL: %v", err))
	}

	fmt.Println()
	fmt.Println("Setting up the shortcut and Windows registration...")
	launcherPath, uninstallerPath, err := installFiles()
	if err != nil {
		fail(fmt.Sprintf("could not install the launcher/uninstaller: %v", err))
	}
	if err := wininstall.CreateShortcuts(launcherPath); err != nil {
		// Not fatal - webssh is fully installed either way, and the manual
		// start command below still works.
		fmt.Printf("Warning: could not create shortcuts: %v\n", err)
	} else {
		fmt.Println("Added a 'webssh' shortcut to your Desktop and Start Menu.")
	}
	if err := wininstall.RegisterUninstall(uninstallerPath, launcherPath, version.Version); err != nil {
		fmt.Printf("Warning: could not register webssh under Settings > Apps: %v\n", err)
	} else {
		fmt.Println("Registered webssh under Settings > Apps, with a working Uninstall button.")
	}

	fmt.Println()
	fmt.Println("Done. Starting webssh now — it'll open in your browser shortly.")
	fmt.Println("Next time, just use the webssh shortcut.")
	pause()

	_ = exec.Command(launcherPath).Start()
}

// installFiles writes the embedded webssh-launcher.exe and webssh-uninstall.exe
// to a stable, permanent location - they can't stay wherever webssh-setup.exe
// was downloaded to (e.g. Downloads), since the shortcut and the Settings >
// Apps entry both need paths that last.
func installFiles() (launcherPath, uninstallerPath string, err error) {
	dir := wininstall.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	launcherPath = filepath.Join(dir, "webssh-launcher.exe")
	if err := os.WriteFile(launcherPath, launcherExe, 0o755); err != nil {
		return "", "", err
	}
	uninstallerPath = filepath.Join(dir, "webssh-uninstall.exe")
	if err := os.WriteFile(uninstallerPath, uninstallExe, 0o755); err != nil {
		return "", "", err
	}
	return launcherPath, uninstallerPath, nil
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

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

// showInfo pops up a message box in addition to the console text above it -
// a reboot requirement is easy to miss in scrolling console output (or if
// the window is closed before it's read), and this needs to actually stick.
func showInfo(msg string) {
	title, _ := syscall.UTF16PtrFromString("webssh")
	text, _ := syscall.UTF16PtrFromString(msg)
	const mbIconInformation = 0x40
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconInformation)
}
