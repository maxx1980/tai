package knownhosts

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"testing"

	"webssh/internal/store"
)

// hashName builds a token the way ssh does with HashKnownHosts.
func hashName(salt []byte, name string) string {
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(name))
	return "|1|" + base64.StdEncoding.EncodeToString(salt) + "|" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// A real line from `ssh-keygen -H` style output: |1|salt|hash for "192.168.1.5".
func TestMatchHashedAgainstOpenSSHToken(t *testing.T) {
	salt := []byte("0123456789abcdefghij")
	tok := hashName(salt, "192.168.1.5")
	if !MatchPattern(tok, "192.168.1.5") {
		t.Fatal("hashed token does not match its own name")
	}
	if MatchPattern(tok, "192.168.1.6") {
		t.Fatal("hashed token matched another name")
	}
	if MatchPattern("|1|garbage", "192.168.1.5") {
		t.Fatal("malformed token matched")
	}
}

func TestMatchPlainPatterns(t *testing.T) {
	for _, tc := range []struct {
		field, name string
		want        bool
	}{
		{"pve.lan,192.168.168.2", "192.168.168.2", true},
		{"PVE.lan", "pve.lan", true},
		{"*.lan", "db.lan", true},
		{"*.lan,!db.lan", "db.lan", false},
		{"[git.example.com]:2222", "[git.example.com]:2222", true},
		{"git.example.com", "[git.example.com]:2222", false},
	} {
		if got := MatchPattern(tc.field, tc.name); got != tc.want {
			t.Errorf("MatchPattern(%q, %q) = %v, want %v", tc.field, tc.name, got, tc.want)
		}
	}
}

func TestMatchesInventory(t *testing.T) {
	salt := []byte("saltsaltsaltsaltsalt")
	hosts := []store.Host{
		{ID: 1, Alias: "pox", Hostname: "192.168.1.5", Port: 22},
		{ID: 2, Alias: "git", Hostname: "Git.Example.com", Port: 2222},
		{ID: 3, Alias: "db", Hostname: "192.168.168.6", Port: 22},
	}
	cases := []struct {
		hosts string
		want  int64
		name  string
	}{
		{hashName(salt, "192.168.1.5"), 1, "192.168.1.5"},
		{hashName(salt, "[git.example.com]:2222"), 2, "[git.example.com]:2222"},
		{hashName(salt, "db"), 3, "db"}, // recorded under the alias (ssh db)
	}
	for _, tc := range cases {
		m := Matches(store.KnownHost{Hosts: tc.hosts}, hosts)
		if len(m) != 1 || m[0].HostID != tc.want || m[0].Name != tc.name {
			t.Errorf("Matches(%q) = %+v, want host %d via %q", tc.name, m, tc.want, tc.name)
		}
	}
	if m := Matches(store.KnownHost{Hosts: hashName(salt, "10.9.9.9")}, hosts); len(m) != 0 {
		t.Errorf("unknown host matched %+v", m)
	}
}
