// Package store owns the SQLite database that is the source of truth for the
// inventory (hosts, groups, tags) and generated keys. ssh_config is imported
// into and exported from this store; the DB always wins.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO
)

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Group is a node in the inventory hierarchy (prod/staging, per-client, ...).
type Group struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id"`
	Sort     int    `json:"sort"`
}

// Host is a single SSH endpoint.
type Host struct {
	ID           int64             `json:"id"`
	Alias        string            `json:"alias"`
	Hostname     string            `json:"hostname"`
	User         string            `json:"user"`
	Port         int               `json:"port"`
	IdentityFile string            `json:"identity_file"`
	ProxyJump    string            `json:"proxy_jump"`
	ExtraOptions map[string]string `json:"extra_options"`
	GroupID      *int64            `json:"group_id"`
	Notes        string            `json:"notes"`
	Tags         []string          `json:"tags"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`

	// Password is input-only: it is stored server-side and never returned on
	// reads (HasPassword reflects whether one is saved). ClearPassword requests
	// removing a saved password on update.
	Password      string `json:"password,omitempty"`
	HasPassword   bool   `json:"has_password"`
	ClearPassword bool   `json:"clear_password,omitempty"`
}

// Key is a generated SSH keypair the app tracks.
type Key struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	PublicKey     string  `json:"public_key"`
	PrivatePath   string  `json:"private_path"`
	HasPassphrase bool    `json:"has_passphrase"`
	DeployedHosts []int64 `json:"deployed_hosts"`

	// Computed at request time (not persisted): whether a copy of this key also
	// lives in ~/.ssh, and where.
	InSSH   bool   `json:"in_ssh"`
	SSHPath string `json:"ssh_path,omitempty"`
}

// Open opens (creating if needed) the SQLite database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: serialize writes, avoids "database is locked"
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS groups (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	name      TEXT NOT NULL,
	parent_id INTEGER REFERENCES groups(id) ON DELETE SET NULL,
	sort      INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS hosts (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	alias         TEXT NOT NULL UNIQUE,
	hostname      TEXT NOT NULL DEFAULT '',
	user          TEXT NOT NULL DEFAULT '',
	port          INTEGER NOT NULL DEFAULT 22,
	identity_file TEXT NOT NULL DEFAULT '',
	proxy_jump    TEXT NOT NULL DEFAULT '',
	extra_options TEXT NOT NULL DEFAULT '{}',
	group_id      INTEGER REFERENCES groups(id) ON DELETE SET NULL,
	notes         TEXT NOT NULL DEFAULT '',
	created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS tags (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS host_tags (
	host_id INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
	tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	PRIMARY KEY (host_id, tag_id)
);
CREATE TABLE IF NOT EXISTS keys (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	name           TEXT NOT NULL,
	type           TEXT NOT NULL DEFAULT 'ed25519',
	public_key     TEXT NOT NULL DEFAULT '',
	private_path   TEXT NOT NULL DEFAULT '',
	has_passphrase INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS host_keys (
	key_id  INTEGER NOT NULL REFERENCES keys(id) ON DELETE CASCADE,
	host_id INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
	PRIMARY KEY (key_id, host_id)
);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS known_hosts (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	marker   TEXT NOT NULL DEFAULT '',
	hosts    TEXT NOT NULL,
	key_type TEXT NOT NULL,
	key_data TEXT NOT NULL,
	comment  TEXT NOT NULL DEFAULT '',
	added_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(hosts, key_data)
);`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Incremental migrations (ignore "duplicate column" on re-run).
	for _, stmt := range []string{
		`ALTER TABLE hosts ADD COLUMN password TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}

// ---- Groups ----

// ListGroups returns all groups ordered by sort then name.
func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(`SELECT id, name, parent_id, sort FROM groups ORDER BY sort, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.ParentID, &g.Sort); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreateGroup inserts a group and returns it with its new ID.
func (s *Store) CreateGroup(g Group) (Group, error) {
	res, err := s.db.Exec(`INSERT INTO groups (name, parent_id, sort) VALUES (?, ?, ?)`, g.Name, g.ParentID, g.Sort)
	if err != nil {
		return Group{}, err
	}
	g.ID, _ = res.LastInsertId()
	return g, nil
}

// UpdateGroup updates a group by ID.
func (s *Store) UpdateGroup(g Group) error {
	_, err := s.db.Exec(`UPDATE groups SET name=?, parent_id=?, sort=? WHERE id=?`, g.Name, g.ParentID, g.Sort, g.ID)
	return err
}

// DeleteGroup removes a group (children/hosts have group_id set NULL).
func (s *Store) DeleteGroup(id int64) error {
	_, err := s.db.Exec(`DELETE FROM groups WHERE id=?`, id)
	return err
}

// ---- Hosts ----

// ListHosts returns all hosts with their tags attached.
func (s *Store) ListHosts() ([]Host, error) {
	rows, err := s.db.Query(`SELECT id, alias, hostname, user, port, identity_file, proxy_jump, extra_options, group_id, notes, created_at, updated_at, (password != '') FROM hosts ORDER BY alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachTags(out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetHost returns a single host by ID with tags.
func (s *Store) GetHost(id int64) (Host, error) {
	row := s.db.QueryRow(`SELECT id, alias, hostname, user, port, identity_file, proxy_jump, extra_options, group_id, notes, created_at, updated_at, (password != '') FROM hosts WHERE id=?`, id)
	h, err := scanHost(row)
	if err != nil {
		return Host{}, err
	}
	list := []Host{h}
	if err := s.attachTags(list); err != nil {
		return Host{}, err
	}
	return list[0], nil
}

type scanner interface{ Scan(dest ...any) error }

func scanHost(sc scanner) (Host, error) {
	var h Host
	var extra string
	if err := sc.Scan(&h.ID, &h.Alias, &h.Hostname, &h.User, &h.Port, &h.IdentityFile, &h.ProxyJump, &extra, &h.GroupID, &h.Notes, &h.CreatedAt, &h.UpdatedAt, &h.HasPassword); err != nil {
		return Host{}, err
	}
	h.ExtraOptions = map[string]string{}
	if extra != "" {
		_ = json.Unmarshal([]byte(extra), &h.ExtraOptions)
	}
	return h, nil
}

func (s *Store) attachTags(hosts []Host) error {
	if len(hosts) == 0 {
		return nil
	}
	byID := map[int64]*Host{}
	for i := range hosts {
		hosts[i].Tags = []string{}
		byID[hosts[i].ID] = &hosts[i]
	}
	rows, err := s.db.Query(`SELECT ht.host_id, t.name FROM host_tags ht JOIN tags t ON t.id = ht.tag_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hid int64
		var name string
		if err := rows.Scan(&hid, &name); err != nil {
			return err
		}
		if h, ok := byID[hid]; ok {
			h.Tags = append(h.Tags, name)
		}
	}
	return rows.Err()
}

// CreateHost inserts a host (and its tags) and returns it with the new ID.
func (s *Store) CreateHost(h Host) (Host, error) {
	extra, _ := json.Marshal(h.ExtraOptions)
	if h.Port == 0 {
		h.Port = 22
	}
	res, err := s.db.Exec(`INSERT INTO hosts (alias, hostname, user, port, identity_file, proxy_jump, extra_options, group_id, notes, password) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		h.Alias, h.Hostname, h.User, h.Port, h.IdentityFile, h.ProxyJump, string(extra), h.GroupID, h.Notes, h.Password)
	if err != nil {
		return Host{}, err
	}
	h.ID, _ = res.LastInsertId()
	if err := s.setHostTags(h.ID, h.Tags); err != nil {
		return Host{}, err
	}
	return s.GetHost(h.ID)
}

// SetHostPassword saves (or overwrites) the stored password for a host.
func (s *Store) SetHostPassword(id int64, password string) error {
	_, err := s.db.Exec(`UPDATE hosts SET password=? WHERE id=?`, password, id)
	return err
}

// ClearHostPassword removes the stored password for a host.
func (s *Store) ClearHostPassword(id int64) error {
	return s.SetHostPassword(id, "")
}

// GetHostPassword returns the stored password for a host (empty if none).
func (s *Store) GetHostPassword(id int64) string {
	var pw string
	_ = s.db.QueryRow(`SELECT password FROM hosts WHERE id=?`, id).Scan(&pw)
	return pw
}

// UpsertHostByAlias inserts or updates a host keyed by its unique alias. Used by import.
func (s *Store) UpsertHostByAlias(h Host) (Host, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM hosts WHERE alias=?`, h.Alias).Scan(&id)
	switch err {
	case sql.ErrNoRows:
		return s.CreateHost(h)
	case nil:
		h.ID = id
		if err := s.UpdateHost(h); err != nil {
			return Host{}, err
		}
		return s.GetHost(h.ID)
	default:
		return Host{}, err
	}
}

// UpdateHost updates a host and replaces its tag set.
func (s *Store) UpdateHost(h Host) error {
	extra, _ := json.Marshal(h.ExtraOptions)
	if h.Port == 0 {
		h.Port = 22
	}
	_, err := s.db.Exec(`UPDATE hosts SET alias=?, hostname=?, user=?, port=?, identity_file=?, proxy_jump=?, extra_options=?, group_id=?, notes=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		h.Alias, h.Hostname, h.User, h.Port, h.IdentityFile, h.ProxyJump, string(extra), h.GroupID, h.Notes, h.ID)
	if err != nil {
		return err
	}
	return s.setHostTags(h.ID, h.Tags)
}

// DeleteHost removes a host and its tag/key links (via cascade).
func (s *Store) DeleteHost(id int64) error {
	_, err := s.db.Exec(`DELETE FROM hosts WHERE id=?`, id)
	return err
}

func (s *Store) setHostTags(hostID int64, tags []string) error {
	if _, err := s.db.Exec(`DELETE FROM host_tags WHERE host_id=?`, hostID); err != nil {
		return err
	}
	for _, name := range tags {
		if name == "" {
			continue
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO tags (name) VALUES (?)`, name); err != nil {
			return err
		}
		var tagID int64
		if err := s.db.QueryRow(`SELECT id FROM tags WHERE name=?`, name).Scan(&tagID); err != nil {
			return err
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO host_tags (host_id, tag_id) VALUES (?, ?)`, hostID, tagID); err != nil {
			return err
		}
	}
	return nil
}

// ListTags returns all distinct tag names.
func (s *Store) ListTags() ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ---- Keys ----

// ListKeys returns all keys with their deployed host IDs.
func (s *Store) ListKeys() ([]Key, error) {
	rows, err := s.db.Query(`SELECT id, name, type, public_key, private_path, has_passphrase FROM keys ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Key
	byID := map[int64]*Key{}
	for rows.Next() {
		var k Key
		var hp int
		if err := rows.Scan(&k.ID, &k.Name, &k.Type, &k.PublicKey, &k.PrivatePath, &hp); err != nil {
			return nil, err
		}
		k.HasPassphrase = hp != 0
		k.DeployedHosts = []int64{}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	drows, err := s.db.Query(`SELECT key_id, host_id FROM host_keys`)
	if err != nil {
		return nil, err
	}
	defer drows.Close()
	for drows.Next() {
		var kid, hid int64
		if err := drows.Scan(&kid, &hid); err != nil {
			return nil, err
		}
		if k, ok := byID[kid]; ok {
			k.DeployedHosts = append(k.DeployedHosts, hid)
		}
	}
	return out, drows.Err()
}

// GetKey returns a single key by ID.
func (s *Store) GetKey(id int64) (Key, error) {
	var k Key
	var hp int
	err := s.db.QueryRow(`SELECT id, name, type, public_key, private_path, has_passphrase FROM keys WHERE id=?`, id).
		Scan(&k.ID, &k.Name, &k.Type, &k.PublicKey, &k.PrivatePath, &hp)
	if err != nil {
		return Key{}, err
	}
	k.HasPassphrase = hp != 0
	return k, nil
}

// GetKeyByName returns the key with the given name, if any.
func (s *Store) GetKeyByName(name string) (Key, bool, error) {
	var k Key
	var hp int
	err := s.db.QueryRow(`SELECT id, name, type, public_key, private_path, has_passphrase FROM keys WHERE name=?`, name).
		Scan(&k.ID, &k.Name, &k.Type, &k.PublicKey, &k.PrivatePath, &hp)
	switch err {
	case sql.ErrNoRows:
		return Key{}, false, nil
	case nil:
		k.HasPassphrase = hp != 0
		return k, true, nil
	default:
		return Key{}, false, err
	}
}

// UpdateKey updates a key record by ID.
func (s *Store) UpdateKey(k Key) error {
	hp := 0
	if k.HasPassphrase {
		hp = 1
	}
	_, err := s.db.Exec(`UPDATE keys SET name=?, type=?, public_key=?, private_path=?, has_passphrase=? WHERE id=?`,
		k.Name, k.Type, k.PublicKey, k.PrivatePath, hp, k.ID)
	return err
}

// CreateKey records a generated keypair.
func (s *Store) CreateKey(k Key) (Key, error) {
	hp := 0
	if k.HasPassphrase {
		hp = 1
	}
	res, err := s.db.Exec(`INSERT INTO keys (name, type, public_key, private_path, has_passphrase) VALUES (?,?,?,?,?)`,
		k.Name, k.Type, k.PublicKey, k.PrivatePath, hp)
	if err != nil {
		return Key{}, err
	}
	k.ID, _ = res.LastInsertId()
	return k, nil
}

// DeleteKey removes a key record (does not delete files on disk).
func (s *Store) DeleteKey(id int64) error {
	_, err := s.db.Exec(`DELETE FROM keys WHERE id=?`, id)
	return err
}

// MarkKeyDeployed records that key was installed on host.
func (s *Store) MarkKeyDeployed(keyID, hostID int64) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO host_keys (key_id, host_id) VALUES (?, ?)`, keyID, hostID)
	return err
}

// ---- Known hosts ----

// KnownHost is one parsed entry of a known_hosts file, managed by webssh.
type KnownHost struct {
	ID      int64  `json:"id"`
	Marker  string `json:"marker"`   // "", "@cert-authority" or "@revoked"
	Hosts   string `json:"hosts"`    // comma-joined host patterns (or a hashed token)
	KeyType string `json:"key_type"` // e.g. ssh-ed25519
	KeyData string `json:"key_data"` // base64 of the public key's wire marshaling
	Comment string `json:"comment"`

	// Computed at request time (not persisted).
	InSSH       bool   `json:"in_ssh"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// ListKnownHosts returns all managed known_hosts entries.
func (s *Store) ListKnownHosts() ([]KnownHost, error) {
	rows, err := s.db.Query(`SELECT id, marker, hosts, key_type, key_data, comment FROM known_hosts ORDER BY hosts, key_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnownHost{}
	for rows.Next() {
		var kh KnownHost
		if err := rows.Scan(&kh.ID, &kh.Marker, &kh.Hosts, &kh.KeyType, &kh.KeyData, &kh.Comment); err != nil {
			return nil, err
		}
		out = append(out, kh)
	}
	return out, rows.Err()
}

// GetKnownHost returns a single entry by id.
func (s *Store) GetKnownHost(id int64) (KnownHost, error) {
	var kh KnownHost
	err := s.db.QueryRow(`SELECT id, marker, hosts, key_type, key_data, comment FROM known_hosts WHERE id=?`, id).
		Scan(&kh.ID, &kh.Marker, &kh.Hosts, &kh.KeyType, &kh.KeyData, &kh.Comment)
	return kh, err
}

// CreateKnownHost inserts an entry, ignoring duplicates (same hosts + key). The
// stored (new or pre-existing) row is returned; created reports whether it was
// newly inserted.
func (s *Store) CreateKnownHost(kh KnownHost) (row KnownHost, created bool, err error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO known_hosts (marker, hosts, key_type, key_data, comment) VALUES (?,?,?,?,?)`,
		kh.Marker, kh.Hosts, kh.KeyType, kh.KeyData, kh.Comment)
	if err != nil {
		return KnownHost{}, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Already present — return the existing row.
		var e KnownHost
		if err := s.db.QueryRow(`SELECT id, marker, hosts, key_type, key_data, comment FROM known_hosts WHERE hosts=? AND key_data=?`,
			kh.Hosts, kh.KeyData).Scan(&e.ID, &e.Marker, &e.Hosts, &e.KeyType, &e.KeyData, &e.Comment); err != nil {
			return KnownHost{}, false, err
		}
		return e, false, nil
	}
	id, _ := res.LastInsertId()
	kh.ID = id
	return kh, true, nil
}

// DeleteKnownHost removes a managed entry (does not touch ~/.ssh/known_hosts).
func (s *Store) DeleteKnownHost(id int64) error {
	_, err := s.db.Exec(`DELETE FROM known_hosts WHERE id=?`, id)
	return err
}

// ---- Settings ----

// GetSetting returns a setting value or def if unset.
func (s *Store) GetSetting(key, def string) string {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err != nil {
		return def
	}
	return v
}

// SetSetting upserts a setting value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// AllSettings returns every setting as a map.
func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ---- Backup / restore / reset ----

// KeyExport is a key record plus its private-key file bytes, for portable backups.
type KeyExport struct {
	Key
	PrivateKeyData []byte `json:"private_key_data"`
}

// Snapshot is a complete, portable dump of the store: everything needed to
// reconstruct the inventory, keys and settings on another machine. Host.Password
// is populated (backups are encrypted). The master-password hash is excluded.
type Snapshot struct {
	Version  int               `json:"version"`
	Groups   []Group           `json:"groups"`
	Hosts    []Host            `json:"hosts"`
	Keys       []KeyExport       `json:"keys"`
	HostKeys   [][2]int64        `json:"host_keys"` // {key_id, host_id}
	KnownHosts []KnownHost       `json:"known_hosts"`
	Settings   map[string]string `json:"settings"`
}

// ExportAll builds a Snapshot of the whole store. Key private-key file bytes are
// NOT read here (PrivateKeyData is left empty) — the caller fills them from disk.
func (s *Store) ExportAll() (*Snapshot, error) {
	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	hosts, err := s.ListHosts()
	if err != nil {
		return nil, err
	}
	for i := range hosts {
		hosts[i].Password = s.GetHostPassword(hosts[i].ID)
	}
	keyList, err := s.ListKeys()
	if err != nil {
		return nil, err
	}
	keys := make([]KeyExport, len(keyList))
	var hk [][2]int64
	for i, k := range keyList {
		keys[i] = KeyExport{Key: k}
		for _, hid := range k.DeployedHosts {
			hk = append(hk, [2]int64{k.ID, hid})
		}
	}
	knownHosts, err := s.ListKnownHosts()
	if err != nil {
		return nil, err
	}
	settings, err := s.AllSettings()
	if err != nil {
		return nil, err
	}
	// The master-password hash IS included: a backup captures the password so a
	// restore reproduces the exact state (the file is already encrypted with it).
	return &Snapshot{
		Version:    1,
		Groups:     groups,
		Hosts:      hosts,
		Keys:       keys,
		HostKeys:   hk,
		KnownHosts: knownHosts,
		Settings:   settings,
	}, nil
}

// ImportAll replaces the entire store with snap, in a single transaction. Rows
// are inserted with their original IDs (so group/host/key relationships are
// preserved); foreign-key checks are deferred to commit so insertion order does
// not matter. All settings — including the master-password hash — are restored
// verbatim from the snapshot. Callers must have already written key files to
// disk and set each KeyExport.PrivatePath before calling.
func (s *Store) ImportAll(snap *Snapshot) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`PRAGMA defer_foreign_keys=ON`); err != nil {
		return err
	}
	for _, t := range []string{"host_tags", "host_keys", "hosts", "groups", "tags", "keys", "known_hosts", "settings"} {
		if _, err := tx.Exec(`DELETE FROM ` + t); err != nil {
			return err
		}
	}

	for _, g := range snap.Groups {
		if _, err := tx.Exec(`INSERT INTO groups (id, name, parent_id, sort) VALUES (?,?,?,?)`,
			g.ID, g.Name, g.ParentID, g.Sort); err != nil {
			return err
		}
	}

	// Rebuild tags fresh (IDs need not be preserved — only host_tags references
	// them, and we rebuild those too from each host's tag names).
	tagID := map[string]int64{}
	ensureTag := func(name string) (int64, error) {
		if id, ok := tagID[name]; ok {
			return id, nil
		}
		res, err := tx.Exec(`INSERT INTO tags (name) VALUES (?)`, name)
		if err != nil {
			return 0, err
		}
		id, _ := res.LastInsertId()
		tagID[name] = id
		return id, nil
	}

	for _, h := range snap.Hosts {
		extra, _ := json.Marshal(h.ExtraOptions)
		if h.Port == 0 {
			h.Port = 22
		}
		if _, err := tx.Exec(`INSERT INTO hosts (id, alias, hostname, user, port, identity_file, proxy_jump, extra_options, group_id, notes, password, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			h.ID, h.Alias, h.Hostname, h.User, h.Port, h.IdentityFile, h.ProxyJump, string(extra), h.GroupID, h.Notes, h.Password, h.CreatedAt, h.UpdatedAt); err != nil {
			return err
		}
		for _, name := range h.Tags {
			if name == "" {
				continue
			}
			tid, err := ensureTag(name)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO host_tags (host_id, tag_id) VALUES (?,?)`, h.ID, tid); err != nil {
				return err
			}
		}
	}

	for _, k := range snap.Keys {
		hp := 0
		if k.HasPassphrase {
			hp = 1
		}
		if _, err := tx.Exec(`INSERT INTO keys (id, name, type, public_key, private_path, has_passphrase) VALUES (?,?,?,?,?,?)`,
			k.ID, k.Name, k.Type, k.PublicKey, k.PrivatePath, hp); err != nil {
			return err
		}
	}

	for _, pair := range snap.HostKeys {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO host_keys (key_id, host_id) VALUES (?,?)`, pair[0], pair[1]); err != nil {
			return err
		}
	}

	for _, kh := range snap.KnownHosts {
		if _, err := tx.Exec(`INSERT INTO known_hosts (id, marker, hosts, key_type, key_data, comment) VALUES (?,?,?,?,?,?)`,
			kh.ID, kh.Marker, kh.Hosts, kh.KeyType, kh.KeyData, kh.Comment); err != nil {
			return err
		}
	}

	for key, val := range snap.Settings {
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?,?)`, key, val); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ResetAll wipes every table (inventory, keys and settings, including the master
// password), returning the store to a fresh state.
func (s *Store) ResetAll() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys=ON`); err != nil {
		return err
	}
	for _, t := range []string{"host_tags", "host_keys", "hosts", "groups", "tags", "keys", "known_hosts", "settings"} {
		if _, err := tx.Exec(`DELETE FROM ` + t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Ping verifies the DB is reachable.
func (s *Store) Ping() error {
	if err := s.db.Ping(); err != nil {
		return fmt.Errorf("db ping: %w", err)
	}
	return nil
}
