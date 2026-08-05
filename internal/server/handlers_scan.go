package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"webssh/internal/netscan"
	"webssh/internal/store"
)

// maxProbes caps the total connect attempts one scan may make (targets × ports),
// keeping a single request bounded no matter how the two are combined.
const maxProbes = 20000

// handleScan expands a target specification (single address, a.b.c.x-y range, or
// CIDR network) and reports which of those hosts have the requested TCP ports
// open. It is a plain connect scan: no login is attempted, so nothing lands in
// the target's auth log beyond a dropped connection.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Targets   string `json:"targets"`
		Ports     []int  `json:"ports"`
		TimeoutMS int    `json:"timeout_ms"`
		Resolve   bool   `json:"resolve"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(body.Targets) == "" {
		writeErr(w, 400, fmt.Errorf("enter at least one address, range or network"))
		return
	}

	targets, err := netscan.ParseTargets(body.Targets)
	if err != nil {
		writeErr(w, 400, err)
		return
	}

	ports, err := cleanPorts(body.Ports)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if len(targets)*len(ports) > maxProbes {
		writeErr(w, 400, fmt.Errorf("%d addresses × %d ports exceeds the %d-probe limit — scan a smaller range",
			len(targets), len(ports), maxProbes))
		return
	}

	timeout := time.Duration(body.TimeoutMS) * time.Millisecond
	switch {
	case timeout <= 0:
		timeout = 1500 * time.Millisecond
	case timeout < 200*time.Millisecond:
		timeout = 200 * time.Millisecond
	case timeout > 5*time.Second:
		timeout = 5 * time.Second
	}

	start := time.Now()
	hosts := netscan.Scan(r.Context(), targets, netscan.Options{
		Ports:   ports,
		Timeout: timeout,
		Resolve: body.Resolve,
	})
	writeJSON(w, 200, map[string]any{
		"hosts":      hosts,
		"scanned":    len(targets),
		"ports":      ports,
		"elapsed_ms": time.Since(start).Milliseconds(),
	})
}

// cleanPorts validates and de-duplicates the requested port list, falling back
// to the SSH/telnet defaults when none was given.
func cleanPorts(in []int) ([]int, error) {
	if len(in) == 0 {
		return netscan.DefaultPorts, nil
	}
	if len(in) > 16 {
		return nil, fmt.Errorf("at most 16 ports per scan")
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(in))
	for _, p := range in {
		if p < 1 || p > 65535 {
			return nil, fmt.Errorf("port %d is out of range", p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// handleBulkCreateHosts adds several hosts in one request — the "add the
// discovered machines" step of a scan. Aliases collide often here (two scans of
// the same subnet, or a host already in the inventory), so each alias is made
// unique with a numeric suffix rather than failing the whole batch. ssh_config
// is exported once at the end instead of once per host.
func (s *Server) handleBulkCreateHosts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hosts []store.Host `json:"hosts"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if len(body.Hosts) == 0 {
		writeErr(w, 400, fmt.Errorf("no hosts to add"))
		return
	}

	existing, err := s.st.ListHosts()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	taken := make(map[string]bool, len(existing))
	for _, h := range existing {
		taken[h.Alias] = true
	}

	created := make([]store.Host, 0, len(body.Hosts))
	var failed []string
	for _, h := range body.Hosts {
		h.ID = 0
		h.Alias = uniqueAlias(strings.TrimSpace(h.Alias), h.Hostname, taken)
		row, err := s.st.CreateHost(h)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", h.Alias, err))
			continue
		}
		taken[row.Alias] = true
		created = append(created, row)
	}
	if len(created) > 0 {
		s.autoExport()
	}
	writeJSON(w, 200, map[string]any{
		"created": len(created),
		"hosts":   created,
		"failed":  failed,
	})
}

// uniqueAlias returns an alias not already in taken, falling back to the
// hostname (and finally to "host") when none was supplied.
func uniqueAlias(alias, hostname string, taken map[string]bool) string {
	base := alias
	if base == "" {
		base = strings.TrimSpace(hostname)
	}
	if base == "" {
		base = "host"
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}
