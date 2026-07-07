package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"webssh/internal/askpass"
	"webssh/internal/config"
	"webssh/internal/keys"
	"webssh/internal/launcher"
	"webssh/internal/mount"
	"webssh/internal/sshconfig"
	"webssh/internal/store"
)

// sshArgs builds explicit `ssh` arguments from a host record, so a connection
// works whether or not the alias has been exported to ssh_config.
func (s *Server) sshArgs(h store.Host) []string {
	args := []string{}
	if h.Port != 0 && h.Port != 22 {
		args = append(args, "-p", strconv.Itoa(h.Port))
	}
	if h.IdentityFile != "" {
		args = append(args, "-i", expandHome(h.IdentityFile, s.paths.Home))
	}
	if h.ProxyJump != "" {
		args = append(args, "-J", h.ProxyJump)
	}
	for k, v := range h.ExtraOptions {
		if v == "" {
			continue
		}
		args = append(args, "-o", k+"="+v)
	}
	target := h.Hostname
	if target == "" {
		target = h.Alias // fall back to ssh_config alias
	}
	if h.User != "" {
		target = h.User + "@" + target
	}
	return append(args, target)
}

// expandHome expands a leading ~ to the user's home directory (ssh does this
// for config files, but not for -i on the command line when run without a shell).
func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// pathID parses the {id} path value.
func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

// autoExport keeps the managed ssh_config file in sync after inventory changes,
// so `ssh <alias>` (native terminal, plain shell) resolves without a manual
// export. Best-effort: a failure is logged but does not fail the request.
func (s *Server) autoExport() {
	hosts, err := s.st.ListHosts()
	if err != nil {
		return
	}
	if err := sshconfig.Export(hosts, s.paths.ManagedConfig, s.paths.SSHConfig); err != nil {
		log.Printf("auto-export ssh_config: %v", err)
	}
}

// settingsWithDefaults merges stored settings over the defaults.
func (s *Server) settingsWithDefaults() map[string]string {
	out := config.Defaults(s.paths.Home)
	stored, _ := s.st.AllSettings()
	for k, v := range stored {
		out[k] = v
	}
	delete(out, config.KeyMasterPassword) // reserved: never expose the master-password hash
	return out
}

// handleState returns the full application snapshot for the SPA.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.st.ListHosts()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	groups, err := s.st.ListGroups()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	tags, err := s.st.ListTags()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	keyList, err := s.st.ListKeys()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.enrichKeys(keyList)
	knownHosts, err := s.st.ListKnownHosts()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.enrichKnownHosts(knownHosts)
	settings := s.settingsWithDefaults()
	mounts, _ := mount.List(settings[config.KeyMountBaseDir])
	writeJSON(w, 200, map[string]any{
		"hosts":       hosts,
		"groups":      groups,
		"tags":        tags,
		"keys":        keyList,
		"known_hosts": knownHosts,
		"settings":    settings,
		"mounts":      mounts,
	})
}

// handleHealth returns the current per-host reachability snapshot as a JSON
// object keyed by host id: {"1":"green","2":"red",...}. Hosts not yet probed are
// simply absent (the SPA renders those as "unknown").
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.health.Snapshot())
}

// ---- hosts ----

func (s *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	var h store.Host
	if err := readJSON(r, &h); err != nil {
		writeErr(w, 400, err)
		return
	}
	created, err := s.st.CreateHost(h)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.autoExport()
	writeJSON(w, 201, created)
}

func (s *Server) handleUpdateHost(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var h store.Host
	if err := readJSON(r, &h); err != nil {
		writeErr(w, 400, err)
		return
	}
	h.ID = id
	if err := s.st.UpdateHost(h); err != nil {
		writeErr(w, 400, err)
		return
	}
	// Password is preserved by UpdateHost; only touch it on explicit request.
	if h.ClearPassword {
		_ = s.st.ClearHostPassword(id)
	} else if h.Password != "" {
		_ = s.st.SetHostPassword(id, h.Password)
	}
	updated, err := s.st.GetHost(id)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.autoExport()
	writeJSON(w, 200, updated)
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.st.DeleteHost(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.autoExport()
	w.WriteHeader(http.StatusNoContent)
}

// ---- groups ----

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var g store.Group
	if err := readJSON(r, &g); err != nil {
		writeErr(w, 400, err)
		return
	}
	created, err := s.st.CreateGroup(g)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 201, created)
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var g store.Group
	if err := readJSON(r, &g); err != nil {
		writeErr(w, 400, err)
		return
	}
	g.ID = id
	if err := s.st.UpdateGroup(g); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, g)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.st.DeleteGroup(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- ssh_config sync ----

// importHosts upserts parsed hosts by alias and returns the count.
func (s *Server) importHosts(hosts []store.Host) (int, error) {
	var n int
	for _, h := range hosts {
		if _, err := s.st.UpsertHostByAlias(h); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// handleImport imports from ~/.ssh/config, or a custom path when {path} is given.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	_ = readJSON(r, &body)
	path := s.paths.SSHConfig
	if strings.TrimSpace(body.Path) != "" {
		path = expandHome(strings.TrimSpace(body.Path), s.paths.Home)
	}
	hosts, err := sshconfig.Import(path)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	imported, err := s.importHosts(hosts)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"imported": imported, "path": path})
}

// handleImportUpload imports ssh_config content uploaded from the browser.
func (s *Server) handleImportUpload(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	defer file.Close()
	hosts, err := sshconfig.Parse(file)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	imported, err := s.importHosts(hosts)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"imported": imported})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.st.ListHosts()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if err := sshconfig.Export(hosts, s.paths.ManagedConfig, s.paths.SSHConfig); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"exported": len(hosts), "path": s.paths.ManagedConfig})
}

// ---- keys ----

func (s *Server) handleGenerateKey(w http.ResponseWriter, r *http.Request) {
	var p keys.GenParams
	if err := readJSON(r, &p); err != nil {
		writeErr(w, 400, err)
		return
	}
	keysDir := filepath.Join(s.paths.DataDir, "keys")
	k, err := keys.Generate(keysDir, p)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	saved, err := s.st.CreateKey(k)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 201, saved)
}

// handleImportKey imports an existing keypair. The key material is sent as JSON
// text (not a multipart upload) so it works even where the browser blocks file
// uploads. If a key of the same name already exists it returns 409 {exists:true}
// unless overwrite=true, so the SPA can warn before replacing it.
func (s *Server) handleImportKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		Private   string `json:"private"`
		Public    string `json:"public"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeErr(w, 400, fmt.Errorf("key name required"))
		return
	}
	if strings.TrimSpace(body.Private) == "" {
		writeErr(w, 400, fmt.Errorf("private key is empty"))
		return
	}
	// OpenSSH tooling expects a trailing newline in key files.
	priv := body.Private
	if !strings.HasSuffix(priv, "\n") {
		priv += "\n"
	}
	var pubData []byte
	if strings.TrimSpace(body.Public) != "" {
		pubData = []byte(body.Public)
	}

	keysDir := filepath.Join(s.paths.DataDir, "keys")
	existing, hasDB, _ := s.st.GetKeyByName(name)
	_, statErr := os.Stat(filepath.Join(keysDir, name))
	if (hasDB || statErr == nil) && !body.Overwrite {
		writeJSON(w, http.StatusConflict, map[string]any{
			"exists": true,
			"error":  fmt.Sprintf("a key named %q already exists", name),
		})
		return
	}

	k, err := keys.Import(keysDir, keys.ImportParams{Name: name, PrivateData: []byte(priv), PublicData: pubData})
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if hasDB {
		k.ID = existing.ID
		if err := s.st.UpdateKey(k); err != nil {
			writeErr(w, 500, err)
			return
		}
	} else {
		if k, err = s.st.CreateKey(k); err != nil {
			writeErr(w, 500, err)
			return
		}
	}
	writeJSON(w, 201, k)
}

// handleExportKey streams a key's public or private file as a download.
func (s *Server) handleExportKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	k, err := s.st.GetKey(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	switch r.URL.Query().Get("kind") {
	case "public":
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", k.Name+".pub"))
		io.WriteString(w, strings.TrimSpace(k.PublicKey)+"\n")
	case "private":
		data, err := os.ReadFile(k.PrivatePath)
		if err != nil {
			writeErr(w, 404, fmt.Errorf("private key file unavailable: %v", err))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", k.Name))
		w.Write(data)
	default:
		writeErr(w, 400, fmt.Errorf("kind must be 'public' or 'private'"))
	}
}

// sshDir is the user's ~/.ssh directory.
func (s *Server) sshDir() string { return filepath.Join(s.paths.Home, ".ssh") }

// enrichKeys marks each key with whether an equivalent copy (matched by public
// key material) exists in ~/.ssh, and where.
func (s *Server) enrichKeys(list []store.Key) {
	idx := keys.IndexSSHDir(s.sshDir())
	for i := range list {
		list[i].InSSH = false
		list[i].SSHPath = ""
		if id, ok := keys.PubKeyID(list[i].PublicKey); ok {
			if p, found := idx[id]; found {
				list[i].InSSH = true
				list[i].SSHPath = p
			}
		}
	}
}

// handleSSHExport copies a managed key into ~/.ssh so plain `ssh` can use it.
func (s *Server) handleSSHExport(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	k, err := s.st.GetKey(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if _, err := keys.ExportToSSH(s.sshDir(), k); err != nil {
		writeErr(w, 400, err)
		return
	}
	list := []store.Key{k}
	s.enrichKeys(list)
	writeJSON(w, 200, list[0])
}

// handleSSHRemove deletes the ~/.ssh copy of a key (matched by public key
// material), leaving only the webssh-managed copy.
func (s *Server) handleSSHRemove(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	k, err := s.st.GetKey(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	keyID, ok := keys.PubKeyID(k.PublicKey)
	if !ok {
		writeErr(w, 400, fmt.Errorf("key has no valid public key"))
		return
	}
	path, found := keys.IndexSSHDir(s.sshDir())[keyID]
	if !found {
		writeErr(w, 404, fmt.Errorf("no copy of this key in ~/.ssh"))
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		writeErr(w, 500, err)
		return
	}
	_ = os.Remove(path + ".pub")
	list := []store.Key{k}
	s.enrichKeys(list)
	writeJSON(w, 200, list[0])
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.st.DeleteKey(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeployKey(w http.ResponseWriter, r *http.Request) {
	keyID, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var body struct {
		HostIDs  []int64 `json:"host_ids"`
		Password string  `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	k, err := s.st.GetKey(keyID)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	results := make([]map[string]any, 0, len(body.HostIDs))
	for _, hid := range body.HostIDs {
		h, err := s.st.GetHost(hid)
		if err != nil {
			results = append(results, map[string]any{"host_id": hid, "ok": false, "error": "host not found"})
			continue
		}
		// Prefer the host's saved password; fall back to one supplied in the
		// request (used when a host has no stored password).
		pw := s.st.GetHostPassword(hid)
		if pw == "" {
			pw = body.Password
		}
		if pw == "" {
			results = append(results, map[string]any{"host_id": hid, "alias": h.Alias, "ok": false, "error": "no saved password for host"})
			continue
		}
		derr := keys.Deploy(keys.DeployTarget{Hostname: hostAddr(h), User: h.User, Port: h.Port}, pw, k.PublicKey)
		if derr != nil {
			results = append(results, map[string]any{"host_id": hid, "alias": h.Alias, "ok": false, "error": derr.Error()})
			continue
		}
		_ = s.st.MarkKeyDeployed(keyID, hid)
		results = append(results, map[string]any{"host_id": hid, "alias": h.Alias, "ok": true})
	}
	writeJSON(w, 200, map[string]any{"results": results})
}

func hostAddr(h store.Host) string {
	if h.Hostname != "" {
		return h.Hostname
	}
	return h.Alias
}

// ---- mounts ----

func (s *Server) handleListMounts(w http.ResponseWriter, r *http.Request) {
	base := s.settingsWithDefaults()[config.KeyMountBaseDir]
	mounts, err := mount.List(base)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"mounts": mounts})
}

func (s *Server) handleMount(w http.ResponseWriter, r *http.Request) {
	h, base, err := s.hostAndBase(r)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	// Optional password (empty body is fine). Fall back to a saved host password.
	var body struct {
		Password string `json:"password"`
	}
	_ = readJSON(r, &body)
	pw := body.Password
	if pw == "" {
		pw = s.st.GetHostPassword(h.ID)
	}
	point, err := mount.Mount(base, h, pw)
	if err != nil {
		// Signal the SPA to collect a password and retry, instead of the
		// underlying ssh prompting on the daemon console.
		if errors.Is(err, mount.ErrNeedPassword) {
			writeJSON(w, http.StatusPreconditionRequired, map[string]any{
				"need_password": true,
				"error":         err.Error(),
			})
			return
		}
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"mountpoint": point})
}

func (s *Server) handleUnmount(w http.ResponseWriter, r *http.Request) {
	h, base, err := s.hostAndBase(r)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if err := mount.Unmount(base, h); err != nil {
		writeErr(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) hostAndBase(r *http.Request) (store.Host, string, error) {
	id, err := pathID(r)
	if err != nil {
		return store.Host{}, "", err
	}
	h, err := s.st.GetHost(id)
	if err != nil {
		return store.Host{}, "", err
	}
	return h, s.settingsWithDefaults()[config.KeyMountBaseDir], nil
}

// ---- native launch ----

func (s *Server) handleLaunchTerminal(w http.ResponseWriter, r *http.Request) {
	s.launch(w, r, config.KeyTerminalCmd)
}

func (s *Server) handleLaunchFiles(w http.ResponseWriter, r *http.Request) {
	s.launch(w, r, config.KeyFilesCmd)
}

func (s *Server) launch(w http.ResponseWriter, r *http.Request, cmdKey string) {
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
	settings := s.settingsWithDefaults()
	mp := mount.Mountpoint(settings[config.KeyMountBaseDir], h)

	// For the native terminal, feed a saved password to ssh via a per-host
	// wrapper that self-sets SSH_ASKPASS (env alone doesn't reach ssh under
	// single-server terminal emulators). The file manager launch (xdg-open on a
	// mountpoint) spawns no ssh, so it needs nothing.
	var sshWrapper string
	if cmdKey == config.KeyTerminalCmd {
		if pw := s.st.GetHostPassword(h.ID); pw != "" {
			if helper, herr := askpass.Ensure(s.paths.DataDir); herr == nil {
				if wp, werr := askpass.Wrapper(s.paths.DataDir, h.ID, helper, pw); werr == nil {
					sshWrapper = wp
				} else {
					log.Printf("askpass wrapper: %v", werr)
				}
			} else {
				log.Printf("askpass helper: %v", herr)
			}
		}
	}

	if err := launcher.Spawn(settings[cmdKey], launcher.VarsFromHost(h, mp), sshWrapper); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- settings ----

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	for k, v := range body {
		if k == config.KeyMasterPassword {
			continue // reserved: set only via PUT /api/settings/password
		}
		if err := s.st.SetSetting(k, v); err != nil {
			writeErr(w, 500, fmt.Errorf("save %s: %w", k, err))
			return
		}
	}
	// Keep the cached auth-disabled flag in sync with the stored setting.
	s.authDisabled.Store(s.st.GetSetting(config.KeyAuthDisabled, "") == "1")
	writeJSON(w, 200, s.settingsWithDefaults())
}

// handleRestart re-executes the daemon in place: it replies, then replaces the
// process image with a fresh copy (same PID, same args). The listening socket is
// close-on-exec so the port frees up and the new image rebinds (SO_REUSEADDR) and
// reopens the browser per browser_cmd. Active connections drop for ~0.5s. Unix
// only, which matches the rest of the app (pty, xdg-open).
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"restarting": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(400 * time.Millisecond)
		exe, err := os.Executable()
		if err != nil {
			log.Printf("restart: cannot resolve executable: %v", err)
			return
		}
		if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
			log.Printf("restart: exec failed: %v", err)
		}
	}()
}
