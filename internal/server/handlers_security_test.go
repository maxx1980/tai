package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webssh/internal/backup"
	"webssh/internal/config"
	"webssh/internal/store"
)

func newSecurityTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dataDir, "webssh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sshDir := filepath.Join(root, ".ssh")
	s := &Server{
		st: st,
		paths: config.Paths{
			Home:          root,
			DataDir:       dataDir,
			SSHDir:        sshDir,
			SSHConfig:     filepath.Join(sshDir, "config"),
			ManagedConfig: filepath.Join(sshDir, "config.d", "inventory"),
		},
		sessions: map[string]struct{}{},
	}
	return s, st
}

func TestRestoreRefreshesAuthDisabledCache(t *testing.T) {
	s, st := newSecurityTestServer(t)
	if err := st.SetSetting(config.KeyAuthDisabled, "1"); err != nil {
		t.Fatal(err)
	}
	s.syncAuthDisabled()
	if !s.authDisabled.Load() {
		t.Fatal("test precondition: auth bypass is not enabled")
	}

	plain, err := json.Marshal(store.Snapshot{
		Version:  1,
		Settings: map[string]string{config.KeyAuthDisabled: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := backup.Encrypt("restore-password", plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, code, err := s.restoreEncrypted("restore-password", encrypted); err != nil || code != http.StatusOK {
		t.Fatalf("restoreEncrypted = code %d, err %v", code, err)
	}
	if s.authDisabled.Load() {
		t.Fatal("auth bypass remained enabled after restore")
	}
}

func TestResetRefreshesAuthDisabledCache(t *testing.T) {
	s, st := newSecurityTestServer(t)
	hash, err := backup.HashPassword("master-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(config.KeyMasterPassword, hash); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(config.KeyAuthDisabled, "1"); err != nil {
		t.Fatal(err)
	}
	s.syncAuthDisabled()
	if !s.authDisabled.Load() {
		t.Fatal("test precondition: auth bypass is not enabled")
	}

	r := httptest.NewRequest(http.MethodPost, "/api/reset", strings.NewReader(`{"password":"master-password"}`))
	w := httptest.NewRecorder()
	s.handleReset(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("reset status = %d, body = %s", w.Code, w.Body.String())
	}
	if s.authDisabled.Load() {
		t.Fatal("auth bypass remained enabled after reset")
	}
}
