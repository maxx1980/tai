package keys

import (
	"os"
	"path/filepath"
	"testing"

	"webssh/internal/store"
)

func TestValidName(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"id_ed25519", true},
		{"acme.prod", true},
		{"", false},
		{".", false},
		{"..", false},
		{"../escape", false},
		{"sub/key", false},
		{`sub\key`, false},
	} {
		if err := ValidName(tc.name); (err == nil) != tc.ok {
			t.Errorf("ValidName(%q) = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// write creates a file and returns its path.
func write(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRemoveFilesDeletesBothParts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	priv := write(t, dir, "test")
	write(t, dir, "test.pub")

	n, err := RemoveFiles(dir, store.Key{PrivatePath: priv})
	if err != nil {
		t.Fatalf("RemoveFiles: %v", err)
	}
	if n != 2 {
		t.Errorf("removed %d files, want 2", n)
	}
	for _, p := range []string{priv, priv + ".pub"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
}

func TestRemoveFilesToleratesMissingPub(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	priv := write(t, dir, "test")

	n, err := RemoveFiles(dir, store.Key{PrivatePath: priv})
	if err != nil {
		t.Fatalf("RemoveFiles: %v", err)
	}
	if n != 1 {
		t.Errorf("removed %d files, want 1", n)
	}
}

// A private_path pointing outside the managed directory must be refused rather
// than obeyed — otherwise a doctored database row could unlink ~/.ssh keys.
func TestRemoveFilesRefusesOutsideKeysDir(t *testing.T) {
	root := t.TempDir()
	keysDir := filepath.Join(root, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := write(t, filepath.Join(root, "ssh"), "id_ed25519")

	for _, path := range []string{
		outside,
		filepath.Join(keysDir, "..", "ssh", "id_ed25519"), // traversal back out
	} {
		if _, err := RemoveFiles(keysDir, store.Key{PrivatePath: path}); err == nil {
			t.Errorf("RemoveFiles(%q) succeeded, want refusal", path)
		}
		if _, err := os.Stat(outside); err != nil {
			t.Fatalf("outside key was deleted: %v", err)
		}
	}
}

func TestRemoveFilesEmptyPath(t *testing.T) {
	if _, err := RemoveFiles(t.TempDir(), store.Key{}); err == nil {
		t.Error("empty PrivatePath accepted, want error")
	}
}

func TestDeployRequiresHostKeyVerifier(t *testing.T) {
	err := Deploy(DeployTarget{Hostname: "127.0.0.1", User: "root", Port: 22}, "password", "key")
	if err == nil || err.Error() != "SSH host-key verifier is not configured" {
		t.Fatalf("Deploy error = %v, want missing verifier", err)
	}
}
