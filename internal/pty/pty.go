// Package pty starts an `ssh <alias>` session attached to a pseudo-terminal so
// it can be bridged to a browser xterm.js terminal over a websocket.
package pty

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// Session is a running ssh process attached to a PTY.
type Session struct {
	Cmd *exec.Cmd
	Pty *os.File
}

// Start launches `ssh` with the given arguments in a new PTY. The caller builds
// explicit connection arguments from the host record so the terminal works
// whether or not the alias has been exported to ssh_config.
func Start(args []string) (*Session, error) {
	cmd := exec.Command("ssh", args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &Session{Cmd: cmd, Pty: f}, nil
}

// Resize sets the PTY window size.
func (s *Session) Resize(rows, cols uint16) error {
	return pty.Setsize(s.Pty, &pty.Winsize{Rows: rows, Cols: cols})
}

// Close terminates the session and releases the PTY.
func (s *Session) Close() {
	_ = s.Pty.Close()
	if s.Cmd.Process != nil {
		_ = s.Cmd.Process.Kill()
	}
	_ = s.Cmd.Wait()
}
