// Package hostkeys verifies SSH server keys against an OpenSSH known_hosts
// file. Unknown hosts are trusted on first use (TOFU) and appended to the file;
// a key that changes afterwards is rejected.
package hostkeys

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/ssh"
	sshknownhosts "golang.org/x/crypto/ssh/knownhosts"
)

// Verifier is a concurrency-safe SSH host-key callback backed by path.
type Verifier struct {
	path string
	mu   sync.Mutex
}

// New returns a verifier backed by an OpenSSH known_hosts file. The file and
// its parent directory are created lazily on the first connection.
func New(path string) *Verifier { return &Verifier{path: path} }

// Callback implements ssh.HostKeyCallback. A previously unseen host is added
// to known_hosts, matching OpenSSH's StrictHostKeyChecking=accept-new behavior.
// Revoked keys, malformed files and changed keys are always rejected.
func (v *Verifier) Callback(hostname string, remote net.Addr, key ssh.PublicKey) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.ensureFile(); err != nil {
		return fmt.Errorf("prepare known_hosts: %w", err)
	}
	check, err := sshknownhosts.New(v.path)
	if err != nil {
		return fmt.Errorf("load known_hosts: %w", err)
	}
	if err := check(hostname, remote, key); err == nil {
		return nil
	} else {
		var unknown *sshknownhosts.KeyError
		if !errors.As(err, &unknown) {
			return fmt.Errorf("verify SSH host key for %s: %w", hostname, err)
		}
		if len(unknown.Want) != 0 {
			return fmt.Errorf(
				"SSH host key changed for %s (received %s); check or remove the old entry in %s: %w",
				hostname, ssh.FingerprintSHA256(key), v.path, err,
			)
		}
	}

	line := sshknownhosts.Line([]string{sshknownhosts.Normalize(hostname)}, key)
	if err := appendLine(v.path, line); err != nil {
		return fmt.Errorf("trust first SSH host key for %s: %w", hostname, err)
	}
	return nil
}

func (v *Verifier) ensureFile() error {
	if v.path == "" {
		return errors.New("known_hosts path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(v.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(v.path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// appendLine preserves a final line that lacks a newline instead of joining the
// new host key onto it and corrupting both entries.
func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() > 0 {
		last := []byte{0}
		if _, err := f.ReadAt(last, fi.Size()-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			if _, err := f.WriteString("\n"); err != nil {
				return err
			}
		}
	}
	_, err = f.WriteString(line + "\n")
	return err
}
