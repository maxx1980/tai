package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backups kept on this machine live in <DataDir>/backups. The updater writes one
// there before every rebuild; this is what makes that snapshot useful, since a
// backup nobody can list or open is not a rollback.
func (s *Server) backupsDir() string { return filepath.Join(s.paths.DataDir, "backups") }

// backupFile is one file in the backups directory, as the SPA sees it.
type backupFile struct {
	Name   string    `json:"name"`
	Size   int64     `json:"size"`
	Made   time.Time `json:"made"`
	Legacy bool      `json:"legacy"` // a plain .db copy from before backups were encrypted
}

// backupPath resolves a name the SPA sent into a path inside the backups
// directory. Anything with a path separator in it, or with an extension webssh
// does not write, is refused outright rather than sanitised into something else.
func (s *Server) backupPath(name string) (string, error) {
	bad := errors.New("no backup by that name")
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return "", bad
	}
	switch filepath.Ext(name) {
	case ".enc", ".db":
	default:
		return "", bad
	}
	return filepath.Join(s.backupsDir(), name), nil
}

// saveBackup seals the store with password and writes it into the backups
// directory under name. Every backup webssh makes lands here — the one the
// Backup button asks for and the one the updater takes before a rebuild — so
// there is a single place to look when something has to be rolled back.
func (s *Server) saveBackup(password, name string) (string, error) {
	dir := s.backupsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	enc, err := s.exportEncrypted(password)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	// It holds saved host passwords and private keys — encrypted, but the file
	// mode costs nothing and the directory is 0700 already.
	if err := os.WriteFile(path, enc, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// handleListBackups lists the backups on this machine, newest first.
func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.backupsDir())
	if err != nil && !os.IsNotExist(err) {
		writeErr(w, 500, err)
		return
	}
	// An empty slice, not nil: the SPA maps over this without checking.
	out := []backupFile{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext != ".enc" && ext != ".db" {
			continue
		}
		fi, ierr := e.Info()
		if ierr != nil {
			continue
		}
		out = append(out, backupFile{
			Name:   e.Name(),
			Size:   fi.Size(),
			Made:   fi.ModTime(),
			Legacy: ext == ".db",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Made.After(out[j].Made) })
	writeJSON(w, 200, out)
}

// handleRestoreBackup rolls back to a backup stored on this machine — the same
// operation as uploading the file in Settings, without the round trip through
// the browser.
func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	path, err := s.backupPath(body.Name)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if filepath.Ext(body.Name) == ".db" {
		// A plain database copy is a whole SQLite file, not a snapshot this
		// process can import — and it cannot be swapped in underneath a running
		// daemon either. Say what to do instead of failing obscurely.
		writeErr(w, 400, fmt.Errorf(
			"%s is a plain database copy from an older version: stop webssh and move it over %s by hand",
			body.Name, s.paths.DBPath))
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, 404, errors.New("no backup by that name"))
		return
	}
	snap, code, err := s.restoreEncrypted(body.Password, data)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	writeJSON(w, 200, map[string]int{
		"hosts": len(snap.Hosts), "keys": len(snap.Keys), "groups": len(snap.Groups),
	})
}

// handleDownloadBackup sends a stored backup to the browser, so the one copy
// does not have to stay on the same disk as the thing it protects.
func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	path, err := s.backupPath(r.PathValue("name"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, 404, errors.New("no backup by that name"))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(path)+"\"")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

// handleDeleteBackup removes one stored backup. Nothing prunes them on its own:
// which snapshot is still worth keeping is the user's call, not the daemon's.
func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	path, err := s.backupPath(r.PathValue("name"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if err := os.Remove(path); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
