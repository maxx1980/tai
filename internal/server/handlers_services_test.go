package server

import (
	"testing"

	"webssh/internal/store"
)

func TestServiceURL(t *testing.T) {
	h := store.Host{
		Alias:     "pve1",
		Hostname:  "10.0.0.3",
		PVEPort:   8006,
		PBSPort:   8007,
		HTTPPort:  80,
		HTTPSPort: 443,
	}
	for kind, want := range map[string]string{
		// The Proxmox ports must survive into the URL: https://host/ is port 443,
		// a different server entirely.
		"pve": "https://10.0.0.3:8006/",
		"pbs": "https://10.0.0.3:8007/",
		// A port the scheme already implies is dropped, so the URL reads the way
		// a user would type it.
		"http":  "http://10.0.0.3/",
		"https": "https://10.0.0.3/",
	} {
		got, err := serviceURL(h, kind)
		if err != nil {
			t.Errorf("serviceURL(%q): %v", kind, err)
			continue
		}
		if got != want {
			t.Errorf("serviceURL(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestServiceURLNonDefaultPorts(t *testing.T) {
	h := store.Host{Alias: "web", Hostname: "example.com", HTTPPort: 8080, HTTPSPort: 8443}
	if got, _ := serviceURL(h, "http"); got != "http://example.com:8080/" {
		t.Errorf("http = %q", got)
	}
	if got, _ := serviceURL(h, "https"); got != "https://example.com:8443/" {
		t.Errorf("https = %q", got)
	}
}

func TestServiceURLFallsBackToAlias(t *testing.T) {
	h := store.Host{Alias: "box.lan", HTTPPort: 80}
	if got, _ := serviceURL(h, "http"); got != "http://box.lan/" {
		t.Errorf("got %q", got)
	}
}

func TestServiceURLRejects(t *testing.T) {
	h := store.Host{Alias: "plain", Hostname: "10.0.0.9"}
	if _, err := serviceURL(h, "pve"); err == nil {
		t.Error("an unconfigured service must be an error, not a URL to port 0")
	}
	if _, err := serviceURL(h, "gopher"); err == nil {
		t.Error("an unknown service kind must be rejected")
	}
	if _, err := serviceURL(store.Host{HTTPPort: 80}, "http"); err == nil {
		t.Error("a host with no address must be rejected")
	}
}

func TestUniqueAlias(t *testing.T) {
	taken := map[string]bool{"gw": true, "gw-2": true}
	if got := uniqueAlias("gw", "10.0.0.1", taken); got != "gw-3" {
		t.Errorf("got %q, want gw-3", got)
	}
	if got := uniqueAlias("", "10.0.0.5", taken); got != "10.0.0.5" {
		t.Errorf("empty alias should fall back to the hostname, got %q", got)
	}
	if got := uniqueAlias("", "", taken); got != "host" {
		t.Errorf("got %q, want host", got)
	}
}
