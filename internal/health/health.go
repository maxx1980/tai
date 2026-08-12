// Package health runs background reachability checks against inventory hosts and
// exposes their status (green/yellow/red) as an in-memory snapshot for the API.
//
// The probe deliberately does NOT perform a full SSH login (no auth, no account
// lockout risk, no auth-log spam). It only asks two cheap questions:
//   - is the host's primary TCP port open?  → green
//   - else, does the host answer ICMP ping? → yellow
//   - neither                               → red
//
// The primary port is SSH when the host has one; hosts without SSH (a telnet
// box, a Proxmox appliance reached only over its web UI) fall back to whichever
// service port they do expose, so their dot still means something.
package health

import (
	"context"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"

	"webssh/internal/store"
)

// State is a host's coarse reachability level. A host absent from the snapshot
// is treated by the frontend as "unknown" (not yet checked).
type State string

const (
	Green  State = "green"  // SSH TCP port open — fully reachable
	Yellow State = "yellow" // answers ping but SSH port closed
	Red    State = "red"    // no ping, no port — unreachable
)

const (
	interval    = 60 * time.Second // re-check cadence
	dialTimeout = 3 * time.Second  // TCP connect timeout per host
	maxParallel = 10               // cap concurrent probes
)

// Checker periodically probes every host and caches the latest state.
type Checker struct {
	st *store.Store

	mu     sync.RWMutex
	status map[int64]State
}

// New builds a Checker over the given store. Call Run to start the loop.
func New(st *store.Store) *Checker {
	return &Checker{st: st, status: map[int64]State{}}
}

// Run probes immediately, then every interval, until ctx is cancelled.
func (c *Checker) Run(ctx context.Context) {
	c.checkAll(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.checkAll(ctx)
		}
	}
}

// Snapshot returns a copy of the current per-host state, safe to hand to a JSON
// encoder while checks keep running.
func (c *Checker) Snapshot() map[int64]State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[int64]State, len(c.status))
	for id, st := range c.status {
		out[id] = st
	}
	return out
}

// checkAll probes every host in parallel (bounded) and replaces the status map,
// so entries for deleted hosts drop out.
func (c *Checker) checkAll(ctx context.Context) {
	hosts, err := c.st.ListHosts()
	if err != nil {
		return
	}

	next := make(map[int64]State, len(hosts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)

	for _, h := range hosts {
		wg.Add(1)
		go func(h store.Host) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st := c.probe(ctx, h)
			mu.Lock()
			next[h.ID] = st
			mu.Unlock()
		}(h)
	}
	wg.Wait()

	c.mu.Lock()
	c.status = next
	c.mu.Unlock()
}

// probe returns the reachability state for a single host: primary port open →
// green, else ping-only → yellow, else red. A host with no port at all is judged
// on ping alone.
func (c *Checker) probe(ctx context.Context, h store.Host) State {
	host := hostOf(h)

	if port := portOf(h); port > 0 {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		d := net.Dialer{Timeout: dialTimeout}
		if conn, err := d.DialContext(ctx, "tcp", addr); err == nil {
			_ = conn.Close()
			return Green
		}
	}
	if pingOK(ctx, host) {
		return Yellow
	}
	return Red
}

// pingOK reports whether host answers a single ICMP echo within ~1s. It shells
// out to the system ping, mirroring the app's existing habit of exec-ing the
// ssh binary; if ping is missing it simply reports false.
//
// -W means two different things depending on the ping: GNU ping (Linux) takes
// seconds, BSD ping (macOS) takes milliseconds — using the Linux value on
// Darwin would time out every host after 1ms and mark it red.
func pingOK(ctx context.Context, host string) bool {
	wait := "1"
	if runtime.GOOS == "darwin" {
		wait = "1000"
	}
	cmd := exec.CommandContext(ctx, "ping", "-c", "1", "-W", wait, "-n", host)
	return cmd.Run() == nil
}

// hostOf resolves the address to probe: the explicit hostname, or the alias when
// none is set (matching the fallback used by the SSH dialers).
func hostOf(h store.Host) string {
	if h.Hostname != "" {
		return h.Hostname
	}
	return h.Alias
}

// portOf picks the port that best represents "this host is up": SSH when it has
// one, otherwise the first other service it exposes. Returns 0 when the host has
// no ports at all.
func portOf(h store.Host) int {
	for _, p := range []int{h.Port, h.TelnetPort, h.PVEPort, h.PBSPort, h.HTTPSPort, h.HTTPPort} {
		if p > 0 {
			return p
		}
	}
	return 0
}
