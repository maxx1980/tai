//go:build windows

// Package wslutil holds the WSL/Windows plumbing shared by webssh-setup (the
// installer) and webssh-launcher (the shortcut target): checking whether WSL
// and a distro are ready, installing whichever is missing, and requesting
// UAC elevation when that's needed. Windows-only, since none of it makes
// sense anywhere else.
package wslutil

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Available reports whether wsl.exe itself exists on PATH.
func Available() bool {
	_, err := exec.LookPath("wsl.exe")
	return err == nil
}

// Ready reports whether WSL is installed and usable. wsl.exe exits non-zero
// (and prints an explanatory line to stderr, discarded here) when it isn't.
func Ready() bool {
	cmd := exec.Command("wsl.exe", "--status")
	return cmd.Run() == nil
}

// DistroReady checks whether a specific distro exists and can run a command
// as root, without needing to parse any of wsl.exe's text output.
func DistroReady(name string) bool {
	cmd := exec.Command("wsl.exe", "-d", name, "-u", "root", "--", "true")
	return cmd.Run() == nil
}

// Distros lists the registered WSL distros, e.g. ["Ubuntu"]. Only meant as a
// fallback for when a specific expected distro name doesn't just work.
func Distros() []string {
	out, err := exec.Command("wsl.exe", "-l", "-q").Output()
	if err != nil {
		return nil
	}
	var distros []string
	for _, line := range strings.Split(DecodeText(out), "\n") {
		line = strings.TrimFunc(line, func(r rune) bool {
			return !unicode.IsPrint(r)
		})
		if line != "" {
			distros = append(distros, line)
		}
	}
	return distros
}

// DecodeText decodes wsl.exe's console output. Real console apps on
// Windows switch to UTF-16LE (sometimes but not always with a leading BOM)
// when stdout is a pipe rather than an actual console, which is exactly the
// case when os/exec captures it. Exported so callers reading wsl.exe output
// for any other reason (e.g. webssh-launcher scanning for a startup URL) can
// defend against the same thing without duplicating the decode logic.
func DecodeText(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u16 = append(u16, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(u16))
}

// InstallDistro runs `wsl --install -d name --no-launch`, with output going
// straight to this process's console - wsl.exe's own progress output is the
// feedback the user needs to see while it downloads and installs.
//
// --no-launch matters: without it, wsl.exe launches the distro once install
// finishes to run its first-boot setup, which is an interactive wizard for
// creating a UNIX username and password - it reads/writes this process's
// inherited console, and since nothing here can drive that prompt, install
// just hangs waiting for a human. webssh only ever runs as root (see
// DistroReady/get.sh's caller), so that default user is never needed.
func InstallDistro(name string) {
	cmd := exec.Command("wsl.exe", "--install", "-d", name, "--no-launch")
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	_ = cmd.Run()
}

// RunChecked runs a command with inherited stdio and reports whether it failed.
func RunChecked(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// IsElevated reports whether this process already has administrator rights.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// RelaunchElevated re-starts the given executable through Windows' "runas"
// verb, which is what makes the OS show the standard UAC prompt. The caller
// should exit immediately afterward, leaving the elevated copy to continue.
// golang.org/x/sys/windows doesn't wrap shell32's ShellExecuteW, so it's
// called directly.
func RelaunchElevated(exePath string) error {
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
		return &execError{code: int(ret)}
	}
	return nil
}

type execError struct{ code int }

func (e *execError) Error() string {
	return "ShellExecute failed"
}

// Code returns the raw ShellExecute error code, for callers that want to
// report it.
func (e *execError) Code() int { return e.code }
