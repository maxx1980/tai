// Package config resolves filesystem paths and default settings for webssh.
package config

import (
	"os"
	"path/filepath"
)

// Setting keys stored in the DB.
const (
	KeyTerminalCmd  = "terminal_cmd"
	KeyFilesCmd     = "files_cmd"
	KeyMountBaseDir = "mount_base_dir"
	KeyTheme        = "theme"

	// KeyMasterPassword stores the argon2id hash of the master password. It is
	// reserved: never returned to the SPA nor writable via PUT /api/settings.
	KeyMasterPassword = "master_pw"
)

// Defaults returns the default value for a setting key.
func Defaults(home string) map[string]string {
	return map[string]string{
		// {{alias}} {{user}} {{host}} {{port}} {{mountpoint}} are substituted at spawn time.
		KeyTerminalCmd:  "gnome-terminal -- ssh {{alias}}",
		KeyFilesCmd:     "xdg-open {{mountpoint}}",
		KeyMountBaseDir: filepath.Join(home, "mnt", "webssh"),
		KeyTheme:        "system",
	}
}

// Paths holds the resolved locations webssh reads and writes.
type Paths struct {
	Home          string // user home dir
	DataDir       string // ~/.local/share/webssh
	DBPath        string // <DataDir>/webssh.db
	SSHDir        string // ~/.ssh
	SSHConfig     string // ~/.ssh/config
	ManagedConfig string // ~/.ssh/config.d/inventory
}

// Resolve builds the standard paths for the current user.
func Resolve() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	dataDir := filepath.Join(home, ".local", "share", "webssh")
	sshDir := filepath.Join(home, ".ssh")
	return Paths{
		Home:          home,
		DataDir:       dataDir,
		DBPath:        filepath.Join(dataDir, "webssh.db"),
		SSHDir:        sshDir,
		SSHConfig:     filepath.Join(sshDir, "config"),
		ManagedConfig: filepath.Join(sshDir, "config.d", "inventory"),
	}, nil
}

// EnsureDirs creates the data directory if missing.
func (p Paths) EnsureDirs() error {
	return os.MkdirAll(p.DataDir, 0o700)
}
