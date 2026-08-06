//go:build windows

// Command webssh-uninstall removes what webssh-setup added on the Windows
// side: the Desktop/Start Menu shortcuts, the "webssh" entry under Settings
// > Apps (Apps & features), and the installed webssh-launcher.exe itself.
// It's what that entry's Uninstall button runs, and webssh-setup installs a
// copy of it there for exactly that purpose.
//
// It never touches WSL, the Linux distro, or webssh's data there on its own
// - those can hold things that have nothing to do with webssh, so removing
// them isn't something an uninstaller should do without being asked. Pass
// -purge to also remove the webssh binary itself from inside WSL, via
// get.sh's own --uninstall (which already leaves the inventory/keys alone).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"webssh/internal/wininstall"
	"webssh/internal/wslutil"
)

const (
	defaultDistro = "Ubuntu"
	getShURL      = "https://raw.githubusercontent.com/maxx1980/tai/main/get.sh"
)

func main() {
	purge := flag.Bool("purge", false, "also remove webssh itself from inside WSL (your inventory/keys are kept either way)")
	flag.Parse()

	fmt.Println("Removing webssh...")

	wininstall.RemoveShortcuts()

	if err := wininstall.UnregisterUninstall(); err != nil {
		fmt.Printf("Warning: could not remove the Settings > Apps entry: %v\n", err)
	}

	if *purge {
		purgeInsideWSL()
	} else {
		fmt.Println("webssh itself is still installed inside WSL (run with -purge to remove it too).")
	}

	dir := wininstall.Dir()
	fmt.Println("Removing " + dir + "...")
	selfDelete(dir)

	fmt.Println("Done.")
	pause()
}

// purgeInsideWSL runs get.sh's own --uninstall inside the distro webssh was
// installed into. Best-effort: if WSL or the distro is already gone, there
// is nothing left to purge, which isn't an error.
func purgeInsideWSL() {
	if !wslutil.Available() || !wslutil.Ready() {
		return
	}
	distro := defaultDistro
	if !wslutil.DistroReady(distro) {
		distros := wslutil.Distros()
		if len(distros) == 0 {
			return
		}
		distro = distros[0]
	}
	fmt.Println("Removing webssh from inside WSL...")
	cmd := fmt.Sprintf("curl -fsSL %s | bash -s -- --uninstall", getShURL)
	if err := wslutil.RunChecked("wsl.exe", "-d", distro, "-u", "root", "--", "bash", "-lc", cmd); err != nil {
		fmt.Printf("Warning: could not remove webssh from inside WSL: %v\n", err)
	}
}

// selfDelete removes dir - this program's own installed copy, alongside
// webssh-launcher.exe - a moment after this process exits, since a running
// program can't delete the file backing itself. "ping -n 2" is the standard
// ~1-second delay trick that works with no interactive console, unlike
// "timeout"; DETACHED_PROCESS lets it outlive this process cleanly.
func selfDelete(dir string) {
	cmd := exec.Command("cmd.exe", "/c", `ping 127.0.0.1 -n 2 >nul & rmdir /s /q "`+dir+`"`)
	const detachedProcess = 0x00000008
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess}
	_ = cmd.Start()
}

func pause() {
	fmt.Println()
	fmt.Print("Press Enter to close this window...")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
