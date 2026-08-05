package health

import (
	"testing"

	"webssh/internal/store"
)

func TestPortOfPrefersSSH(t *testing.T) {
	h := store.Host{Port: 2200, TelnetPort: 23, PVEPort: 8006}
	if got := portOf(h); got != 2200 {
		t.Fatalf("got %d, want the SSH port 2200", got)
	}
}

// A host with no SSH is still worth probing — its dot should reflect whichever
// service it does expose, not sit at "unknown" forever.
func TestPortOfFallsBackToAServicePort(t *testing.T) {
	for name, tc := range map[string]struct {
		host store.Host
		want int
	}{
		"telnet only": {store.Host{TelnetPort: 23}, 23},
		"pve only":    {store.Host{PVEPort: 8006}, 8006},
		"pbs only":    {store.Host{PBSPort: 8007}, 8007},
		"https over http": {
			store.Host{HTTPPort: 80, HTTPSPort: 443}, 443,
		},
		"nothing at all": {store.Host{Alias: "bare"}, 0},
	} {
		if got := portOf(tc.host); got != tc.want {
			t.Errorf("%s: portOf = %d, want %d", name, got, tc.want)
		}
	}
}

func TestHostOfFallsBackToAlias(t *testing.T) {
	if got := hostOf(store.Host{Alias: "box", Hostname: "10.0.0.1"}); got != "10.0.0.1" {
		t.Errorf("got %q", got)
	}
	if got := hostOf(store.Host{Alias: "box"}); got != "box" {
		t.Errorf("got %q", got)
	}
}
