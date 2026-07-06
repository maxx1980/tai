package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"

	"github.com/pkg/sftp"

	"webssh/internal/sftpbrowse"
	"webssh/internal/store"
)

// sftpClient resolves the host and a live SFTP client. The one-shot password (on
// the first connect) travels in the X-SSH-Password header so it stays out of
// URLs and logs. On auth failure it writes a 428 asking the SPA for a password
// and returns ok=false.
func (s *Server) sftpClient(w http.ResponseWriter, r *http.Request) (*sftp.Client, store.Host, bool) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return nil, store.Host{}, false
	}
	h, err := s.st.GetHost(id)
	if err != nil {
		writeErr(w, 404, err)
		return nil, store.Host{}, false
	}
	pw := r.Header.Get("X-SSH-Password")
	if pw == "" {
		pw = s.st.GetHostPassword(h.ID) // fall back to a saved host password
	}
	cl, err := s.sftp.Client(h, pw)
	if err != nil {
		if errors.Is(err, sftpbrowse.ErrNeedPassword) {
			writeJSON(w, http.StatusPreconditionRequired, map[string]any{
				"need_password": true,
				"error":         err.Error(),
			})
			return nil, store.Host{}, false
		}
		writeErr(w, 502, err)
		return nil, store.Host{}, false
	}
	return cl, h, true
}

func (s *Server) handleSftpList(w http.ResponseWriter, r *http.Request) {
	cl, _, ok := s.sftpClient(w, r)
	if !ok {
		return
	}
	dir, entries, err := sftpbrowse.List(cl, r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"path": dir, "entries": entries})
}

func (s *Server) handleSftpDownload(w http.ResponseWriter, r *http.Request) {
	cl, _, ok := s.sftpClient(w, r)
	if !ok {
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		writeErr(w, 400, fmt.Errorf("path required"))
		return
	}
	f, fi, err := sftpbrowse.Open(cl, p)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(p)))
	_, _ = io.Copy(w, f)
}

func (s *Server) handleSftpUpload(w http.ResponseWriter, r *http.Request) {
	cl, _, ok := s.sftpClient(w, r)
	if !ok {
		return
	}
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		writeErr(w, 400, fmt.Errorf("dir required"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	defer file.Close()
	dst := path.Join(dir, path.Base(header.Filename))
	if err := sftpbrowse.Upload(cl, dst, file); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"path": dst})
}

func (s *Server) handleSftpMkdir(w http.ResponseWriter, r *http.Request) {
	cl, _, ok := s.sftpClient(w, r)
	if !ok {
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &body); err != nil || body.Path == "" {
		writeErr(w, 400, fmt.Errorf("path required"))
		return
	}
	if err := sftpbrowse.Mkdir(cl, body.Path); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleSftpRename(w http.ResponseWriter, r *http.Request) {
	cl, _, ok := s.sftpClient(w, r)
	if !ok {
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := readJSON(r, &body); err != nil || body.From == "" || body.To == "" {
		writeErr(w, 400, fmt.Errorf("from and to required"))
		return
	}
	if err := sftpbrowse.Rename(cl, body.From, body.To); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleSftpRemove(w http.ResponseWriter, r *http.Request) {
	cl, _, ok := s.sftpClient(w, r)
	if !ok {
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		writeErr(w, 400, fmt.Errorf("path required"))
		return
	}
	if err := sftpbrowse.Remove(cl, p); err != nil {
		writeErr(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSftpDisconnect(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.sftp.Disconnect(id)
	w.WriteHeader(http.StatusNoContent)
}
