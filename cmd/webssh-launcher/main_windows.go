//go:build windows

// Command webssh-launcher is what the Desktop/Start Menu shortcut webssh-setup
// creates actually points at. It starts webssh inside WSL, reads the token
// URL it prints on its first line of output, and opens that URL in whatever
// browser Windows already has - webssh itself has no browser to open from
// inside a bare WSL distro, so this stands in for the app window a native
// build would otherwise open directly. Built with -H=windowsgui (see the
// Makefile), so it never shows a console of its own.
package main

import (
	"bufio"
	"io"
	"os/exec"
	"regexp"
	"syscall"
	"unsafe"

	"webssh/internal/wslutil"
)

const defaultDistro = "Ubuntu"

var urlPattern = regexp.MustCompile(`https?://\S+\?token=\S+`)

func main() {
	if !wslutil.Available() || !wslutil.Ready() {
		showError("WSL isn't set up yet. Run the webssh installer first.")
		return
	}

	distro := defaultDistro
	if !wslutil.DistroReady(distro) {
		distros := wslutil.Distros()
		if len(distros) == 0 {
			showError("webssh isn't installed yet. Run the webssh installer first.")
			return
		}
		distro = distros[0]
	}

	cmd := exec.Command("wsl.exe", "-d", distro, "-u", "root", "--", "/root/.local/bin/webssh", "--no-open")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		showError("could not start webssh: " + err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		showError("could not start webssh: " + err.Error())
		return
	}
	// Drained for the life of the process so webssh never blocks trying to
	// write to a pipe nobody is reading.
	go io.Copy(io.Discard, stderr)

	if err := cmd.Start(); err != nil {
		showError("could not start webssh: " + err.Error())
		return
	}

	url := readURL(stdout)
	go io.Copy(io.Discard, stdout)

	if url == "" {
		showError("webssh did not report a URL to open. Run the webssh installer again.")
		_ = cmd.Process.Kill()
		return
	}

	_ = exec.Command("explorer.exe", url).Start()

	// Stay alive (invisibly - there's no console) for as long as webssh runs,
	// so its stdout/stderr pipes stay open instead of breaking mid-write.
	_ = cmd.Wait()
}

// readURL scans r for webssh's startup line and returns the token URL in it,
// or "" if the process ended (or the pattern never showed up) first. webssh
// itself always writes plain UTF-8, but wsl.exe's relay of it is untested on
// real hardware, so each line is also tried through the same UTF-16 decoding
// wsl.exe's own text output sometimes needs (see wslutil.DecodeText) as a
// safety net in case the relay does the same thing.
func readURL(r io.Reader) string {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if m := urlPattern.FindString(string(line)); m != "" {
			return m
		}
		if m := urlPattern.FindString(wslutil.DecodeText(line)); m != "" {
			return m
		}
	}
	return ""
}

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

// showError is this program's only user-visible output: it has no console
// (built with -H=windowsgui), so a native message box is the one way to
// surface a problem instead of failing invisibly.
func showError(msg string) {
	title, _ := syscall.UTF16PtrFromString("webssh")
	text, _ := syscall.UTF16PtrFromString(msg)
	const mbIconError = 0x10
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconError)
}
