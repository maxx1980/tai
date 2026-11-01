package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"webssh/internal/keys"
	"webssh/internal/store"
)

// Key detection answers "which of my keys does this host accept?" the way one
// would by hand — `ssh -v host true` and the "Server accepts key" line — and
// records the answer: the key becomes the host's identity_file (so the
// exported ssh_config pins it) and is linked to the host in Keys.

var (
	acceptsKeyRe = regexp.MustCompile(`Server accepts key: (\S+) \S+ (SHA256:[A-Za-z0-9+/=]+)`)
	authPubkeyRe = regexp.MustCompile(`Authenticated to .* using "publickey"`)
)

// detectTimeout bounds one probe: connect, offer keys, run `true`.
const detectTimeout = 30 * time.Second

// acceptedKey parses `ssh -v` output: the path (or agent comment) and SHA256
// fingerprint of the key the server accepted, if it logged in with a key.
func acceptedKey(verbose string) (path, fingerprint string, ok bool) {
	if !authPubkeyRe.MatchString(verbose) {
		return "", "", false
	}
	m := acceptsKeyRe.FindAllStringSubmatch(verbose, -1)
	if len(m) == 0 {
		return "", "", false
	}
	last := m[len(m)-1] // with several, the last accepted one authenticated
	return last[1], last[2], true
}

// fingerprintOf returns the SHA256 fingerprint of an authorized_keys line.
func fingerprintOf(pub string) string {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(pk)
}

// findKeyFile locates the private key with fingerprint fp: the path ssh
// reported when it is a readable key, else a "*.pub" twin in ~/.ssh or the
// webssh keys directory. It returns the private path and its public line.
func (s *Server) findKeyFile(reported, fp string) (priv, pub string) {
	try := func(p string) bool {
		data, err := os.ReadFile(p + ".pub")
		if err != nil || fingerprintOf(string(data)) != fp {
			return false
		}
		if _, err := os.Stat(p); err != nil {
			return false
		}
		priv, pub = p, strings.TrimSpace(string(data))
		return true
	}
	if filepath.IsAbs(reported) && try(reported) {
		return
	}
	for _, dir := range []string{s.sshDir(), s.keysDir()} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".pub") && try(filepath.Join(dir, strings.TrimSuffix(e.Name(), ".pub"))) {
				return
			}
		}
	}
	return "", ""
}

// tildePath writes a path under the home directory as "~/…", the form ssh
// config and other accounts restoring a backup both understand.
func (s *Server) tildePath(p string) string {
	if rest, ok := strings.CutPrefix(p, s.paths.Home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return p
}

// ensureTrackedKey returns the webssh key with public line pub, importing the
// private key at priv under a free name derived from its file name if webssh
// does not track it yet.
func (s *Server) ensureTrackedKey(priv, pub string) (store.Key, bool, error) {
	want, ok := keys.PubKeyID(pub)
	if !ok {
		return store.Key{}, false, fmt.Errorf("unreadable public key")
	}
	list, err := s.st.ListKeys()
	if err != nil {
		return store.Key{}, false, err
	}
	for _, k := range list {
		if id, ok := keys.PubKeyID(k.PublicKey); ok && id == want {
			return k, false, nil
		}
	}
	data, err := os.ReadFile(priv)
	if err != nil {
		return store.Key{}, false, err
	}
	base := filepath.Base(priv)
	name := base
	for i := 2; ; i++ {
		_, taken, _ := s.st.GetKeyByName(name)
		_, statErr := os.Stat(filepath.Join(s.keysDir(), name))
		if !taken && statErr != nil {
			break
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	k, err := keys.Import(s.keysDir(), keys.ImportParams{Name: name, PrivateData: data, PublicData: []byte(pub + "\n")})
	if err != nil {
		return store.Key{}, false, err
	}
	k, err = s.st.CreateKey(k)
	return k, true, err
}

// handleDetectKey runs `ssh -v -o BatchMode=yes <host> true`, finds the key
// the server accepted, and records it as the host's identity_file and as a
// deployment in Keys.
func (s *Server) handleDetectKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	h, err := s.st.GetHost(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if h.Port == 0 {
		writeErr(w, 400, fmt.Errorf("%s has no SSH port", h.Alias))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), detectTimeout)
	defer cancel()
	// BatchMode: never prompt (no password, no new host key) — only keys count.
	args := append([]string{"-v", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}, s.sshArgs(h)...)
	args = append(args, "true")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()

	path, fp, ok := acceptedKey(out.String())
	if !ok {
		v := out.String()
		reason := "the host accepted none of your keys"
		switch {
		case ctx.Err() != nil:
			reason = "ssh timed out"
		case strings.Contains(v, "Host key verification failed"):
			reason = "the host key is not in ~/.ssh/known_hosts yet — connect once to trust it"
		case strings.Contains(v, "Connection refused"), strings.Contains(v, "Could not resolve"),
			strings.Contains(v, "Connection timed out"), strings.Contains(v, "No route to host"):
			reason = "could not connect"
		case runErr == nil:
			reason = "the host let ssh in without a key"
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": reason})
		return
	}

	priv, pub := s.findKeyFile(path, fp)
	if priv == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":       fmt.Sprintf("the host accepts %s (%s), but no key file with that fingerprint was found in ~/.ssh or webssh's keys — it may live only in ssh-agent", fp, path),
			"fingerprint": fp,
		})
		return
	}
	k, imported, err := s.ensureTrackedKey(priv, pub)
	if err != nil {
		writeErr(w, 500, fmt.Errorf("track key: %w", err))
		return
	}
	identity := s.tildePath(priv)
	if err := s.st.SetHostIdentityFile(h.ID, identity); err != nil {
		writeErr(w, 500, err)
		return
	}
	if err := s.st.MarkKeyDeployed(k.ID, h.ID); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.autoExport()
	writeJSON(w, 200, map[string]any{
		"key":           k.Name,
		"identity_file": identity,
		"fingerprint":   fp,
		"imported":      imported,
	})
}
