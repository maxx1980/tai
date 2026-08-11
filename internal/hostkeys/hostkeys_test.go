package hostkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	sshknownhosts "golang.org/x/crypto/ssh/knownhosts"
)

func publicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestCallbackTrustsFirstKeyAndRejectsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "known_hosts")
	v := New(path)
	addr := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 2222}
	first := publicKey(t)
	replacement := publicKey(t)

	if err := v.Callback("router.example:2222", addr, first); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := v.Callback("router.example:2222", addr, first); err != nil {
		t.Fatalf("known key: %v", err)
	}

	err := v.Callback("router.example:2222", addr, replacement)
	if err == nil {
		t.Fatal("replacement key accepted")
	}
	var mismatch *sshknownhosts.KeyError
	if !errors.As(err, &mismatch) || len(mismatch.Want) == 0 {
		t.Fatalf("replacement error = %v, want known-host mismatch", err)
	}
	if !strings.Contains(err.Error(), ssh.FingerprintSHA256(replacement)) ||
		!strings.Contains(err.Error(), path) {
		t.Fatalf("replacement error lacks fingerprint or remediation path: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(string(data)), "\n"); got != 0 {
		t.Fatalf("known_hosts contains %d extra lines: %q", got, data)
	}
	if !strings.HasPrefix(string(data), "[router.example]:2222 ") {
		t.Fatalf("non-default port was not normalized: %q", data)
	}
}

func TestCallbackPreservesExistingLineWithoutFinalNewline(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "known_hosts")
	existing := sshknownhosts.Line([]string{"existing.example"}, publicKey(t))
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	v := New(path)
	addr := &net.TCPAddr{IP: net.ParseIP("192.0.2.11"), Port: 22}
	if err := v.Callback("new.example:22", addr, publicKey(t)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), existing+"\nnew.example ") {
		t.Fatalf("existing line was corrupted: %q", data)
	}
}
