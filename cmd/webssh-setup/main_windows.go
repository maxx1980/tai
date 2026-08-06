//go:build windows

// Command webssh-setup is a native double-click installer for Windows: it
// gets WSL and a Linux distro ready (installing whichever is missing,
// self-elevating via UAC only when that's actually needed) and then runs the
// normal get.sh installer inside that distro. It exists because webssh has
// no native Windows build yet — internal/pty has no ConPTY backend, so the
// web terminal cannot start a shell outside WSL — and because get.bat/get.ps1
// need a console and, on a stock system, fight PowerShell's script
// ExecutionPolicy. A compiled .exe sidesteps both.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	defaultDistro = "Ubuntu"
	getShURL      = "https://raw.githubusercontent.com/maxx1980/tai/main/get.sh"
)

func main() {
	fmt.Println("webssh installer for Windows (via WSL)")
	fmt.Println()

	if _, err := exec.LookPath("wsl.exe"); err != nil {
		fail("wsl.exe was not found. This needs Windows 10 build 1607+ (2004+ recommended) — update Windows and try again.")
	}

	if !wslReady() {
		if !isElevated() {
			relaunchElevated()
			return
		}
		fmt.Printf("Installing WSL and %s (this can take a few minutes)...\n", defaultDistro)
		run("wsl.exe", "--install", "-d", defaultDistro)
		fmt.Println()
		fmt.Println("WSL was just installed. Windows usually needs a reboot before it can run it.")
		fmt.Println("Reboot, then run this installer again to finish installing webssh.")
		pause()
		return
	}

	distros := wslDistros()
	if len(distros) == 0 {
		if !isElevated() {
			relaunchElevated()
			return
		}
		fmt.Printf("WSL is present but has no Linux distro yet. Installing %s...\n", defaultDistro)
		run("wsl.exe", "--install", "-d", defaultDistro)
		fmt.Println("Installed. Run this installer again to continue installing webssh.")
		pause()
		return
	}

	distro := defaultDistro
	if !contains(distros, distro) {
		distro = distros[0]
		fmt.Printf("Using existing distro '%s' instead of the default.\n", distro)
	}

	fmt.Printf("WSL is ready (%s). Installing webssh inside it...\n", distro)

	// -u root sidesteps the interactive first-launch username/password wizard
	// a brand-new distro would otherwise show, and webssh needs nothing a
	// non-root user would give it that root does not already have.
	installCmd := fmt.Sprintf("curl -fsSL %s | bash", getShURL)
	if err := runChecked("wsl.exe", "-d", distro, "-u", "root", "--", "bash", "-lc", installCmd); err != nil {
		fail(fmt.Sprintf("webssh install failed inside WSL: %v", err))
	}

	fmt.Println()
	fmt.Printf("webssh is installed inside WSL (%s). To start it:\n", distro)
	// wsl -u root -- <cmd> isn't a login shell, so ~/.profile (which is what
	// adds ~/.local/bin to PATH) never runs - the full path is required.
	fmt.Printf("  wsl -d %s -u root -- /root/.local/bin/webssh --no-open\n", distro)
	fmt.Println()
	fmt.Println("--no-open matters here: webssh's default 'app' mode looks for a")
	fmt.Println("chromium-based browser to open, and a bare WSL distro has none —")
	fmt.Println("you don't need to install one there. WSL2 forwards localhost to")
	fmt.Println("Windows automatically, so once it prints a URL like")
	fmt.Println("http://127.0.0.1:8022/?token=..., open it in whatever browser")
	fmt.Println("you already have on Windows.")
	pause()
}

// wslReady reports whether WSL itself is installed and usable. wsl.exe exits
// non-zero (and prints an explanatory line to stderr, which is discarded
// here) when it isn't.
func wslReady() bool {
	cmd := exec.Command("wsl.exe", "--status")
	return cmd.Run() == nil
}

// wslDistros lists the registered WSL distros, e.g. ["Ubuntu"].
func wslDistros() []string {
	out, err := exec.Command("wsl.exe", "-l", "-q").Output()
	if err != nil {
		return nil
	}
	var distros []string
	for _, line := range strings.Split(decodeWslText(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			distros = append(distros, line)
		}
	}
	return distros
}

// decodeWslText decodes wsl.exe's console output. Real console apps on
// Windows switch to UTF-16LE (with a BOM) when stdout is a pipe rather than
// an actual console, which is exactly the case when os/exec captures it.
func decodeWslText(b []byte) string {
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		return string(b)
	}
	b = b[2:]
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u16 = append(u16, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(u16))
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// run runs a command with its output going straight to this console and
// ignores the result - used for `wsl --install`, whose own progress output
// is the feedback the user needs to see.
func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	_ = cmd.Run()
}

// runChecked is like run but reports whether the command failed.
func runChecked(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// isElevated reports whether this process already has administrator rights.
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// relaunchElevated re-starts this same executable through Windows' "runas"
// verb, which is what makes the OS show the standard UAC prompt, then exits
// the current (unprivileged) process. golang.org/x/sys/windows doesn't wrap
// shell32's ShellExecuteW, so it's called directly.
func relaunchElevated() {
	fmt.Println("Administrator rights are needed to install WSL. Requesting elevation...")
	exePath, err := os.Executable()
	if err != nil {
		fail("could not determine my own path to elevate: " + err.Error())
	}

	shell32 := syscall.NewLazyDLL("shell32.dll")
	shellExecute := shell32.NewProc("ShellExecuteW")

	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exePath)
	const swShowNormal = 1
	ret, _, _ := shellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0,
		0,
		swShowNormal,
	)
	// ShellExecute returns a value > 32 on success, an error code otherwise.
	if ret <= 32 {
		fail(fmt.Sprintf("could not request elevation (error code %d) — right-click this program and choose \"Run as administrator\" instead.", ret))
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
