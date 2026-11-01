package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"webssh/internal/store"
)

// Real `ssh -v` lines from logging in to arscv-9bras-ipoe (2026-11-01).
const verboseOK = `debug1: Offering public key: /home/maxx/.ssh/id_ed25519 ED25519 SHA256:MMa3YFwPFUrz+R2g2wXo9ujKVlOpeL2sHWj4VCEhQTk agent
debug1: Server accepts key: /home/maxx/.ssh/id_ed25519 ED25519 SHA256:MMa3YFwPFUrz+R2g2wXo9ujKVlOpeL2sHWj4VCEhQTk agent
Authenticated to 93.170.26.5 ([93.170.26.5]:22) using "publickey".`

func TestAcceptedKey(t *testing.T) {
	path, fp, ok := acceptedKey(verboseOK)
	if !ok || path != "/home/maxx/.ssh/id_ed25519" || fp != "SHA256:MMa3YFwPFUrz+R2g2wXo9ujKVlOpeL2sHWj4VCEhQTk" {
		t.Errorf("acceptedKey = %q, %q, %v", path, fp, ok)
	}
	// Logged in by password: an accepted-key line alone does not count.
	if _, _, ok := acceptedKey(strings.Replace(verboseOK, `"publickey"`, `"password"`, 1)); ok {
		t.Error("password login reported as a key")
	}
	if _, _, ok := acceptedKey("debug1: Authentications that can continue: publickey,password"); ok {
		t.Error("failed login reported as a key")
	}
}

// Clicking the grey key: ssh is asked, the accepted key becomes the host's
// identity_file, is imported into Keys and linked to the host.
func TestDetectKeyAssignsIdentityAndDeployment(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	s, st := newSecurityTestServer(t)
	if err := os.MkdirAll(s.paths.SSHDir, 0o700); err != nil {
		t.Fatal(err)
	}
	priv := filepath.Join(s.paths.SSHDir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", priv).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	fpOut, err := exec.Command("ssh-keygen", "-lf", priv+".pub").Output()
	if err != nil {
		t.Fatal(err)
	}
	fp := strings.Fields(string(fpOut))[1]

	// A fake ssh on PATH that "logs in" with that key.
	bin := t.TempDir()
	script := "#!/bin/sh\ncat >&2 <<'X'\n" +
		"debug1: Server accepts key: " + priv + " ED25519 " + fp + " agent\n" +
		"Authenticated to 10.0.0.1 ([10.0.0.1]:22) using \"publickey\".\nX\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	h, err := st.CreateHost(store.Host{Alias: "box", Hostname: "10.0.0.1", User: "maxx", Port: 22})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/hosts/1/detect-key", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	s.handleDetectKey(rec, req)
	if rec.Code != 200 {
		t.Fatalf("detect: %d %s", rec.Code, rec.Body)
	}
	var resp struct {
		Key          string `json:"key"`
		IdentityFile string `json:"identity_file"`
		Imported     bool   `json:"imported"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.IdentityFile != "~/.ssh/id_ed25519" || resp.Key != "id_ed25519" || !resp.Imported {
		t.Errorf("response = %+v", resp)
	}

	got, _ := st.GetHost(h.ID)
	if got.IdentityFile != "~/.ssh/id_ed25519" {
		t.Errorf("identity_file = %q", got.IdentityFile)
	}
	list, _ := st.ListKeys()
	if len(list) != 1 || len(list[0].DeployedHosts) != 1 || list[0].DeployedHosts[0] != h.ID {
		t.Errorf("keys = %+v, want id_ed25519 deployed to host %d", list, h.ID)
	}
	if data, _ := os.ReadFile(s.paths.ManagedConfig); !strings.Contains(string(data), "IdentityFile ~/.ssh/id_ed25519") {
		t.Errorf("exported ssh_config lacks IdentityFile:\n%s", data)
	}

	// A second detection reuses the tracked key instead of importing a copy.
	rec = httptest.NewRecorder()
	s.handleDetectKey(rec, req)
	if list, _ := st.ListKeys(); rec.Code != 200 || len(list) != 1 {
		t.Errorf("second detect: %d, %d keys", rec.Code, len(list))
	}
}
