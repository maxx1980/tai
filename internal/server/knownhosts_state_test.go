package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webssh/internal/knownhosts"
	"webssh/internal/store"
)

const (
	khGitHub = "github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
	khStored = "pox ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHNSK2MzhuINhxpQ0ZcPmhrygEmwlMxBl6dTYKmuuuJO"
)

func stateKnownHosts(t *testing.T, s *Server) []store.KnownHost {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleState(rec, httptest.NewRequest("GET", "/api/state", nil))
	if rec.Code != 200 {
		t.Fatalf("state: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		KnownHosts []store.KnownHost `json:"known_hosts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.KnownHosts
}

// Every key ssh trusts is listed, not only the ones imported into webssh.
func TestStateListsSSHOnlyKnownHosts(t *testing.T) {
	s, st := newSecurityTestServer(t)
	if err := os.MkdirAll(s.paths.SSHDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.paths.SSHDir, "known_hosts")
	if err := os.WriteFile(path, []byte(khStored+"\n"+khGitHub+"\n"+khGitHub+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateKnownHost(knownhosts.Parse([]byte(khStored))[0]); err != nil {
		t.Fatal(err)
	}

	list := stateKnownHosts(t, s)
	if len(list) != 2 {
		t.Fatalf("got %d entries, want the stored one plus github.com once: %+v", len(list), list)
	}
	var db, only store.KnownHost
	for _, kh := range list {
		if kh.InDB {
			db = kh
		} else {
			only = kh
		}
	}
	if db.Hosts != "pox" || !db.InSSH || db.ID <= 0 {
		t.Errorf("stored entry = %+v", db)
	}
	if only.Hosts != "github.com" || !only.InSSH || only.ID >= 0 || only.Line == "" {
		t.Errorf("ssh-only entry = %+v", only)
	}

	// Removing it by line takes it out of ~/.ssh/known_hosts.
	rec := httptest.NewRecorder()
	s.handleSSHRemoveKnownHostLine(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"line":`+mustJSON(only.Line)+`}`)))
	if rec.Code != 200 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "github.com") || !strings.Contains(string(data), "pox ") {
		t.Errorf("known_hosts after remove:\n%s", data)
	}
	if got := stateKnownHosts(t, s); len(got) != 1 {
		t.Errorf("after remove got %d entries, want 1", len(got))
	}
}

func mustJSON(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
