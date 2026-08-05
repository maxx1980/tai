package netscan

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func addrs(t *testing.T, spec string) []string {
	t.Helper()
	got, err := ParseTargets(spec)
	if err != nil {
		t.Fatalf("ParseTargets(%q): %v", spec, err)
	}
	out := make([]string, len(got))
	for i, a := range got {
		out[i] = a.String()
	}
	return out
}

func TestParseTargetsSingle(t *testing.T) {
	got := addrs(t, "192.168.23.24")
	if len(got) != 1 || got[0] != "192.168.23.24" {
		t.Fatalf("got %v", got)
	}
}

func TestParseTargetsOctetRange(t *testing.T) {
	got := addrs(t, "192.168.1.2-32")
	if len(got) != 31 {
		t.Fatalf("want 31 addresses, got %d (%v)", len(got), got)
	}
	if got[0] != "192.168.1.2" || got[len(got)-1] != "192.168.1.32" {
		t.Fatalf("bad bounds: %s .. %s", got[0], got[len(got)-1])
	}
}

func TestParseTargetsExplicitRange(t *testing.T) {
	got := addrs(t, "10.0.0.254-10.0.1.2")
	want := []string{"10.0.0.254", "10.0.0.255", "10.0.1.0", "10.0.1.1", "10.0.1.2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseTargetsReversedRange(t *testing.T) {
	got := addrs(t, "192.168.1.32-2")
	if len(got) != 31 || got[0] != "192.168.1.2" {
		t.Fatalf("reversed range should be normalised, got %v", got)
	}
}

func TestParseTargetsCIDRDropsNetworkAndBroadcast(t *testing.T) {
	got := addrs(t, "192.168.1.0/24")
	if len(got) != 254 {
		t.Fatalf("want 254 usable addresses, got %d", len(got))
	}
	if got[0] != "192.168.1.1" || got[253] != "192.168.1.254" {
		t.Fatalf("bad bounds: %s .. %s", got[0], got[253])
	}
}

func TestParseTargetsCIDRSmallPrefixesKeepAll(t *testing.T) {
	if got := addrs(t, "192.168.1.5/32"); len(got) != 1 || got[0] != "192.168.1.5" {
		t.Fatalf("/32 should be one host, got %v", got)
	}
	if got := addrs(t, "192.168.1.4/31"); len(got) != 2 {
		t.Fatalf("/31 should be two hosts, got %v", got)
	}
}

func TestParseTargetsMixedAndDeduped(t *testing.T) {
	got := addrs(t, "192.168.23.24, 192.168.1.2-4\n192.168.23.24")
	want := []string{"192.168.23.24", "192.168.1.2", "192.168.1.3", "192.168.1.4"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseTargetsRejects(t *testing.T) {
	for _, spec := range []string{
		"",
		"   ",
		"192.168.1.0/8",   // over the address cap
		"192.168.1.2-999", // not a last octet
		"192.168.1.300",   // not an address and not resolvable
		"192.168.1.2-::1", // mixed families
		"not a host at all!!",
	} {
		if got, err := ParseTargets(spec); err == nil {
			t.Errorf("ParseTargets(%q) should fail, got %v", spec, got)
		}
	}
}

func TestServiceName(t *testing.T) {
	for port, want := range map[int]string{
		22: "ssh", 2222: "ssh", 23: "telnet",
		80: "http", 443: "https", 8006: "pve", 8007: "pbs",
		9999: "tcp",
	} {
		if got := serviceName(port); got != want {
			t.Errorf("serviceName(%d) = %q, want %q", port, got, want)
		}
	}
}

func TestDefaultPortsCoverTheCardActions(t *testing.T) {
	want := map[int]bool{22: true, 23: true, 80: true, 443: true, 8006: true, 8007: true}
	for _, p := range DefaultPorts {
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("DefaultPorts is missing %v", want)
	}
}

func TestPrintableStopsAtNewlineAndStripsControl(t *testing.T) {
	got := printable([]byte("SSH-2.0-OpenSSH_9.6\r\nmore"))
	if got != "SSH-2.0-OpenSSH_9.6" {
		t.Fatalf("got %q", got)
	}
	if got := printable([]byte{0xff, 0xfd, 0x18, 'h', 'i'}); got != "hi" {
		t.Fatalf("telnet IAC bytes should be stripped, got %q", got)
	}
}

// TestScanFindsListener starts a fake SSH server on loopback and checks the scan
// reports the port as open, names it, and captures the banner.
func TestScanFindsListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("SSH-2.0-TestServer\r\n"))
			_ = c.Close()
		}
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	// A closed port on the same address must not show up.
	closedLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, closedStr, _ := net.SplitHostPort(closedLn.Addr().String())
	closedPort, _ := strconv.Atoi(closedStr)
	closedLn.Close()

	hosts := Scan(context.Background(), []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Options{
		Ports:   []int{port, closedPort},
		Timeout: time.Second,
	})
	if len(hosts) != 1 {
		t.Fatalf("want 1 host, got %d (%v)", len(hosts), hosts)
	}
	if len(hosts[0].Services) != 1 || hosts[0].Services[0].Port != port {
		t.Fatalf("want only the open port %d, got %+v", port, hosts[0].Services)
	}
	if hosts[0].Services[0].Banner != "SSH-2.0-TestServer" {
		t.Fatalf("banner = %q", hosts[0].Services[0].Banner)
	}
}

func TestScanReturnsNothingForDeadAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	ln.Close() // nothing is listening now

	hosts := Scan(context.Background(), []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Options{
		Ports:   []int{port},
		Timeout: 300 * time.Millisecond,
	})
	if len(hosts) != 0 {
		t.Fatalf("want no hosts, got %v", hosts)
	}
}
