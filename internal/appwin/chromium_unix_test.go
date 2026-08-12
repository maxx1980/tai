//go:build unix

package appwin

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Close must actually terminate the process Open launched — without it, the
// daemon's own shutdown never touches the chromium instance it started, and
// that instance outlives the daemon indefinitely on macOS (see
// killStaleProfileHolder's doc comment). A no-op Close, or one main.go never
// called, is exactly what let "close the window, relaunch, get a broken
// window" and "the process never quits" both go unnoticed. syscall.Kill(pid,
// 0) — the standard existence check — has no Windows equivalent, hence unix.
func TestCloseTerminatesLaunchedProcess(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "chromium")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ui := &chromiumUI{exe: exe, profileDir: t.TempDir()}
	if err := ui.Open("http://example.test"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	pid := ui.proc.Pid
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("process %d not running right after Open(): %v", pid, err)
	}

	ui.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // gone
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d still running 2s after Close()", pid)
}
