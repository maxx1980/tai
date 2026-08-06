package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"webssh/internal/backup"
	"webssh/internal/config"
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
		Tag      string `json:"tag"`
		Backup   bool   `json:"backup"`
		Password string `json:"password"`
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

	var snapshot func(string) (string, error)
	if body.Backup {
		if code, err := s.checkBackupPassword(body.Password); err != nil {
			writeErr(w, code, err)
			return
		}
		pw := body.Password
		snapshot = func(tag string) (string, error) { return s.snapshotBackup(pw, tag) }
	}
	if err := s.updates.Start(info.Latest, snapshot); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, s.updates.Status())
}

// checkBackupPassword validates the password the update dialog collected, and
// reports the status to answer with if it does not hold up. With a master
// password set it has to be that one: a snapshot sealed with anything else is a
// rollback nobody will be able to open. Without one, any non-empty password
// does — the file is only readable with it either way.
func (s *Server) checkBackupPassword(pw string) (int, error) {
	stored := s.st.GetSetting(config.KeyMasterPassword, "")
	if stored == "" {
		if pw == "" {
			return 400, errors.New("a password is needed to encrypt the backup")
		}
		return 200, nil
	}
	if !backup.VerifyPassword(stored, pw) {
		return 401, errors.New("wrong master password")
	}
	return 200, nil
}

// snapshotBackup writes an encrypted backup into <DataDir>/backups and returns
// its path. This is the pre-update rollback point: an update rebuilds the binary
// and never touches the data, but a new version may migrate the database, and a
// migration is the one step that cannot be undone by checking the old tag back
// out. The file is the same format the Settings panel produces, so it restores
// through the same code — from the list of backups, or from disk on another
// machine.
func (s *Server) snapshotBackup(password, tag string) (string, error) {
	// The tag being installed goes in the name: which version this was taken
	// ahead of is the first thing you want to know when rolling back.
	name := fmt.Sprintf("webssh-%s-before-%s.enc", time.Now().Format("20060102-150405"), safeName(tag))
	return s.saveBackup(password, name)
}

// safeName keeps a git tag usable as part of a file name. Tags are tame in
// practice, but the name is built from something GitHub controls.
func safeName(tag string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		}
		return '-'
	}, tag)
	if clean == "" {
		return "update"
	}
	return clean
}

// currentVersion is what the SPA shows as "you are running". Resolved once:
// the answer cannot change while the process lives, and working it out may mean
// shelling out to git, which has no business on the /api/state hot path.
var currentVersion = sync.OnceValue(update.Current)
