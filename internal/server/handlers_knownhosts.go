package server

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"webssh/internal/knownhosts"
	"webssh/internal/store"
)

// knownHostsPath is the user's ~/.ssh/known_hosts file.
func (s *Server) knownHostsPath() string { return filepath.Join(s.sshDir(), "known_hosts") }

// enrichKnownHosts marks each entry with its fingerprint and whether an
// equivalent entry currently lives in ~/.ssh/known_hosts.
func (s *Server) enrichKnownHosts(list []store.KnownHost) {
	idx := knownhosts.IndexSSH(s.knownHostsPath())
	for i := range list {
		list[i].Fingerprint = knownhosts.Fingerprint(list[i])
		list[i].InSSH = idx[knownhosts.Identity(list[i])]
	}
}

// storeKnownHosts inserts parsed entries and returns how many were newly added.
func (s *Server) storeKnownHosts(entries []store.KnownHost) (added int, err error) {
	for _, kh := range entries {
		_, created, cerr := s.st.CreateKnownHost(kh)
		if cerr != nil {
			return added, cerr
		}
		if created {
			added++
		}
	}
	return added, nil
}

// handleImportKnownHosts imports entries from ~/.ssh/known_hosts (or a custom
// server-side path) into the webssh store.
func (s *Server) handleImportKnownHosts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	_ = readJSON(r, &body)
	path := strings.TrimSpace(body.Path)
	if path == "" {
		path = s.knownHostsPath()
	} else {
		path = expandHome(path, s.paths.Home)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("read %s: %w", path, err))
		return
	}
	entries := knownhosts.Parse(data)
	added, err := s.storeKnownHosts(entries)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]int{"added": added, "parsed": len(entries)})
}

// handleAddKnownHosts imports entries from pasted/uploaded text (JSON, not a
// multipart upload) into the webssh store.
func (s *Server) handleAddKnownHosts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data string `json:"data"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	entries := knownhosts.Parse([]byte(body.Data))
	if len(entries) == 0 {
		writeErr(w, 400, fmt.Errorf("no valid known_hosts entries found"))
		return
	}
	added, err := s.storeKnownHosts(entries)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]int{"added": added, "parsed": len(entries)})
}

// handleScanKnownHost runs ssh-keyscan against a host and stores the entries.
func (s *Server) handleScanKnownHost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	host := strings.TrimSpace(body.Host)
	if host == "" {
		writeErr(w, 400, fmt.Errorf("host required"))
		return
	}
	if body.Port == 0 {
		body.Port = 22
	}
	cmd := exec.Command("ssh-keyscan", "-p", strconv.Itoa(body.Port), "-T", "10", host)
	out, err := cmd.Output()
	if err != nil {
		writeErr(w, 400, fmt.Errorf("ssh-keyscan %s: %v", host, err))
		return
	}
	entries := knownhosts.Parse(out)
	if len(entries) == 0 {
		writeErr(w, 400, fmt.Errorf("no host keys returned for %s", host))
		return
	}
	added, err := s.storeKnownHosts(entries)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]int{"added": added, "parsed": len(entries)})
}

// handleExportAllKnownHosts writes every managed entry into ~/.ssh/known_hosts.
func (s *Server) handleExportAllKnownHosts(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListKnownHosts()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	written := 0
	for _, kh := range list {
		if knownhosts.ExportToSSH(s.knownHostsPath(), kh) == nil {
			written++
		}
	}
	writeJSON(w, 200, map[string]int{"written": written, "total": len(list)})
}

// handleSSHExportKnownHost writes one managed entry into ~/.ssh/known_hosts.
func (s *Server) handleSSHExportKnownHost(w http.ResponseWriter, r *http.Request) {
	kh, ok := s.knownHostByID(w, r)
	if !ok {
		return
	}
	if err := knownhosts.ExportToSSH(s.knownHostsPath(), kh); err != nil {
		writeErr(w, 400, err)
		return
	}
	list := []store.KnownHost{kh}
	s.enrichKnownHosts(list)
	writeJSON(w, 200, list[0])
}

// handleSSHRemoveKnownHost deletes one entry from ~/.ssh/known_hosts, keeping the
// webssh-managed copy.
func (s *Server) handleSSHRemoveKnownHost(w http.ResponseWriter, r *http.Request) {
	kh, ok := s.knownHostByID(w, r)
	if !ok {
		return
	}
	if err := knownhosts.RemoveFromSSH(s.knownHostsPath(), kh); err != nil {
		writeErr(w, 500, err)
		return
	}
	list := []store.KnownHost{kh}
	s.enrichKnownHosts(list)
	writeJSON(w, 200, list[0])
}

// handleDeleteKnownHost removes a managed entry (does not touch ~/.ssh).
func (s *Server) handleDeleteKnownHost(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.st.DeleteKnownHost(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// knownHostByID loads the entry named by the {id} path value, writing an error
// response and returning ok=false on failure.
func (s *Server) knownHostByID(w http.ResponseWriter, r *http.Request) (store.KnownHost, bool) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return store.KnownHost{}, false
	}
	kh, err := s.st.GetKnownHost(id)
	if err != nil {
		writeErr(w, 404, err)
		return store.KnownHost{}, false
	}
	return kh, true
}
