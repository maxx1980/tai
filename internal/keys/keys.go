// Package keys generates SSH keypairs (via ssh-keygen) and deploys public keys
// to remote hosts for passwordless access (append to ~/.ssh/authorized_keys).
package keys

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"webssh/internal/store"
)

// GenParams describes a key to generate.
type GenParams struct {
	Name       string // basename for the key file
	Type       string // ed25519 | rsa | ecdsa
	Passphrase string // empty => no passphrase
	Comment    string
}

// ValidName rejects key names that would resolve outside keysDir. Callers that
// touch the filesystem by name (stat, remove) must run this *before* joining the
// name onto a path, not after.
func ValidName(name string) error {
	if name == "" {
		return fmt.Errorf("key name required")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid key name")
	}
	return nil
}

// RemoveFiles deletes a managed key's private and public files and reports how
// many were removed. It refuses to touch anything outside keysDir, so a
// hand-edited private_path in the database can never make webssh unlink an
// arbitrary file (one in ~/.ssh, say). A missing file is not an error.
func RemoveFiles(keysDir string, k store.Key) (int, error) {
	if k.PrivatePath == "" {
		return 0, fmt.Errorf("key has no file on disk")
	}
	dir, err := filepath.Abs(keysDir)
	if err != nil {
		return 0, err
	}
	priv, err := filepath.Abs(k.PrivatePath)
	if err != nil {
		return 0, err
	}
	if filepath.Dir(priv) != dir {
		return 0, fmt.Errorf("key file %s is outside the managed directory", k.PrivatePath)
	}
	n := 0
	for _, p := range []string{priv, priv + ".pub"} {
		switch err := os.Remove(p); {
		case err == nil:
			n++
		case os.IsNotExist(err):
		default:
			return n, err
		}
	}
	return n, nil
}

// Generate runs ssh-keygen to create a keypair under keysDir and returns a
// populated store.Key (not yet persisted).
func Generate(keysDir string, p GenParams) (store.Key, error) {
	if p.Type == "" {
		p.Type = "ed25519"
	}
	if err := ValidName(p.Name); err != nil {
		return store.Key{}, err
	}
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return store.Key{}, err
	}
	priv := filepath.Join(keysDir, p.Name)
	if _, err := os.Stat(priv); err == nil {
		return store.Key{}, fmt.Errorf("key %q already exists", p.Name)
	}
	comment := p.Comment
	if comment == "" {
		comment = "webssh-" + time.Now().Format("20060102")
	}
	args := []string{"-t", p.Type, "-f", priv, "-N", p.Passphrase, "-C", comment}
	if p.Type == "rsa" {
		args = append(args, "-b", "4096")
	}
	cmd := exec.Command("ssh-keygen", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return store.Key{}, fmt.Errorf("ssh-keygen: %v: %s", err, strings.TrimSpace(string(out)))
	}
	pub, err := os.ReadFile(priv + ".pub")
	if err != nil {
		return store.Key{}, err
	}
	return store.Key{
		Name:          p.Name,
		Type:          p.Type,
		PublicKey:     strings.TrimSpace(string(pub)),
		PrivatePath:   priv,
		HasPassphrase: p.Passphrase != "",
	}, nil
}

// ImportParams describes an existing keypair to import into webssh.
type ImportParams struct {
	Name        string
	PrivateData []byte
	PublicData  []byte // optional; derived from the private key when omitted
}

// Import writes an existing keypair into keysDir and returns a store.Key. It
// derives the public key from the private one when PublicData is empty. Callers
// are responsible for the overwrite decision before calling.
func Import(keysDir string, p ImportParams) (store.Key, error) {
	if err := ValidName(p.Name); err != nil {
		return store.Key{}, err
	}
	if len(p.PrivateData) == 0 {
		return store.Key{}, fmt.Errorf("private key is empty")
	}

	hasPass := false
	pub := p.PublicData

	// Parse the private key to validate it and (when possible) derive the public key.
	signer, err := ssh.ParsePrivateKey(p.PrivateData)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			hasPass = true
			if len(pub) == 0 && missing.PublicKey != nil {
				pub = ssh.MarshalAuthorizedKey(missing.PublicKey)
			}
			if len(pub) == 0 {
				return store.Key{}, fmt.Errorf("encrypted key: also upload the matching .pub file")
			}
		} else {
			return store.Key{}, fmt.Errorf("not a valid OpenSSH private key: %v", err)
		}
	} else if len(pub) == 0 {
		pub = ssh.MarshalAuthorizedKey(signer.PublicKey())
	}

	pk, _, _, _, perr := ssh.ParseAuthorizedKey(pub)
	if perr != nil {
		return store.Key{}, fmt.Errorf("invalid public key: %v", perr)
	}

	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return store.Key{}, err
	}
	priv := filepath.Join(keysDir, p.Name)
	if err := os.WriteFile(priv, p.PrivateData, 0o600); err != nil {
		return store.Key{}, err
	}
	if err := os.WriteFile(priv+".pub", pub, 0o644); err != nil {
		return store.Key{}, err
	}

	return store.Key{
		Name:          p.Name,
		Type:          shortType(pk.Type()),
		PublicKey:     strings.TrimSpace(string(pub)),
		PrivatePath:   priv,
		HasPassphrase: hasPass,
	}, nil
}

func shortType(t string) string {
	switch {
	case t == "ssh-ed25519":
		return "ed25519"
	case t == "ssh-rsa":
		return "rsa"
	case strings.HasPrefix(t, "ecdsa-"):
		return "ecdsa"
	case strings.HasPrefix(t, "sk-"):
		return "sk"
	default:
		return t
	}
}

// PubKeyID returns a canonical identity for an authorized_keys line (the wire
// marshaling of the key, base64-encoded), so the same key can be recognised
// regardless of its comment or filename. ok is false if the line doesn't parse.
func PubKeyID(publicKey string) (string, bool) {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(pk.Marshal()), true
}

// IndexSSHDir scans sshDir for "*.pub" files and returns a map from PubKeyID to
// the corresponding private-key path (the .pub path minus its extension).
func IndexSSHDir(sshDir string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		pubPath := filepath.Join(sshDir, e.Name())
		data, rerr := os.ReadFile(pubPath)
		if rerr != nil {
			continue
		}
		if id, ok := PubKeyID(string(data)); ok {
			out[id] = strings.TrimSuffix(pubPath, ".pub")
		}
	}
	return out
}

// ExportToSSH copies the key's private and public files into sshDir (e.g.
// ~/.ssh), so plain `ssh` can use it. It refuses to clobber an existing file of
// the same name and returns the written private path.
func ExportToSSH(sshDir string, k store.Key) (string, error) {
	if k.Name == "" || strings.ContainsAny(k.Name, "/\\") {
		return "", fmt.Errorf("invalid key name")
	}
	data, err := os.ReadFile(k.PrivatePath)
	if err != nil {
		return "", fmt.Errorf("read private key: %w", err)
	}
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return "", err
	}
	priv := filepath.Join(sshDir, k.Name)
	if _, err := os.Stat(priv); err == nil {
		return "", fmt.Errorf("~/.ssh/%s already exists", k.Name)
	}
	if err := os.WriteFile(priv, data, 0o600); err != nil {
		return "", err
	}
	if k.PublicKey != "" {
		_ = os.WriteFile(priv+".pub", []byte(strings.TrimSpace(k.PublicKey)+"\n"), 0o644)
	}
	return priv, nil
}

// DeployTarget is the connection info needed to install a key on a host.
type DeployTarget struct {
	Hostname        string
	User            string
	Port            int
	HostKeyCallback ssh.HostKeyCallback
}

// Deploy connects to the target with password auth and appends publicKey to the
// remote ~/.ssh/authorized_keys (idempotently). Returns nil if already present.
func Deploy(t DeployTarget, password, publicKey string) error {
	if t.Port == 0 {
		t.Port = 22
	}
	if t.User == "" {
		return fmt.Errorf("user required to deploy key")
	}
	if t.HostKeyCallback == nil {
		return fmt.Errorf("SSH host-key verifier is not configured")
	}
	cfg := &ssh.ClientConfig{
		User:            t.User,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: t.HostKeyCallback,
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(t.Hostname, fmt.Sprintf("%d", t.Port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("connect %s: %w", addr, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	// Read the key from stdin to avoid any shell-quoting issues with the comment.
	session.Stdin = strings.NewReader(strings.TrimSpace(publicKey) + "\n")
	const script = `umask 077; mkdir -p ~/.ssh; key=$(cat); ` +
		`touch ~/.ssh/authorized_keys; ` +
		`grep -qxF "$key" ~/.ssh/authorized_keys 2>/dev/null || printf '%s\n' "$key" >> ~/.ssh/authorized_keys; ` +
		`chmod 600 ~/.ssh/authorized_keys`
	if out, err := session.CombinedOutput("sh -c '" + script + "'"); err != nil {
		return fmt.Errorf("deploy: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
