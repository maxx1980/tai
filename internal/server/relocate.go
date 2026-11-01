package server

import (
	"log"
	"path/filepath"
	"strings"

	"webssh/internal/config"
	"webssh/internal/store"
)

// The database stores some absolute paths: each key's private_path, a host's
// identity_file, and settings such as mount_base_dir. When the data directory
// or an encrypted backup moves to another account (or another machine with a
// different $HOME), those paths still point into the old user's home. The
// helpers below detect the old home and rewrite such paths onto the current one.

// keysSuffix is where a key file lives relative to the home directory it was
// created under; it is how an old home is recognised from a key path.
var keysSuffix = string(filepath.Separator) + filepath.Join(".local", "share", "webssh", "keys")

// mountSuffix is the tail of the default mount_base_dir.
var mountSuffix = string(filepath.Separator) + filepath.Join("mnt", "webssh")

// homeFromKeyPath returns the home directory a managed key path was created
// under, e.g. /home/alice for /home/alice/.local/share/webssh/keys/id_x.
func homeFromKeyPath(priv string) (string, bool) {
	if priv == "" {
		return "", false
	}
	dir := filepath.Dir(filepath.Clean(priv))
	home, ok := strings.CutSuffix(dir, keysSuffix)
	if !ok || home == "" {
		return "", false
	}
	return home, true
}

// homeFromMountBase returns the home directory a default-style mount_base_dir
// was derived from, e.g. /home/alice for /home/alice/mnt/webssh.
func homeFromMountBase(dir string) (string, bool) {
	home, ok := strings.CutSuffix(filepath.Clean(dir), mountSuffix)
	if !ok || home == "" {
		return "", false
	}
	return home, true
}

// foreignHomes collects the distinct home directories, other than current,
// that the given hints and stored data refer to.
func foreignHomes(current string, hints []string, keys []store.Key, settings map[string]string) []string {
	current = filepath.Clean(current)
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		if h == "" {
			return
		}
		h = filepath.Clean(h)
		if h == current || h == string(filepath.Separator) || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, h := range hints {
		add(h)
	}
	for _, k := range keys {
		if h, ok := homeFromKeyPath(k.PrivatePath); ok {
			add(h)
		}
	}
	if h, ok := homeFromMountBase(settings[config.KeyMountBaseDir]); ok {
		add(h)
	}
	return out
}

// snapshotHomes returns the home directories a backup was made under: the
// recorded one, plus any implied by its key paths (older backups lack Home).
func snapshotHomes(snap *store.Snapshot) []string {
	var out []string
	if snap.Home != "" {
		out = append(out, snap.Home)
	}
	for _, k := range snap.Keys {
		if h, ok := homeFromKeyPath(k.PrivatePath); ok {
			out = append(out, h)
		}
	}
	if h, ok := homeFromMountBase(snap.Settings[config.KeyMountBaseDir]); ok {
		out = append(out, h)
	}
	return out
}

// relocateHome rewrites stored paths that point into another user's home so
// they point into the current one. hints are extra old homes known to the
// caller (e.g. from a backup being restored). It reports whether anything
// changed; failures are logged, never fatal — a stale path is no worse than
// before.
func (s *Server) relocateHome(hints []string) bool {
	home := s.paths.Home
	keys, err := s.st.ListKeys()
	if err != nil {
		log.Printf("relocate: list keys: %v", err)
		return false
	}
	settings, err := s.st.AllSettings()
	if err != nil {
		log.Printf("relocate: read settings: %v", err)
		return false
	}
	changed := false

	// Keys always live in the managed directory under their own name, so their
	// path can be derived outright rather than prefix-rewritten.
	keysDir := s.keysDir()
	for _, k := range keys {
		want := filepath.Join(keysDir, k.Name)
		if k.PrivatePath == want || k.Name == "" || strings.ContainsAny(k.Name, `/\`) {
			continue
		}
		if _, ok := homeFromKeyPath(k.PrivatePath); !ok && k.PrivatePath != "" {
			continue // not a managed path; leave it alone
		}
		old := k.PrivatePath
		k.PrivatePath = want
		if err := s.st.UpdateKey(k); err != nil {
			log.Printf("relocate: key %q: %v", k.Name, err)
			continue
		}
		log.Printf("relocate: key %q: %s -> %s", k.Name, old, want)
		changed = true
	}

	homes := foreignHomes(home, hints, keys, settings)
	sep := string(filepath.Separator)
	for _, old := range homes {
		// A default mount_base_dir from the old home becomes the default again,
		// so it follows $HOME from now on; any other path under it is rewritten.
		if v, ok := settings[config.KeyMountBaseDir]; ok {
			if filepath.Clean(v) == old+mountSuffix {
				if err := s.st.DeleteSetting(config.KeyMountBaseDir); err == nil {
					log.Printf("relocate: %s: %s -> default", config.KeyMountBaseDir, v)
					changed = true
				}
			} else if rest, ok := strings.CutPrefix(v, old+sep); ok {
				nv := filepath.Join(home, rest)
				if err := s.st.SetSetting(config.KeyMountBaseDir, nv); err == nil {
					log.Printf("relocate: %s: %s -> %s", config.KeyMountBaseDir, v, nv)
					changed = true
				}
			}
		}
		for _, key := range []string{config.KeyAppBrowser, config.KeyTerminalCmd, config.KeyFilesCmd, config.KeyBrowserCmd} {
			v, ok := settings[key]
			if !ok || !strings.Contains(v, old+sep) {
				continue
			}
			nv := strings.ReplaceAll(v, old+sep, home+sep)
			if err := s.st.SetSetting(key, nv); err == nil {
				log.Printf("relocate: %s: %s -> %s", key, v, nv)
				changed = true
			}
		}
		n, err := s.st.ReplaceIdentityFilePrefix(old+sep, home+sep)
		if err != nil {
			log.Printf("relocate: identity files: %v", err)
		} else if n > 0 {
			log.Printf("relocate: %d host identity file(s): %s -> %s", n, old, home)
			changed = true
		}
	}
	return changed
}
