package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"webssh/internal/store"
)

func genKey(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	pub, _ := os.ReadFile(path + ".pub")
	return strings.TrimSpace(string(pub))
}

// "mikrotik_rsa: Deployed to 0 host(s)" although a host's IdentityFile is
// that key: the link through identity_file must count too.
func TestLinkIdentityHosts(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	s, _ := newSecurityTestServer(t)
	managed := filepath.Join(s.keysDir(), "mikrotik_rsa")
	pub := genKey(t, managed)
	// The same key copied to ~/.ssh under another name.
	copyPath := filepath.Join(s.paths.SSHDir, "mt")
	data, _ := os.ReadFile(managed)
	_ = os.MkdirAll(s.paths.SSHDir, 0o700)
	_ = os.WriteFile(copyPath, data, 0o600)
	_ = os.WriteFile(copyPath+".pub", []byte(pub+"\n"), 0o644)
	other := filepath.Join(s.paths.SSHDir, "other")
	genKey(t, other)

	list := []store.Key{{ID: 6, Name: "mikrotik_rsa", PublicKey: pub, PrivatePath: managed}}
	hosts := []store.Host{
		{ID: 1, Alias: "by-path", IdentityFile: managed},
		{ID: 2, Alias: "by-copy", IdentityFile: "~/.ssh/mt"},
		{ID: 3, Alias: "other-key", IdentityFile: "~/.ssh/other"},
		{ID: 4, Alias: "none"},
		{ID: 5, Alias: "missing-file", IdentityFile: "~/.ssh/gone"},
	}
	s.linkIdentityHosts(list, hosts)
	got := list[0].IdentityHosts
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("IdentityHosts = %v, want [1 2]", got)
	}
}
