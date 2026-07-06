// Package knownhosts parses, formats and syncs OpenSSH known_hosts entries that
// webssh manages, mirroring how internal/keys handles key files. Entries are
// matched by their host pattern set plus public-key material, so a copy is
// recognised in ~/.ssh regardless of any trailing comment.
package knownhosts

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	"webssh/internal/store"
)

// Parse turns the contents of a known_hosts file into managed entries. Malformed
// or unparseable lines (and comments/blanks) are skipped so one bad line does not
// abort the whole import.
func Parse(data []byte) []store.KnownHost {
	var out []store.KnownHost
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		marker, hosts, pub, comment, _, err := ssh.ParseKnownHosts([]byte(line))
		if err != nil || len(hosts) == 0 {
			continue
		}
		out = append(out, store.KnownHost{
			Marker:  marker,
			Hosts:   strings.Join(hosts, ","),
			KeyType: pub.Type(),
			KeyData: base64.StdEncoding.EncodeToString(pub.Marshal()),
			Comment: comment,
		})
	}
	return out
}

// pubKey reconstructs the ssh.PublicKey from a stored entry's base64 key data.
func pubKey(kh store.KnownHost) (ssh.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(kh.KeyData)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePublicKey(raw)
}

// Identity is the match key for an entry: its host set plus key material. Two
// entries with the same identity are considered the same (comment aside).
func Identity(kh store.KnownHost) string { return kh.Hosts + "|" + kh.KeyData }

// Fingerprint returns the SHA256 fingerprint of an entry's key ("" if invalid).
func Fingerprint(kh store.KnownHost) string {
	pub, err := pubKey(kh)
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(pub)
}

// Line renders an entry as a known_hosts file line (no trailing newline).
func Line(kh store.KnownHost) string {
	pub, err := pubKey(kh)
	if err != nil {
		return ""
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) // "type base64"
	parts := make([]string, 0, 4)
	if kh.Marker != "" {
		parts = append(parts, kh.Marker)
	}
	parts = append(parts, kh.Hosts, authorized)
	line := strings.Join(parts, " ")
	if kh.Comment != "" {
		line += " " + kh.Comment
	}
	return line
}

// IndexSSH returns the set of Identity() values present in the known_hosts file
// at path (empty if the file is missing).
func IndexSSH(path string) map[string]bool {
	set := map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return set
	}
	for _, kh := range Parse(data) {
		set[Identity(kh)] = true
	}
	return set
}

// ExportToSSH appends an entry to the known_hosts file at path, unless an
// equivalent entry is already present.
func ExportToSSH(path string, kh store.KnownHost) error {
	line := Line(kh)
	if line == "" {
		return fmt.Errorf("entry has no valid public key")
	}
	if IndexSSH(path)[Identity(kh)] {
		return nil // already there — idempotent
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// RemoveFromSSH rewrites the known_hosts file at path without any line whose
// identity matches kh. Unparseable and comment lines are preserved verbatim.
func RemoveFromSSH(path string, kh store.KnownHost) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	target := Identity(kh)
	var kept []string
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			kept = append(kept, raw)
			continue
		}
		marker, hosts, pub, comment, _, perr := ssh.ParseKnownHosts([]byte(trimmed))
		if perr != nil || len(hosts) == 0 {
			kept = append(kept, raw) // keep anything we can't understand
			continue
		}
		cur := store.KnownHost{
			Marker:  marker,
			Hosts:   strings.Join(hosts, ","),
			KeyData: base64.StdEncoding.EncodeToString(pub.Marshal()),
			Comment: comment,
		}
		if Identity(cur) == target {
			continue // drop the matching entry
		}
		kept = append(kept, raw)
	}
	out := strings.Join(kept, "\n")
	if out != "" {
		out += "\n"
	}
	return os.WriteFile(path, []byte(out), 0o644)
}
