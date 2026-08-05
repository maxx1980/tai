// Package netscan discovers reachable hosts on the local network by probing a
// small set of TCP ports (SSH and telnet by default).
//
// It is deliberately a connect-scan and nothing more: it opens a TCP connection,
// optionally reads the greeting banner, and closes it. No SYN tricks, no auth
// attempt, no OS fingerprinting — the point is to populate the inventory with
// machines the user already administers, not to audit someone else's network.
package netscan

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxTargets caps how many addresses one scan may expand to, so a stray /8 in
// the target box can't turn into a multi-hour job.
const MaxTargets = 4096

// DefaultPorts is what the UI offers out of the box: the two shells (SSH,
// telnet), plain and TLS HTTP, and the Proxmox VE / Backup Server web UIs —
// exactly the services an inventory card can act on.
var DefaultPorts = []int{22, 23, 80, 443, 8006, 8007}

// Service is one open port found on a host.
type Service struct {
	Port   int    `json:"port"`
	Name   string `json:"name"`             // "ssh", "telnet", or "tcp"
	Banner string `json:"banner,omitempty"` // first printable line the peer sent
}

// Host is a scanned address that had at least one open port.
type Host struct {
	Address  string    `json:"address"`
	Hostname string    `json:"hostname,omitempty"` // reverse DNS, best effort
	Services []Service `json:"services"`
}

// HasService reports whether the host answered on a port named name.
func (h Host) HasService(name string) bool {
	for _, s := range h.Services {
		if s.Name == name {
			return true
		}
	}
	return false
}

// Options tunes a scan. The zero value is usable; Scan fills in defaults.
type Options struct {
	Ports    []int         // ports to probe (default DefaultPorts)
	Timeout  time.Duration // per-port TCP connect timeout (default 1.5s)
	Parallel int           // max concurrent probes (default 128)
	Resolve  bool          // reverse-DNS the hosts that answered
}

func (o *Options) applyDefaults() {
	if len(o.Ports) == 0 {
		o.Ports = DefaultPorts
	}
	if o.Timeout <= 0 {
		o.Timeout = 1500 * time.Millisecond
	}
	if o.Parallel <= 0 {
		o.Parallel = 128
	}
}

// ParseTargets expands a user-supplied target specification into addresses.
//
// Tokens are separated by commas, semicolons or any whitespace, and each one may be:
//
//	192.168.23.24            a single address (IPv4 or IPv6)
//	192.168.1.2-32           a last-octet range (inclusive)
//	192.168.1.2-192.168.1.32 an explicit start-end range (IPv4)
//	192.168.1.0/24           a network in CIDR notation
//	gateway.lan              a hostname, resolved via DNS
//
// A CIDR block wider than /31 drops its network and broadcast addresses, since
// neither is a machine you can add to the inventory.
func ParseTargets(spec string) ([]netip.Addr, error) {
	fields := strings.FieldsFunc(spec, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	if len(fields) == 0 {
		return nil, fmt.Errorf("no targets given")
	}

	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	add := func(a netip.Addr) error {
		a = a.Unmap()
		if seen[a] {
			return nil
		}
		if len(out) >= MaxTargets {
			return fmt.Errorf("too many addresses (limit %d) — scan a smaller range", MaxTargets)
		}
		seen[a] = true
		out = append(out, a)
		return nil
	}

	for _, tok := range fields {
		addrs, err := expandToken(tok)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			if err := add(a); err != nil {
				return nil, err
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no usable addresses in %q", spec)
	}
	return out, nil
}

// expandToken turns one target token into the addresses it covers.
func expandToken(tok string) ([]netip.Addr, error) {
	switch {
	case strings.Contains(tok, "/"):
		return expandCIDR(tok)
	case strings.Contains(tok, "-"):
		return expandRange(tok)
	}
	if a, err := netip.ParseAddr(tok); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	return resolveHost(tok)
}

func expandCIDR(tok string) ([]netip.Addr, error) {
	p, err := netip.ParsePrefix(tok)
	if err != nil {
		return nil, fmt.Errorf("bad network %q: expected something like 192.168.1.0/24", tok)
	}
	p = p.Masked()
	bits := p.Addr().BitLen()
	if hostBits := bits - p.Bits(); hostBits > 20 {
		return nil, fmt.Errorf("network %s is too large (limit %d addresses)", tok, MaxTargets)
	}

	var out []netip.Addr
	for a := p.Addr(); a.IsValid() && p.Contains(a); a = a.Next() {
		out = append(out, a)
		if len(out) > MaxTargets+2 { // +2 covers the network/broadcast trim below
			return nil, fmt.Errorf("network %s is too large (limit %d addresses)", tok, MaxTargets)
		}
	}
	// Drop network + broadcast for real IPv4 subnets; /31 and /32 are all hosts.
	if p.Addr().Is4() && p.Bits() < 31 && len(out) > 2 {
		out = out[1 : len(out)-1]
	}
	return out, nil
}

// expandRange handles both "192.168.1.2-32" and "192.168.1.2-192.168.1.32".
func expandRange(tok string) ([]netip.Addr, error) {
	lo, hi, ok := strings.Cut(tok, "-")
	if !ok {
		return nil, fmt.Errorf("bad range %q", tok)
	}
	lo, hi = strings.TrimSpace(lo), strings.TrimSpace(hi)

	start, err := netip.ParseAddr(lo)
	if err != nil {
		return nil, fmt.Errorf("bad range %q: %s is not an IP address", tok, lo)
	}
	start = start.Unmap()

	var end netip.Addr
	if strings.ContainsAny(hi, ".:") {
		end, err = netip.ParseAddr(hi)
		if err != nil {
			return nil, fmt.Errorf("bad range %q: %s is not an IP address", tok, hi)
		}
		end = end.Unmap()
	} else {
		// Shorthand: only the final octet of an IPv4 address was given.
		if !start.Is4() {
			return nil, fmt.Errorf("bad range %q: the a.b.c.x-y form is IPv4 only", tok)
		}
		n, err := strconv.Atoi(hi)
		if err != nil || n < 0 || n > 255 {
			return nil, fmt.Errorf("bad range %q: %s is not a last octet (0-255)", tok, hi)
		}
		b := start.As4()
		b[3] = byte(n)
		end = netip.AddrFrom4(b)
	}

	if start.BitLen() != end.BitLen() {
		return nil, fmt.Errorf("bad range %q: mixing IPv4 and IPv6", tok)
	}
	if end.Less(start) {
		start, end = end, start
	}

	var out []netip.Addr
	for a := start; ; a = a.Next() {
		out = append(out, a)
		if a == end {
			break
		}
		if len(out) > MaxTargets || !a.Next().IsValid() {
			return nil, fmt.Errorf("range %s is too large (limit %d addresses)", tok, MaxTargets)
		}
	}
	return out, nil
}

// resolveHost turns a hostname into its addresses, so users can paste a name
// alongside numeric targets.
func resolveHost(tok string) ([]netip.Addr, error) {
	ips, err := net.LookupIP(tok)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("%q is not an IP address, range or network, and does not resolve", tok)
	}
	var out []netip.Addr
	for _, ip := range ips {
		if a, ok := netip.AddrFromSlice(ip); ok {
			out = append(out, a.Unmap())
		}
	}
	return out, nil
}

// Scan probes every target on every port and returns the hosts that answered,
// sorted by address. Targets that answer nothing are omitted entirely.
func Scan(ctx context.Context, targets []netip.Addr, opts Options) []Host {
	opts.applyDefaults()

	type probe struct {
		addr netip.Addr
		port int
	}
	type found struct {
		addr netip.Addr
		svc  Service
	}

	workers := opts.Parallel
	if n := len(targets) * len(opts.Ports); workers > n {
		workers = n
	}
	if workers < 1 {
		return nil
	}

	jobs := make(chan probe)
	results := make(chan found, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if svc, ok := dial(ctx, j.addr, j.port, opts.Timeout); ok {
					results <- found{j.addr, svc}
				}
			}
		}()
	}

	// Feed the workers; a cancelled request stops the scan early rather than
	// holding the connection open for the full target list.
	go func() {
		defer close(jobs)
		for _, a := range targets {
			for _, p := range opts.Ports {
				select {
				case jobs <- probe{a, p}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	byAddr := map[netip.Addr][]Service{}
	for r := range results {
		byAddr[r.addr] = append(byAddr[r.addr], r.svc)
	}

	out := make([]Host, 0, len(byAddr))
	for a, svcs := range byAddr {
		sort.Slice(svcs, func(i, j int) bool { return svcs[i].Port < svcs[j].Port })
		out = append(out, Host{Address: a.String(), Services: svcs})
	}
	sort.Slice(out, func(i, j int) bool {
		ai, _ := netip.ParseAddr(out[i].Address)
		aj, _ := netip.ParseAddr(out[j].Address)
		return ai.Less(aj)
	})

	if opts.Resolve {
		resolveNames(ctx, out)
	}
	return out
}

// dial opens a TCP connection and, when it succeeds, tries to read the greeting
// banner. A closed port, a filtered port and a timeout are all just "not open".
func dial(ctx context.Context, addr netip.Addr, port int, timeout time.Duration) (Service, bool) {
	target := net.JoinHostPort(addr.String(), strconv.Itoa(port))
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return Service{}, false
	}
	defer conn.Close()

	svc := Service{Port: port, Name: serviceName(port)}

	// Servers that speak first (SSH, telnet) identify themselves immediately;
	// for anything else this just times out cheaply and we report the bare port.
	_ = conn.SetReadDeadline(time.Now().Add(minDur(timeout, 900*time.Millisecond)))
	buf := make([]byte, 128)
	if n, err := conn.Read(buf); err == nil && n > 0 {
		svc.Banner = printable(buf[:n])
	}
	return svc, true
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// serviceName labels the well-known ports this app cares about. The names match
// the host record's service fields, so a scan result maps straight onto a card.
func serviceName(port int) string {
	switch port {
	case 22, 2222:
		return "ssh"
	case 23, 2323:
		return "telnet"
	case 80, 8080:
		return "http"
	case 443, 8443:
		return "https"
	case 8006:
		return "pve"
	case 8007:
		return "pbs"
	default:
		return "tcp"
	}
}

// printable keeps the first line of a banner and strips control bytes, so a
// telnet server's IAC negotiation doesn't end up in the UI as mojibake.
func printable(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c == '\n' || c == '\r' {
			break
		}
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		}
	}
	return strings.TrimSpace(sb.String())
}

// resolveNames fills in reverse DNS for the hosts that answered. It is advisory
// only — a missing PTR record just leaves the name empty.
func resolveNames(ctx context.Context, hosts []Host) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for i := range hosts {
		names, err := net.DefaultResolver.LookupAddr(ctx, hosts[i].Address)
		if err != nil || len(names) == 0 {
			continue
		}
		hosts[i].Hostname = strings.TrimSuffix(names[0], ".")
	}
}
