// Package sshconfig imports hosts from ~/.ssh/config and exports the inventory
// to a managed file included from the main config. The main config is never
// rewritten: we only ensure a single idempotent Include line (after backup).
package sshconfig

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"webssh/internal/store"
)

// managedHeader is written at the top of the managed file to make its origin clear.
const managedHeader = `# Managed by webssh — do not edit by hand.
# Edit hosts in the webssh UI; this file is regenerated on export.
`

// Import parses an ssh_config file at path into host records.
func Import(path string) ([]store.Host, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads ssh_config content from r into host records. Blocks whose Host
// pattern contains wildcards (* ?) or lists multiple patterns are skipped,
// since they are global defaults rather than concrete hosts.
func Parse(r io.Reader) ([]store.Host, error) {
	var hosts []store.Host
	var cur *store.Host
	flush := func() {
		if cur != nil && cur.Alias != "" {
			hosts = append(hosts, *cur)
		}
		cur = nil
	}

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val := splitKV(line)
		lkey := strings.ToLower(key)
		if lkey == "host" {
			flush()
			patterns := strings.Fields(val)
			if len(patterns) != 1 || strings.ContainsAny(patterns[0], "*?!") {
				continue // global/wildcard block — skip
			}
			cur = &store.Host{Alias: patterns[0], Port: 22, ExtraOptions: map[string]string{}}
			continue
		}
		if cur == nil {
			continue // keyword outside a concrete Host block
		}
		switch lkey {
		case "hostname":
			cur.Hostname = val
		case "user":
			cur.User = val
		case "port":
			if p, err := strconv.Atoi(val); err == nil {
				cur.Port = p
			}
		case "identityfile":
			cur.IdentityFile = val
		case "proxyjump":
			cur.ProxyJump = val
		default:
			cur.ExtraOptions[key] = val
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return hosts, nil
}

func splitKV(line string) (key, val string) {
	// ssh_config allows "Key value" or "Key=value".
	if i := strings.IndexAny(line, " \t="); i >= 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(strings.TrimLeft(line[i:], " \t="))
	}
	return line, ""
}

// Export writes all hosts to the managed file and ensures the main config
// Includes it. mainPath is backed up before its first modification.
func Export(hosts []store.Host, managedPath, mainPath string) error {
	if err := os.MkdirAll(filepath.Dir(managedPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(managedPath, []byte(render(hosts)), 0o600); err != nil {
		return err
	}
	return ensureInclude(mainPath, managedPath)
}

func render(hosts []store.Host) string {
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Alias < hosts[j].Alias })
	var b strings.Builder
	b.WriteString(managedHeader)
	for _, h := range hosts {
		// A host with no SSH port is not an ssh target (telnet box, appliance
		// web UI) — emitting it would give `ssh <alias>` a broken entry.
		if h.Port == 0 {
			continue
		}
		fmt.Fprintf(&b, "\nHost %s\n", h.Alias)
		if h.Hostname != "" {
			fmt.Fprintf(&b, "    HostName %s\n", h.Hostname)
		}
		if h.User != "" {
			fmt.Fprintf(&b, "    User %s\n", h.User)
		}
		if h.Port != 0 && h.Port != 22 {
			fmt.Fprintf(&b, "    Port %d\n", h.Port)
		}
		if h.IdentityFile != "" {
			fmt.Fprintf(&b, "    IdentityFile %s\n", h.IdentityFile)
		}
		if h.ProxyJump != "" {
			fmt.Fprintf(&b, "    ProxyJump %s\n", h.ProxyJump)
		}
		keys := make([]string, 0, len(h.ExtraOptions))
		for k := range h.ExtraOptions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "    %s %s\n", k, h.ExtraOptions[k])
		}
	}
	return b.String()
}

// ensureInclude prepends `Include <managedPath>` to mainPath if not already
// present. The main config is backed up (once per call) before writing.
func ensureInclude(mainPath, managedPath string) error {
	includeLine := "Include " + managedPath
	existing, err := os.ReadFile(mainPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(existing), includeLine) {
		return nil // already included
	}
	if len(existing) > 0 {
		backup := fmt.Sprintf("%s.webssh-bak-%s", mainPath, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, existing, 0o600); err != nil {
			return fmt.Errorf("backup main config: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o700); err != nil {
		return err
	}
	// Include must appear before any `Host`/`Match` block to apply globally,
	// so we prepend it.
	var b strings.Builder
	b.WriteString("# Added by webssh\n")
	b.WriteString(includeLine + "\n\n")
	b.Write(existing)
	return os.WriteFile(mainPath, []byte(b.String()), 0o600)
}
