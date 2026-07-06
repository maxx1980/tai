package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"webssh/internal/backup"
	"webssh/internal/config"
	"webssh/internal/store"
)

// keysDir is where webssh stores the private/public key files it manages.
func (s *Server) keysDir() string { return filepath.Join(s.paths.DataDir, "keys") }

// handleLockState tells the SPA whether a master password is set and whether the
// current session is unlocked (so it knows whether to show the unlock screen).
func (s *Server) handleLockState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{
		"has_password": s.masterPasswordSet(),
		"unlocked":     s.isUnlocked(r),
	})
}

// handleUnlock verifies the master password and, on success, mints an unlock
// session cookie.
func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	stored := s.st.GetSetting(config.KeyMasterPassword, "")
	if stored == "" {
		writeJSON(w, 200, map[string]bool{"unlocked": true}) // nothing to unlock
		return
	}
	if !backup.VerifyPassword(stored, body.Password) {
		writeErr(w, 401, errors.New("wrong password"))
		return
	}
	http.SetCookie(w, s.newSession())
	writeJSON(w, 200, map[string]bool{"unlocked": true})
}

// handleLock forgets the current unlock session and clears its cookie.
func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	s.dropSession(r)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		SameSite: http.SameSiteStrictMode, HttpOnly: true,
	})
	writeJSON(w, 200, map[string]bool{"unlocked": false})
}

// handleSetPassword sets, changes, or removes the master password.
//   - if a password is already set, the correct "current" is required
//   - an empty "new" removes protection
//   - setting a non-empty password also mints a session so the current tab is
//     not immediately locked out
func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	stored := s.st.GetSetting(config.KeyMasterPassword, "")
	if stored != "" && !backup.VerifyPassword(stored, body.Current) {
		writeErr(w, 403, errors.New("current password is wrong"))
		return
	}

	if body.New == "" {
		if err := s.st.SetSetting(config.KeyMasterPassword, ""); err != nil {
			writeErr(w, 500, err)
			return
		}
		s.clearSessions()
		writeJSON(w, 200, map[string]bool{"has_password": false})
		return
	}

	hash, err := backup.HashPassword(body.New)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if err := s.st.SetSetting(config.KeyMasterPassword, hash); err != nil {
		writeErr(w, 500, err)
		return
	}
	http.SetCookie(w, s.newSession())
	writeJSON(w, 200, map[string]bool{"has_password": true})
}

// handleBackup verifies the master password and streams an encrypted dump of the
// entire store (inventory, saved passwords, keys with private files, settings).
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	stored := s.st.GetSetting(config.KeyMasterPassword, "")
	if stored == "" {
		writeErr(w, 400, errors.New("set a master password first"))
		return
	}
	if !backup.VerifyPassword(stored, body.Password) {
		writeErr(w, 401, errors.New("wrong password"))
		return
	}

	snap, err := s.st.ExportAll()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// Read each key's private-key file into the snapshot so backups are portable.
	for i := range snap.Keys {
		if snap.Keys[i].PrivatePath == "" {
			continue
		}
		if data, rerr := os.ReadFile(snap.Keys[i].PrivatePath); rerr == nil {
			snap.Keys[i].PrivateKeyData = data
		}
	}
	plain, err := json.Marshal(snap)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	enc, err := backup.Encrypt(body.Password, plain)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	name := fmt.Sprintf("webssh-backup-%s.enc", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.WriteHeader(200)
	_, _ = w.Write(enc)
}

// handleRestore decrypts an uploaded backup (sent as JSON text, not multipart)
// and replaces the entire store with its contents.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data     string `json:"data"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	plain, err := backup.Decrypt(body.Password, []byte(body.Data))
	if err != nil {
		writeErr(w, 400, err) // ErrWrongPassword => "wrong password or corrupt backup"
		return
	}
	var snap store.Snapshot
	if err := json.Unmarshal(plain, &snap); err != nil {
		writeErr(w, 400, errors.New("backup is not valid"))
		return
	}

	// Write key files back to disk and point each record at the new location.
	dir := s.keysDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, 500, err)
		return
	}
	for i := range snap.Keys {
		k := &snap.Keys[i]
		if k.Name == "" || strings.ContainsAny(k.Name, "/\\") {
			continue
		}
		priv := filepath.Join(dir, k.Name)
		// Remove any existing files first: a leftover key may be read-only (0400),
		// which would make WriteFile's truncate fail with "permission denied".
		_ = os.Remove(priv)
		_ = os.Remove(priv + ".pub")
		if len(k.PrivateKeyData) > 0 {
			if err := os.WriteFile(priv, k.PrivateKeyData, 0o600); err != nil {
				writeErr(w, 500, fmt.Errorf("write key %s: %w", k.Name, err))
				return
			}
		}
		if k.PublicKey != "" {
			_ = os.WriteFile(priv+".pub", []byte(strings.TrimSpace(k.PublicKey)+"\n"), 0o644)
		}
		k.PrivatePath = priv
	}

	if err := s.st.ImportAll(&snap); err != nil {
		writeErr(w, 500, err)
		return
	}
	// The restore may have changed (or introduced) the master password, so drop
	// all unlock sessions: the SPA will re-lock and prompt with the restored one.
	s.clearSessions()
	s.autoExport()
	writeJSON(w, 200, map[string]int{
		"hosts": len(snap.Hosts), "keys": len(snap.Keys), "groups": len(snap.Groups),
	})
}

// handleReset verifies the master password, then wipes the store and the key
// files on disk, returning webssh to a fresh state.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	stored := s.st.GetSetting(config.KeyMasterPassword, "")
	if stored == "" {
		writeErr(w, 400, errors.New("set a master password first"))
		return
	}
	if !backup.VerifyPassword(stored, body.Password) {
		writeErr(w, 401, errors.New("wrong password"))
		return
	}
	if err := s.st.ResetAll(); err != nil {
		writeErr(w, 500, err)
		return
	}
	// Delete the managed key files (created/imported by webssh).
	if entries, derr := os.ReadDir(s.keysDir()); derr == nil {
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(s.keysDir(), e.Name()))
		}
	}
	s.clearSessions()
	s.autoExport() // rewrite the (now empty) managed ssh_config
	writeJSON(w, 200, map[string]bool{"ok": true})
}
