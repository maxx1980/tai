// Package sftpbrowse provides a cached pool of SFTP sessions used by the web
// file browser. Sessions authenticate with the SSH agent, default/host key
// files, or a one-shot password from the web, and are reused across requests
// until idle.
package sftpbrowse

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"webssh/internal/store"
)

// ErrNeedPassword signals the caller to collect a password from the web and retry.
var ErrNeedPassword = errors.New("authentication failed — a password or deployed key is required")

const idleTimeout = 5 * time.Minute

// Entry is a directory listing item.
type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	IsLink  bool   `json:"is_link"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime int64  `json:"mod_time"`
}

type session struct {
	ssh      *ssh.Client
	client   *sftp.Client
	lastUsed time.Time
}

// Manager owns the pool of live SFTP sessions.
type Manager struct {
	mu              sync.Mutex
	sessions        map[int64]*session
	home            string              // local user home (for default key discovery)
	hostKeyCallback ssh.HostKeyCallback // mandatory known_hosts/TOFU verifier
	keyDirs         []string            // extra dirs holding webssh-managed private keys
}

// NewManager builds a Manager. keyDirs are scanned for private keys (e.g. the
// app's generated-keys directory) in addition to the agent and ~/.ssh defaults.
func NewManager(home string, hostKeyCallback ssh.HostKeyCallback, keyDirs ...string) *Manager {
	m := &Manager{
		sessions:        map[int64]*session{},
		home:            home,
		hostKeyCallback: hostKeyCallback,
		keyDirs:         keyDirs,
	}
	go m.reaper()
	return m
}

func (m *Manager) reaper() {
	for range time.Tick(time.Minute) {
		m.mu.Lock()
		for id, s := range m.sessions {
			if time.Since(s.lastUsed) > idleTimeout {
				s.client.Close()
				s.ssh.Close()
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
	}
}

// Client returns a live SFTP client for h, dialing (and caching) if needed.
// password may be empty to attempt key/agent auth only.
func (m *Manager) Client(h store.Host, password string) (*sftp.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[h.ID]; ok {
		s.lastUsed = time.Now()
		return s.client, nil
	}
	s, err := m.dial(h, password)
	if err != nil {
		return nil, err
	}
	m.sessions[h.ID] = s
	return s.client, nil
}

// Disconnect closes and forgets the session for a host (if any).
func (m *Manager) Disconnect(hostID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[hostID]; ok {
		s.client.Close()
		s.ssh.Close()
		delete(m.sessions, hostID)
	}
}

func (m *Manager) dial(h store.Host, password string) (*session, error) {
	if h.User == "" {
		return nil, fmt.Errorf("host %q has no user set", h.Alias)
	}
	if m.hostKeyCallback == nil {
		return nil, errors.New("SSH host-key verifier is not configured")
	}
	var auths []ssh.AuthMethod
	usedPassword := password != ""
	if usedPassword {
		auths = append(auths, ssh.Password(password))
	} else if a, ok := m.keyAuth(h); ok {
		auths = append(auths, a)
	}
	if len(auths) == 0 {
		return nil, ErrNeedPassword
	}

	cfg := &ssh.ClientConfig{
		User:            h.User,
		Auth:            auths,
		HostKeyCallback: m.hostKeyCallback,
		Timeout:         15 * time.Second,
	}
	host := h.Hostname
	if host == "" {
		host = h.Alias
	}
	port := h.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	sc, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		// Distinguish auth failures (retryable with a password) from network errors.
		if !usedPassword && isAuthError(err) {
			return nil, ErrNeedPassword
		}
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}
	cl, err := sftp.NewClient(sc)
	if err != nil {
		sc.Close()
		return nil, err
	}
	return &session{ssh: sc, client: cl, lastUsed: time.Now()}, nil
}

// isAuthError reports whether err is an authentication failure that a password
// could resolve — including the server closing the connection after too many
// key attempts (MaxAuthTries). Such cases should prompt the web for a password
// rather than surfacing a raw error.
func isAuthError(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unable to authenticate") ||
		strings.Contains(m, "too many authentication failures") ||
		strings.Contains(m, "permission denied") ||
		strings.Contains(m, "no supported methods remain")
}

// maxKeys caps how many public keys we offer, staying under the common server
// MaxAuthTries (default 6) so a valid key isn't cut off by "too many failures".
const maxKeys = 5

// keyAuth gathers signers in priority order — the host's IdentityFile, the
// app's generated keys, the SSH agent, then ~/.ssh defaults — de-duplicated and
// capped at maxKeys.
func (m *Manager) keyAuth(h store.Host) (ssh.AuthMethod, bool) {
	var signers []ssh.Signer
	seen := map[string]bool{}
	add := func(s ssh.Signer) {
		if len(signers) >= maxKeys {
			return
		}
		fp := string(ssh.MarshalAuthorizedKey(s.PublicKey()))
		if seen[fp] {
			return
		}
		seen[fp] = true
		signers = append(signers, s)
	}

	addFiles := func(paths []string) {
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if s, err := ssh.ParsePrivateKey(data); err == nil { // skips passphrase-protected
				add(s)
			}
		}
	}

	addFiles(m.priorityKeyFiles(h)) // host IdentityFile + app-generated keys
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if c, err := net.Dial("unix", sock); err == nil {
			if s, err := agent.NewClient(c).Signers(); err == nil {
				for _, sig := range s {
					add(sig)
				}
			}
		}
	}
	addFiles(defaultKeyFiles(m.home))

	if len(signers) == 0 {
		return nil, false
	}
	return ssh.PublicKeys(signers...), true
}

// priorityKeyFiles are the most host-specific keys: the host's IdentityFile and
// the app's generated keys.
func (m *Manager) priorityKeyFiles(h store.Host) []string {
	var files []string
	if h.IdentityFile != "" {
		files = append(files, expandHome(h.IdentityFile, m.home))
	}
	for _, dir := range m.keyDirs {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() || strings.HasSuffix(e.Name(), ".pub") {
					continue
				}
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	return files
}

// defaultKeyFiles are the conventional ~/.ssh identities, tried last.
func defaultKeyFiles(home string) []string {
	var files []string
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		files = append(files, filepath.Join(home, ".ssh", name))
	}
	return files
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// ---- operations ----

// Resolve turns an empty/"~" path into the remote home absolute path.
func Resolve(cl *sftp.Client, p string) (string, error) {
	if p == "" || p == "~" {
		return cl.Getwd()
	}
	return p, nil
}

// List returns the directory entries at dir (sorted: dirs first, then by name).
func List(cl *sftp.Client, dir string) (string, []Entry, error) {
	dir, err := Resolve(cl, dir)
	if err != nil {
		return "", nil, err
	}
	infos, err := cl.ReadDir(dir)
	if err != nil {
		return "", nil, err
	}
	entries := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		isLink := fi.Mode()&os.ModeSymlink != 0
		isDir := fi.IsDir()
		full := path.Join(dir, fi.Name())
		if isLink {
			// Resolve symlink target type so directories are navigable.
			if st, err := cl.Stat(full); err == nil {
				isDir = st.IsDir()
			}
		}
		entries = append(entries, Entry{
			Name:    fi.Name(),
			Path:    full,
			IsDir:   isDir,
			IsLink:  isLink,
			Size:    fi.Size(),
			Mode:    fi.Mode().String(),
			ModTime: fi.ModTime().Unix(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return dir, entries, nil
}

// Open opens a remote file for reading.
func Open(cl *sftp.Client, p string) (io.ReadCloser, os.FileInfo, error) {
	f, err := cl.Open(p)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, fi, nil
}

// Upload writes src to remote path dst (overwriting).
func Upload(cl *sftp.Client, dst string, src io.Reader) error {
	f, err := cl.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}

// Mkdir creates a directory (and parents).
func Mkdir(cl *sftp.Client, p string) error { return cl.MkdirAll(p) }

// Rename moves/renames a path.
func Rename(cl *sftp.Client, from, to string) error { return cl.Rename(from, to) }

// Remove deletes a file, or a directory recursively.
func Remove(cl *sftp.Client, p string) error {
	fi, err := cl.Stat(p)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return cl.Remove(p)
	}
	entries, err := cl.ReadDir(p)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := Remove(cl, path.Join(p, e.Name())); err != nil {
			return err
		}
	}
	return cl.RemoveDirectory(p)
}
