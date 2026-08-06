// Package server exposes the webssh HTTP API and serves the embedded SPA.
//
// Security model (the daemon runs with the user's privileges):
//   - bound to loopback only (enforced in main)
//   - every /api request must present the startup token via the X-Auth-Token
//     header (websocket uses a ?token= query param, as browsers can't set WS headers)
//   - mutating requests must additionally carry a loopback Origin (CSRF defense)
package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"webssh/internal/config"
	"webssh/internal/health"
	"webssh/internal/sftpbrowse"
	"webssh/internal/store"
	"webssh/internal/update"
)

// Server holds shared dependencies for the HTTP handlers.
type Server struct {
	st     *store.Store
	paths  config.Paths
	token  string
	assets fs.FS
	sftp   *sftpbrowse.Manager
	health *health.Checker
	mux    *http.ServeMux

	// updates compares this build with the newest tag on GitHub and, on
	// request, rebuilds the working copy in place.
	updates *update.Updater

	authDisabled atomic.Bool // when true, the API-key check is skipped (user opt-in)

	mu       sync.Mutex          // guards sessions
	sessions map[string]struct{} // valid unlock-session cookie values

	// Presence: open /api/presence websockets, one per browser tab. When the
	// last one goes away the daemon exits (see exit_on_close).
	clients  atomic.Int64
	graceMu  sync.Mutex
	graceT   *time.Timer
	quit     chan struct{}
	quitOnce sync.Once
}

// exitGrace is how long the daemon waits after the last tab disconnects before
// giving up. It must comfortably outlast a page reload, which drops the socket
// and reopens it a moment later.
const exitGrace = 8 * time.Second

// sessionCookie is the name of the cookie proving the master-password gate has
// been unlocked (separate from the loopback startup token).
const sessionCookie = "webssh_session"

// New builds a Server. assets is the embedded SPA filesystem (rooted at dist).
func New(st *store.Store, paths config.Paths, token string, assets fs.FS, hc *health.Checker) *Server {
	s := &Server{
		st:       st,
		paths:    paths,
		token:    token,
		assets:   assets,
		sftp:     sftpbrowse.NewManager(paths.Home, filepath.Join(paths.DataDir, "keys")),
		health:   hc,
		updates:  update.New(),
		mux:      http.NewServeMux(),
		sessions: map[string]struct{}{},
		quit:     make(chan struct{}),
	}
	s.authDisabled.Store(st.GetSetting(config.KeyAuthDisabled, "") == "1")
	s.routes()
	return s
}

// masterPasswordSet reports whether a master password has been configured.
func (s *Server) masterPasswordSet() bool {
	return s.st.GetSetting(config.KeyMasterPassword, "") != ""
}

// isUnlocked reports whether the request may access protected endpoints: true
// when no master password is set, or when a valid session cookie is present.
func (s *Server) isUnlocked(r *http.Request) bool {
	if !s.masterPasswordSet() {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[c.Value]
	return ok
}

// newSession mints a random session id, records it, and returns a cookie for it.
func (s *Server) newSession() *http.Cookie {
	buf := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, buf)
	id := hex.EncodeToString(buf)
	s.mu.Lock()
	s.sessions[id] = struct{}{}
	s.mu.Unlock()
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		SameSite: http.SameSiteStrictMode,
		HttpOnly: true,
	}
}

// dropSession forgets the session named by the request's cookie (if any).
func (s *Server) dropSession(r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
}

// clearSessions forgets every unlock session (used after a reset).
func (s *Server) clearSessions() {
	s.mu.Lock()
	s.sessions = map[string]struct{}{}
	s.mu.Unlock()
}

// Handler returns the root http.Handler, wrapped so a panic in any handler is
// recovered (logged with a stack) instead of taking down the whole daemon.
func (s *Server) Handler() http.Handler { return recoverMW(s.mux) }

func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				// Headers may already be sent; ignore a secondary panic here.
				defer func() { _ = recover() }()
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	// Static SPA + token bootstrap.
	s.mux.HandleFunc("/", s.handleIndex)

	// Lock gate: these are token-guarded but reachable while locked, so the SPA
	// can discover the lock state and unlock.
	gate := func(pattern string, h http.HandlerFunc) {
		s.mux.Handle(pattern, s.requireAuth(h))
	}
	gate("GET /api/lockstate", s.handleLockState)
	gate("POST /api/unlock", s.handleUnlock)

	// API — guarded by requireAuth (+ loopback Origin on mutations) AND, when a
	// master password is set, by a valid unlock session (423 otherwise).
	api := func(pattern string, h http.HandlerFunc) {
		s.mux.Handle(pattern, s.requireAuth(s.requireUnlocked(h)))
	}
	api("GET /api/state", s.handleState)
	api("GET /api/health", s.handleHealth)

	api("POST /api/lock", s.handleLock)
	api("POST /api/restart", s.handleRestart)

	// Self-update: compare with GitHub, then rebuild the working copy in place.
	api("GET /api/update/check", s.handleUpdateCheck)
	api("GET /api/update/status", s.handleUpdateStatus)
	api("POST /api/update/run", s.handleUpdateRun)

	api("PUT /api/settings/password", s.handleSetPassword)
	api("POST /api/backup", s.handleBackup)
	api("POST /api/restore", s.handleRestore)
	api("POST /api/reset", s.handleReset)

	// Backups kept on this machine — the updater leaves one here before every
	// rebuild, and this is how it gets restored again.
	api("GET /api/backups", s.handleListBackups)
	api("POST /api/backups/restore", s.handleRestoreBackup)
	api("GET /api/backups/{name}", s.handleDownloadBackup)
	api("DELETE /api/backups/{name}", s.handleDeleteBackup)

	api("POST /api/hosts", s.handleCreateHost)
	api("POST /api/hosts/bulk", s.handleBulkCreateHosts)
	api("PUT /api/hosts/{id}", s.handleUpdateHost)
	api("DELETE /api/hosts/{id}", s.handleDeleteHost)

	// Network discovery: find machines with SSH/telnet open, then add them.
	api("POST /api/scan", s.handleScan)

	api("POST /api/groups", s.handleCreateGroup)
	api("PUT /api/groups/{id}", s.handleUpdateGroup)
	api("DELETE /api/groups/{id}", s.handleDeleteGroup)

	api("POST /api/import/ssh-config", s.handleImport)
	api("POST /api/import/upload", s.handleImportUpload)
	api("POST /api/export/ssh-config", s.handleExport)

	api("POST /api/keys/generate", s.handleGenerateKey)
	api("POST /api/keys/import", s.handleImportKey)
	api("GET /api/keys/{id}/export", s.handleExportKey)
	api("POST /api/keys/{id}/ssh-export", s.handleSSHExport)
	api("POST /api/keys/{id}/ssh-remove", s.handleSSHRemove)
	api("DELETE /api/keys/{id}", s.handleDeleteKey)
	api("POST /api/keys/{id}/deploy", s.handleDeployKey)

	api("POST /api/known-hosts/import", s.handleImportKnownHosts)
	api("POST /api/known-hosts/add", s.handleAddKnownHosts)
	api("POST /api/known-hosts/scan", s.handleScanKnownHost)
	api("POST /api/known-hosts/export-all", s.handleExportAllKnownHosts)
	api("POST /api/known-hosts/{id}/ssh-export", s.handleSSHExportKnownHost)
	api("POST /api/known-hosts/{id}/ssh-remove", s.handleSSHRemoveKnownHost)
	api("DELETE /api/known-hosts/{id}", s.handleDeleteKnownHost)

	api("GET /api/mounts", s.handleListMounts)
	api("POST /api/mounts/{id}", s.handleMount)
	api("DELETE /api/mounts/{id}", s.handleUnmount)

	api("POST /api/launch/terminal/{id}", s.handleLaunchTerminal)
	api("POST /api/launch/files/{id}", s.handleLaunchFiles)
	api("POST /api/launch/url/{id}", s.handleLaunchURL)

	// Web SFTP browser.
	api("GET /api/sftp/{id}/list", s.handleSftpList)
	api("GET /api/sftp/{id}/download", s.handleSftpDownload)
	api("POST /api/sftp/{id}/upload", s.handleSftpUpload)
	api("POST /api/sftp/{id}/mkdir", s.handleSftpMkdir)
	api("POST /api/sftp/{id}/rename", s.handleSftpRename)
	api("DELETE /api/sftp/{id}/remove", s.handleSftpRemove)
	api("POST /api/sftp/{id}/disconnect", s.handleSftpDisconnect)

	api("PUT /api/settings", s.handleUpdateSettings)

	// Websockets (token via query param — browsers can't set headers on WS).
	s.mux.HandleFunc("/api/terminal/{id}", s.handleTerminal)
	s.mux.HandleFunc("/api/presence", s.handlePresence)
}

// ---- middleware ----

// requireAuth enforces the startup token, and a loopback Origin on mutations.
func (s *Server) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authDisabled.Load() && !s.validToken(r.Header.Get("X-Auth-Token")) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !originIsLocal(r) {
				http.Error(w, "bad origin", http.StatusForbidden)
				return
			}
		}
		next(w, r)
	})
}

// requireUnlocked returns 423 Locked when a master password is set and the
// request lacks a valid unlock session.
func (s *Server) requireUnlocked(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isUnlocked(r) {
			writeJSON(w, http.StatusLocked, map[string]string{"error": "locked"})
			return
		}
		next(w, r)
	}
}

func (s *Server) validToken(got string) bool {
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

// originIsLocal returns true if the request Origin (or Referer) points at a
// loopback host, mitigating cross-site request forgery.
func originIsLocal(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// ---- static / bootstrap ----

// handleIndex serves the SPA. A ?token= query on first navigation is stored in
// a SameSite=Strict cookie so the SPA can read it and send the header thereafter.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if tok := r.URL.Query().Get("token"); tok != "" && s.validToken(tok) {
		http.SetCookie(w, &http.Cookie{
			Name:     "webssh_token",
			Value:    tok,
			Path:     "/",
			SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	// Serve embedded assets; fall back to index.html for SPA routes.
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.assets.Open(path); err == nil {
		f.Close()
		http.FileServerFS(s.assets).ServeHTTP(w, r)
		return
	}
	// SPA fallback.
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/"
	http.ServeFileFS(w, r2, s.assets, "index.html")
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
