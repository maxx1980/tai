package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"webssh/internal/update"
)

// checkTimeout bounds the GitHub round-trip so a hung network cannot hold the
// request (and the SPA's initial load) open.
const checkTimeout = 25 * time.Second

// handleUpdateCheck reports the running version, the newest tag on GitHub and
// whether this install can be updated in place. ?force=1 skips the cache, for
// the panel's "Check again" button.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()
	info, err := s.updates.Check(ctx, r.URL.Query().Get("force") == "1")
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, 200, info)
}

// handleUpdateStatus returns the live state of a running or finished update.
// The SPA polls this: the rebuild runs for minutes, far longer than a request
// should stay open.
func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.updates.Status())
}

// handleUpdateRun starts the update. It re-checks first so a panel left open
// for a while cannot install a tag that is no longer the newest one, or one
// this install was never able to build.
func (s *Server) handleUpdateRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tag    string `json:"tag"`
		Backup bool   `json:"backup"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()
	info, err := s.updates.Check(ctx, true)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if !info.CanUpdate {
		writeErr(w, 400, errors.New(info.Blocker))
		return
	}
	if !info.Available {
		writeErr(w, 400, fmt.Errorf("%s is already the newest version", info.Current))
		return
	}
	if body.Tag != "" && body.Tag != info.Latest {
		writeErr(w, 409, fmt.Errorf("%s is no longer the newest version — %s is", body.Tag, info.Latest))
		return
	}

	var snapshot func() (string, error)
	if body.Backup {
		snapshot = s.snapshotDB
	}
	if err := s.updates.Start(info.Latest, snapshot); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, s.updates.Status())
}

// snapshotDB writes a timestamped copy of the database into <DataDir>/backups
// and returns its path. This is the pre-update backup: an update rebuilds the
// binary and never touches the DB, but a new version may migrate it, and a
// migration is the one step that cannot be undone by checking the old tag back
// out.
func (s *Server) snapshotDB() (string, error) {
	dir := filepath.Join(s.paths.DataDir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("webssh-%s.db", time.Now().Format("20060102-150405")))
	if err := s.st.SnapshotTo(path); err != nil {
		return "", err
	}
	// SQLite creates the copy with the process umask. It holds saved host
	// passwords, so narrow it to the owner even though the directory is 0700.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// currentVersion is what the SPA shows as "you are running". Resolved once:
// the answer cannot change while the process lives, and working it out may mean
// shelling out to git, which has no business on the /api/state hot path.
var currentVersion = sync.OnceValue(update.Current)
