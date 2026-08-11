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

func TestBackupFailsWhenPrivateKeyIsMissing(t *testing.T) {
	s, st := newSecurityTestServer(t)
	missing := filepath.Join(t.TempDir(), "missing-key")
	if _, err := st.CreateKey(store.Key{
		Name: "missing-key", Type: "ed25519", PrivatePath: missing,
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(s.backupsDir(), "incomplete.enc")
	if _, err := s.saveBackup("backup-password", filepath.Base(path)); err == nil {
		t.Fatal("saveBackup succeeded without the private key file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("incomplete backup was published: stat error = %v", err)
	}
	entries, err := os.ReadDir(s.backupsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("backup directory contains partial files: %v", entries)
	}
}

func TestRestoreAtomicallyReplacesKeyDirectory(t *testing.T) {
	s, _ := newSecurityTestServer(t)
	if err := os.MkdirAll(s.keysDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(s.keysDir(), "old-key")
	if err := os.WriteFile(oldPath, []byte("old-private"), 0o600); err != nil {
		t.Fatal(err)
	}

	snap := store.Snapshot{
		Version: 1,
		Keys: []store.KeyExport{{
			Key: store.Key{
				ID: 7, Name: "new-key", Type: "ed25519", PublicKey: "ssh-ed25519 test-public-key",
			},
			PrivateKeyData: []byte("new-private"),
		}},
		Settings: map[string]string{},
	}
	encrypted := encryptSnapshot(t, "restore-password", snap)
	if _, code, err := s.restoreEncrypted("restore-password", encrypted); err != nil || code != http.StatusOK {
		t.Fatalf("restoreEncrypted = code %d, err %v", code, err)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("retired key survived restore: stat error = %v", err)
	}
	newPath := filepath.Join(s.keysDir(), "new-key")
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-private" {
		t.Fatalf("private key = %q, want %q", data, "new-private")
	}
	info, err := os.Stat(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("private key mode = %o, want 600", got)
	}
	keys, err := s.st.ListKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].PrivatePath != newPath {
		t.Fatalf("restored keys = %#v, want one key at %s", keys, newPath)
	}
}

func TestRestoreRollsBackKeyDirectoryWhenDatabaseImportFails(t *testing.T) {
	s, st := newSecurityTestServer(t)
	if _, err := st.CreateGroup(store.Group{ID: 1, Name: "original"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.keysDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(s.keysDir(), "old-key")
	if err := os.WriteFile(oldPath, []byte("old-private"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Duplicate key IDs make ImportAll fail inside its transaction, after the
	// staged key directory has already been installed.
	snap := store.Snapshot{
		Version: 1,
		Keys: []store.KeyExport{
			{Key: store.Key{ID: 9, Name: "new-a", Type: "ed25519"}, PrivateKeyData: []byte("new-a")},
			{Key: store.Key{ID: 9, Name: "new-b", Type: "ed25519"}, PrivateKeyData: []byte("new-b")},
		},
		Settings: map[string]string{},
	}
	encrypted := encryptSnapshot(t, "restore-password", snap)
	if _, code, err := s.restoreEncrypted("restore-password", encrypted); err == nil || code != http.StatusInternalServerError {
		t.Fatalf("restoreEncrypted = code %d, err %v; want database failure", code, err)
	}

	data, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("previous key directory was not restored: %v", err)
	}
	if string(data) != "old-private" {
		t.Fatalf("previous private key = %q, want %q", data, "old-private")
	}
	for _, name := range []string{"new-a", "new-b"} {
		if _, err := os.Stat(filepath.Join(s.keysDir(), name)); !os.IsNotExist(err) {
			t.Fatalf("rejected key %q survived rollback: stat error = %v", name, err)
		}
	}
	groups, err := st.ListGroups()
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Name != "original" {
		t.Fatalf("database was not rolled back: %#v", groups)
	}
}

func encryptSnapshot(t *testing.T, password string, snap store.Snapshot) []byte {
	t.Helper()
	plain, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := backup.Encrypt(password, plain)
	if err != nil {
		t.Fatal(err)
	}
	return encrypted
}
