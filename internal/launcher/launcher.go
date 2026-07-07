// Package launcher spawns native desktop applications (terminal, file manager)
// from user-configured command templates.
package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"webssh/internal/store"
)

// Vars are the substitution values available in command templates.
type Vars struct {
	Alias      string
	User       string
	Host       string
	Port       int
	Mountpoint string
}

// VarsFromHost builds template vars from a host and its mountpoint.
func VarsFromHost(h store.Host, mountpoint string) Vars {
	host := h.Hostname
	if host == "" {
		host = h.Alias
	}
	return Vars{Alias: h.Alias, User: h.User, Host: host, Port: h.Port, Mountpoint: mountpoint}
}

// expand replaces {{name}} placeholders in a template.
func expand(tmpl string, v Vars) string {
	r := strings.NewReplacer(
		"{{alias}}", v.Alias,
		"{{user}}", v.User,
		"{{host}}", v.Host,
		"{{port}}", fmt.Sprintf("%d", v.Port),
		"{{mountpoint}}", v.Mountpoint,
	)
	return r.Replace(tmpl)
}

// Spawn expands the template and launches it detached from webssh so it keeps
// running after the request returns. The command is parsed with shell-like
// field splitting (quotes respected) rather than passed to a shell, so the
// template values are not interpreted as shell code.
//
// When sshWrapper is non-empty, the first bare `ssh` command word in the
// expanded template is replaced with that wrapper path (which self-sets the
// SSH_ASKPASS environment and execs the real ssh). This delivers a saved
// password to ssh even under terminal emulators that run a single background
// server we cannot pass environment to.
func Spawn(tmpl string, v Vars, sshWrapper string) error {
	expanded := expand(tmpl, v)
	fields, err := splitFields(expanded)
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		return fmt.Errorf("empty command")
	}
	if sshWrapper != "" {
		for i, f := range fields {
			if filepath.Base(f) == "ssh" {
				fields[i] = sshWrapper
				break
			}
		}
	}
	cmd := exec.Command(fields[0], fields[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch %q: %w", fields[0], err)
	}
	// Reap the child in the background so it does not become a zombie.
	go cmd.Wait()
	return nil
}

// SplitFields splits a command line into fields honoring quotes, without any
// shell metacharacter expansion. Exported for reuse (e.g. the browser command).
func SplitFields(s string) ([]string, error) { return splitFields(s) }

// splitFields splits a command line into fields, honoring single and double
// quotes. It intentionally does NOT expand shell metacharacters.
func splitFields(s string) ([]string, error) {
	var fields []string
	var cur strings.Builder
	var quote rune
	inField := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			inField = true
		case r == '\'' || r == '"':
			quote = r
			inField = true
		case r == ' ' || r == '\t':
			if inField {
				fields = append(fields, cur.String())
				cur.Reset()
				inField = false
			}
		default:
			cur.WriteRune(r)
			inField = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unbalanced quote in command")
	}
	if inField {
		fields = append(fields, cur.String())
	}
	return fields, nil
}
