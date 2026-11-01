package server

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"webssh/internal/backup"
	"webssh/internal/config"
	"webssh/internal/store"
)

const oldHome = "/home/olduser"

// A database copied from another account must stop pointing into that
// account's home: key paths, the default mount dir and identity files follow
// the current $HOME.
func TestRelocateHomeRewritesForeignPaths(t *testing.T) {
	s, st := newSecurityTestServer(t)
	home := s.paths.Home

	k, err := st.CreateKey(store.Key{Name: "id_old", Type: "ed25519", PrivatePath: oldHome + "/.local/share/webssh/keys/id_old"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := st.CreateHost(store.Host{Alias: "box", Hostname: "10.0.0.1", Port: 22, IdentityFile: oldHome + "/.ssh/id_rsa"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateHost(store.Host{Alias: "other", Hostname: "10.0.0.2", Port: 22, IdentityFile: "~/.ssh/id_x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(config.KeyMountBaseDir, oldHome+"/mnt/webssh"); err != nil {
		t.Fatal(err)
	}

	if !s.relocateHome(nil) {
		t.Fatal("relocateHome reported no change")
	}

	gotKey, err := st.GetKey(k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(s.keysDir(), "id_old"); gotKey.PrivatePath != want {
		t.Errorf("key path = %q, want %q", gotKey.PrivatePath, want)
	}
	gotHost, _ := st.GetHost(h.ID)
	if want := filepath.Join(home, ".ssh", "id_rsa"); gotHost.IdentityFile != want {
		t.Errorf("identity_file = %q, want %q", gotHost.IdentityFile, want)
	}
	gotOther, _ := st.GetHost(other.ID)
	if gotOther.IdentityFile != "~/.ssh/id_x" {
		t.Errorf("unrelated identity_file changed to %q", gotOther.IdentityFile)
	}
	if v := st.GetSetting(config.KeyMountBaseDir, "<unset>"); v != "<unset>" {
		t.Errorf("mount_base_dir still stored as %q, want it reset to the default", v)
	}
	if got := s.settingsWithDefaults()[config.KeyMountBaseDir]; got != filepath.Join(home, "mnt", "webssh") {
		t.Errorf("effective mount_base_dir = %q", got)
	}

	// A second pass has nothing left to do.
	if s.relocateHome(nil) {
		t.Error("second relocateHome pass changed something")
	}
}

// Same-user data must be left exactly as it is.
func TestRelocateHomeLeavesOwnPathsAlone(t *testing.T) {
	s, st := newSecurityTestServer(t)
	if _, err := st.CreateKey(store.Key{Name: "id_me", PrivatePath: filepath.Join(s.keysDir(), "id_me")}); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(s.paths.Home, "custom-mounts")
	if err := st.SetSetting(config.KeyMountBaseDir, custom); err != nil {
		t.Fatal(err)
	}
	if s.relocateHome(nil) {
		t.Error("relocateHome changed same-user data")
	}
	if v := st.GetSetting(config.KeyMountBaseDir, ""); v != custom {
		t.Errorf("mount_base_dir = %q, want %q", v, custom)
	}
}

// Restoring another user's backup relocates settings and identity files, not
// only the key files the restore already rewrote.
func TestRestoreFromOtherUserRelocatesPaths(t *testing.T) {
	s, st := newSecurityTestServer(t)
	plain, err := json.Marshal(store.Snapshot{
		Version: 1,
		Hosts:   []store.Host{{ID: 1, Alias: "box", Hostname: "10.0.0.1", Port: 22, IdentityFile: oldHome + "/.ssh/id_rsa"}},
		Settings: map[string]string{
			config.KeyMountBaseDir: oldHome + "/sshfs",
		},
		Home: oldHome,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := backup.Encrypt("pw", plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, code, err := s.restoreEncrypted("pw", data); err != nil {
		t.Fatalf("restore: %d %v", code, err)
	}
	h, err := st.GetHost(1)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(s.paths.Home, ".ssh", "id_rsa"); h.IdentityFile != want {
		t.Errorf("identity_file = %q, want %q", h.IdentityFile, want)
	}
	if v := st.GetSetting(config.KeyMountBaseDir, ""); v != filepath.Join(s.paths.Home, "sshfs") {
		t.Errorf("mount_base_dir = %q", v)
	}
}

// Saving the settings form with untouched defaults must not freeze them.
func TestUpdateSettingsDoesNotStoreDefaults(t *testing.T) {
	s, st := newSecurityTestServer(t)
	if err := st.SetSetting(config.KeyMountBaseDir, "/somewhere/else"); err != nil {
		t.Fatal(err)
	}
	form := s.settingsWithDefaults()
	form[config.KeyMountBaseDir] = config.Defaults(s.paths.Home)[config.KeyMountBaseDir]
	form[config.KeyTheme] = "dark"
	body, _ := json.Marshal(form)
	rec := httptest.NewRecorder()
	s.handleUpdateSettings(rec, httptest.NewRequest("PUT", "/api/settings", strings.NewReader(string(body))))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	all, _ := st.AllSettings()
	if _, ok := all[config.KeyMountBaseDir]; ok {
		t.Errorf("default mount_base_dir was stored: %q", all[config.KeyMountBaseDir])
	}
	if all[config.KeyTheme] != "dark" || len(all) != 1 {
		t.Errorf("stored settings = %v, want only theme=dark", all)
	}
}
