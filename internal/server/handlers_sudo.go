package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Passwordless sudo for the user running webssh: a one-line drop-in
// /etc/sudoers.d/<user> = "<user> ALL=(ALL) NOPASSWD: ALL", installed with the
// sudo password the user types once. The password reaches sudo on stdin only
// (never argv, logs or the database), and the file is checked with
// `visudo -cf` before it lands, because a broken sudoers drop-in can disable
// sudo altogether.

// sudoersDir is where the drop-in goes; a variable so tests can redirect it.
var sudoersDir = "/etc/sudoers.d"

// sudoTimeout bounds one sudo invocation (a wrong password makes sudo retry
// prompts; with -S and a single line on stdin it gives up at EOF).
const sudoTimeout = 20 * time.Second

// validUser matches names safe in a sudoers rule and as a sudoers.d file name
// (sudo skips drop-ins whose name contains '.' or ends in '~').
var validUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func sudoUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	if u.Uid == "0" {
		return "", fmt.Errorf("webssh already runs as root")
	}
	if !validUser.MatchString(u.Username) {
		return "", fmt.Errorf("user name %q is not safe to put in a sudoers rule", u.Username)
	}
	return u.Username, nil
}

func sudoersRule(name string) string { return name + " ALL=(ALL) NOPASSWD: ALL\n" }

func sudoSupported() error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("sudo is not available on Windows")
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("sudo is not installed")
	}
	return nil
}

// visudoPath finds visudo, which usually lives in /usr/sbin — often not on a
// regular user's PATH.
func visudoPath() string {
	if p, err := exec.LookPath("visudo"); err == nil {
		return p
	}
	for _, p := range []string{"/usr/sbin/visudo", "/sbin/visudo", "/usr/local/sbin/visudo"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// runSudo runs `sudo <args>`: with a password via -S (stdin, prompt
// suppressed, cached credentials dropped with -k so the password is really
// checked), or without one via -n (fails instead of prompting).
func runSudo(ctx context.Context, password string, args ...string) (string, error) {
	var full []string
	if password != "" {
		full = append([]string{"-S", "-k", "-p", ""}, args...)
	} else {
		full = append([]string{"-n"}, args...)
	}
	cmd := exec.CommandContext(ctx, "sudo", full...)
	// English messages: sudoError matches them, and a localised sudo says
	// "Извините, попробуйте ещё раз." instead of "Sorry, try again."
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C", "LANGUAGE=")
	if password != "" {
		cmd.Stdin = strings.NewReader(password + "\n")
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// sudoError turns sudo's output into a message for the user.
func sudoError(out string, err error) (int, error) {
	switch {
	case strings.Contains(out, "incorrect password") || strings.Contains(out, "Sorry, try again"):
		return http.StatusForbidden, fmt.Errorf("wrong sudo password")
	case strings.Contains(out, "not in the sudoers file") || strings.Contains(out, "is not allowed to"):
		return http.StatusForbidden, fmt.Errorf("this user may not use sudo")
	case strings.Contains(out, "a password is required"), strings.Contains(out, "interactive authentication is required"):
		return http.StatusForbidden, fmt.Errorf("sudo needs your password")
	}
	msg := strings.TrimSpace(out)
	if msg == "" {
		msg = err.Error()
	}
	return http.StatusInternalServerError, fmt.Errorf("sudo: %s", msg)
}

// handleSudoStatus reports whether passwordless sudo already works.
func (s *Server) handleSudoStatus(w http.ResponseWriter, r *http.Request) {
	name, uerr := sudoUser()
	resp := map[string]any{"user": name, "file": filepath.Join(sudoersDir, name)}
	if err := sudoSupported(); err != nil {
		resp["supported"], resp["error"] = false, err.Error()
		writeJSON(w, 200, resp)
		return
	}
	if uerr != nil {
		resp["supported"], resp["error"] = false, uerr.Error()
		writeJSON(w, 200, resp)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sudoTimeout)
	defer cancel()
	_, err := runSudo(ctx, "", "true")
	resp["supported"], resp["nopasswd"] = true, err == nil
	writeJSON(w, 200, resp)
}

// handleSudoEnable installs /etc/sudoers.d/<user> with the NOPASSWD rule.
func (s *Server) handleSudoEnable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := sudoSupported(); err != nil {
		writeErr(w, 400, err)
		return
	}
	name, err := sudoUser()
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if body.Password == "" {
		writeErr(w, 400, fmt.Errorf("enter your sudo password"))
		return
	}

	// Stage the rule in our private data dir and syntax-check it first.
	tmp, err := os.CreateTemp(s.paths.DataDir, "sudoers-")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(sudoersRule(name)); err != nil {
		tmp.Close()
		writeErr(w, 500, err)
		return
	}
	tmp.Close()
	if v := visudoPath(); v != "" {
		if out, err := exec.Command(v, "-cf", tmp.Name()).CombinedOutput(); err != nil {
			writeErr(w, 500, fmt.Errorf("visudo rejected the rule: %s", strings.TrimSpace(string(out))))
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), sudoTimeout)
	defer cancel()
	dest := filepath.Join(sudoersDir, name)
	// install sets owner, group and mode in one step; sudo ignores drop-ins
	// that are writable by anyone but root.
	if out, err := runSudo(ctx, body.Password, "install", "-m", "0440", "-o", "root", "-g", "root", tmp.Name(), dest); err != nil {
		code, e := sudoError(out, err)
		writeErr(w, code, e)
		return
	}
	// Confirm it took effect.
	_, err = runSudo(ctx, "", "true")
	writeJSON(w, 200, map[string]any{"user": name, "file": dest, "nopasswd": err == nil})
}

// handleSudoDisable removes /etc/sudoers.d/<user>. While the rule is active
// no password is needed; otherwise the body's password is used.
func (s *Server) handleSudoDisable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	_ = readJSON(r, &body)
	if err := sudoSupported(); err != nil {
		writeErr(w, 400, err)
		return
	}
	name, err := sudoUser()
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sudoTimeout)
	defer cancel()
	dest := filepath.Join(sudoersDir, name)
	if out, err := runSudo(ctx, body.Password, "rm", "-f", dest); err != nil {
		code, e := sudoError(out, err)
		writeErr(w, code, e)
		return
	}
	_, err = runSudo(ctx, "", "true")
	writeJSON(w, 200, map[string]any{"user": name, "file": dest, "nopasswd": err == nil})
}
