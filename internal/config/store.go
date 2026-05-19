// Package config owns the on-disk state for the dashboard:
//
//	/config/.vault/key       — 32-byte master key (chmod 600)
//	/config/config.enc       — encrypted JSON: credentials, trackers, Prowlarr
//	/config/netacl.json      — PLAINTEXT IP allowlist (must apply on /login itself)
//	/config/metadata.json    — KDF/AEAD versioning + creation timestamp
//	/config/.initialized     — atomic marker file (created last)
//
// The encryption model is in crypto.go: argon2id(password, salt=master_key)
// derives the key; ChaCha20-Poly1305 seals the config blob; an auth-tag
// failure on Unlock IS the wrong-password signal (no separate hash).
//
// Boot sequence:
//  1. NewStore loads (or creates) the master key.
//  2. Caller checks IsInitialized:
//     false → run Init(password, username, initialCIDR) on user setup
//     true  → run Unlock(password) on user login
//  3. After Unlock, Get/Save operate on the in-memory decrypted Config.
//  4. Lock() on logout zeroes the derived key.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// On-disk filenames (under /config/).
const (
	configFilename     = "config.enc"
	metadataFilename   = "metadata.json"
	netaclFilename     = "netacl.json"
	initMarkerFilename = ".initialized"

	currentMetaVersion = 1
	currentMetaKDF     = "argon2id"
	currentMetaAEAD    = "chacha20poly1305"
)

// Config holds the dashboard's sensitive fields — encrypted at rest in
// config.enc. Includes the username (cosmetic, for future use) and the
// per-tracker credentials. The plaintext NetACL is intentionally NOT
// here; see the package doc for the reason.
type Config struct {
	Username string `json:"username"`

	ProwlarrURL     string `json:"prowlarr_url"`
	ProwlarrAPIKey  string `json:"prowlarr_api_key"`
	ProwlarrEnabled bool   `json:"prowlarr_enabled"`

	AutobrrURL     string `json:"autobrr_url,omitempty"`
	AutobrrAPIKey  string `json:"autobrr_api_key,omitempty"`
	AutobrrEnabled bool   `json:"autobrr_enabled,omitempty"`

	Trackers []*TrackerEntry `json:"trackers"`
}

// NetACL holds the IP allowlist + reverse-proxy settings. Stored
// plaintext so the request middleware can enforce the allowlist before
// any password has been supplied.
//
// Confirmed flips to true on the first successful SaveNetACL. Until then
// the auth-group middleware bounces every request back to the network
// page so a fresh install can't be left in the wide-open default state
// after setup.
type NetACL struct {
	AllowedCIDRs []string `json:"allowed_cidrs"`
	ProxyHost    string   `json:"proxy_host,omitempty"`
	Confirmed    bool     `json:"confirmed,omitempty"`
}

// TrackerEntry is one managed tracker. DefinitionName matches a Prowlarr
// indexer schema; TrackerType identifies the tracker software (e.g.
// "unit3d"); Name is what the user sees (defaults to definition name).
type TrackerEntry struct {
	DefinitionName string     `json:"definition_name"`
	TrackerType    string     `json:"tracker_type,omitempty"` // trackertype.Type.ID(); defaults to "unit3d"
	Name           string     `json:"name"`
	TrackerURL     string     `json:"tracker_url"`
	APIKey         string     `json:"api_key"`
	Username       string     `json:"username"`
	ProwlarrID     int        `json:"prowlarr_id"`          // 0 if not in Prowlarr
	Enabled        bool       `json:"enabled"`              // mirrors Prowlarr's enable state
	LastSync       *time.Time `json:"last_sync,omitempty"`  // most recent stats fetch attempt
	UserStats      *UserStats `json:"user_stats,omitempty"` // most recent stats fetch success
	SyncError      string     `json:"sync_error,omitempty"` // error from most recent attempt

	// ProwlarrSettings is the full desired schema-field config we manage in
	// Prowlarr, keyed by field name. Root indexer config that Prowlarr keeps
	// outside fields (name, app profile, tags) is stored separately below.
	ProwlarrSettings     map[string]string `json:"prowlarr_settings,omitempty"`
	ProwlarrName         string            `json:"prowlarr_name,omitempty"`
	ProwlarrAppProfileID int               `json:"prowlarr_app_profile_id,omitempty"`
	ProwlarrTags         []int             `json:"prowlarr_tags,omitempty"`
	ProwlarrLastSync     *time.Time        `json:"prowlarr_last_sync,omitempty"`
	ProwlarrSyncError    string            `json:"prowlarr_sync_error,omitempty"`

	// Autobrr integration — parallel structure to Prowlarr above. AutobrrID
	// is the indexer ID inside Autobrr; AutobrrIdentifier is the autobrr
	// definition slug ("alpharatio", etc.) used to match the per-tracker
	// IRC network at render time. AutobrrEnabled mirrors Autobrr's enable
	// state on its indexer record.
	AutobrrID         int    `json:"autobrr_id,omitempty"`
	AutobrrIdentifier string `json:"autobrr_identifier,omitempty"`
	AutobrrEnabled    bool   `json:"autobrr_enabled,omitempty"`

	// AutobrrSettings is the full desired config we manage in Autobrr,
	// keyed by field name (matches the autobrr YAML def setting names).
	// Same write/read/security contract as ProwlarrSettings above.
	AutobrrSettings  map[string]string `json:"autobrr_settings,omitempty"`
	AutobrrLastSync  *time.Time        `json:"autobrr_last_sync,omitempty"`
	AutobrrSyncError string            `json:"autobrr_sync_error,omitempty"`

	// Branding — best-effort scrape of the tracker's landing page.
	// Sticky once obtained; retried on each refresh until found.
	FaviconDataURI string `json:"favicon_data_uri,omitempty"`
	ThemeColor     string `json:"theme_color,omitempty"`
}

// UserStats mirrors what UNIT3D returns from GET /api/user. The values
// are pre-formatted strings (e.g. "1.23 TiB", "2.71") — UNIT3D does the
// formatting, we don't reparse.
type UserStats struct {
	Username   string `json:"username"`
	Group      string `json:"group"`
	Uploaded   string `json:"uploaded"`
	Downloaded string `json:"downloaded"`
	Ratio      string `json:"ratio"`
	Buffer     string `json:"buffer"`
	SeedBonus  string `json:"seedbonus"`
	Seeding    int    `json:"seeding"`
	Leeching   int    `json:"leeching"`
	HitAndRuns int    `json:"hit_and_runs"`
}

// metadata is the (plaintext) sidecar describing how config.enc was
// produced. Future format changes bump Version; today's loader just
// validates the current value.
type metadata struct {
	Version   int    `json:"version"`
	KDF       string `json:"kdf"`
	AEAD      string `json:"aead"`
	CreatedAt string `json:"created_at"`
}

// Store owns the master key, the in-memory derived key, the decrypted
// Config, and the plaintext NetACL. It is the only thing that touches
// the on-disk files.
type Store struct {
	mu         sync.RWMutex
	dir        string
	masterKey  []byte
	derivedKey []byte // nil when locked
	config     *Config
	netacl     *NetACL
	unlocked   bool
}

// Sentinel errors. Callers use errors.Is to distinguish.
var (
	ErrNotInitialized = errors.New("not initialized")
	ErrLocked         = errors.New("store is locked")
	ErrAlreadyInit    = errors.New("already initialized")
	ErrBadPassword    = errBadPassword // re-export from crypto.go for handler consumers
)

// NewStore loads or generates the master key and reads the plaintext
// netacl.json if it exists. It does NOT decrypt the config — that
// requires the user's password (Unlock) or an Init call.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	key, err := loadOrCreateMasterKey(dir)
	if err != nil {
		return nil, fmt.Errorf("master key: %w", err)
	}
	s := &Store{
		dir:       dir,
		masterKey: key,
		config:    &Config{},
		netacl:    &NetACL{},
	}
	// Best-effort: netacl.json doesn't exist before init. We ignore
	// any error so the bootstrap path doesn't crash on first boot.
	if data, err := os.ReadFile(filepath.Join(dir, netaclFilename)); err == nil {
		_ = json.Unmarshal(data, s.netacl)
	}
	return s, nil
}

// IsInitialized reports whether /config/.initialized exists, which is
// the single source of truth for "setup has completed".
func (s *Store) IsInitialized() bool {
	_, err := os.Stat(filepath.Join(s.dir, initMarkerFilename))
	return err == nil
}

// IsUnlocked reports whether the in-memory derived key is present
// (i.e. a successful Init or Unlock has happened in this process).
func (s *Store) IsUnlocked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unlocked
}

// GetNetACL returns a defensive copy of the live NetACL so callers
// can read/mutate freely without aliasing the stored slice.
func (s *Store) GetNetACL() NetACL {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := *s.netacl
	cp.AllowedCIDRs = append([]string(nil), s.netacl.AllowedCIDRs...)
	return cp
}

// SaveNetACL atomically persists the plaintext NetACL. The atomicity
// matters: a half-written netacl.json on next boot could lock the
// operator out. Always sets Confirmed=true — saving IS the confirmation.
func (s *Store) SaveNetACL(n *NetACL) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n.Confirmed = true
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal netacl: %w", err)
	}
	if err := writeAtomic(filepath.Join(s.dir, netaclFilename), data, 0600); err != nil {
		return fmt.Errorf("write netacl: %w", err)
	}
	s.netacl = n
	return nil
}

// Init performs first-boot setup atomically:
//
//  1. Derive the encryption key from password+master_key.
//  2. Encrypt an empty Config (just the username).
//  3. Write metadata.json + netacl.json (with initialCIDR seeded).
//  4. Write the .initialized marker LAST — its presence is what
//     IsInitialized() probes, so a crash before it lands leaves the
//     store recoverable (re-running Init will succeed).
//
// On success the store is left unlocked: the caller can immediately
// start a session.
func (s *Store) Init(password, username, initialCIDR string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(filepath.Join(s.dir, initMarkerFilename)); err == nil {
		return ErrAlreadyInit
	}

	derived := deriveKey(password, s.masterKey)

	initial := &Config{Username: username}
	plaintext, err := json.Marshal(initial)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	blob, err := seal(derived, plaintext)
	if err != nil {
		return fmt.Errorf("seal config: %w", err)
	}

	meta := metadata{
		Version:   currentMetaVersion,
		KDF:       currentMetaKDF,
		AEAD:      currentMetaAEAD,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	netacl := &NetACL{}
	if initialCIDR != "" {
		netacl.AllowedCIDRs = []string{initialCIDR}
	}
	netaclBytes, err := json.MarshalIndent(netacl, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal netacl: %w", err)
	}

	// Order matters: every supporting file before the marker.
	if err := writeAtomic(filepath.Join(s.dir, configFilename), blob, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := writeAtomic(filepath.Join(s.dir, metadataFilename), metaBytes, 0600); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	if err := writeAtomic(filepath.Join(s.dir, netaclFilename), netaclBytes, 0600); err != nil {
		return fmt.Errorf("write netacl: %w", err)
	}
	if err := writeAtomic(filepath.Join(s.dir, initMarkerFilename), []byte("ok\n"), 0600); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}
	// Tighten directory perms now that all files exist. Best-effort.
	_ = os.Chmod(s.dir, 0700)

	s.derivedKey = derived
	s.config = initial
	s.netacl = netacl
	s.unlocked = true
	return nil
}

// Unlock decrypts config.enc with the supplied password. An auth-tag
// failure (wrong password OR corrupted file) returns ErrBadPassword;
// the handler distinguishes via errors.Is.
//
// On any error the partially-derived key bytes are wiped before
// returning — there's no point keeping junk material around.
func (s *Store) Unlock(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(filepath.Join(s.dir, initMarkerFilename)); err != nil {
		return ErrNotInitialized
	}

	enc, err := os.ReadFile(filepath.Join(s.dir, configFilename))
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	derived := deriveKey(password, s.masterKey)
	pt, err := open(derived, enc)
	if err != nil {
		zero(derived)
		return err
	}
	var cfg Config
	if err := json.Unmarshal(pt, &cfg); err != nil {
		zero(derived)
		return fmt.Errorf("unmarshal config: %w", err)
	}

	s.derivedKey = derived
	s.config = &cfg
	s.unlocked = true
	return nil
}

// Lock zeroes the in-memory derived key and clears the cached Config.
// Called on logout, session timeout, or any partial-failure cleanup
// path that took the store past Unlock but couldn't complete the
// session-creation flow.
func (s *Store) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.derivedKey != nil {
		zero(s.derivedKey)
		s.derivedKey = nil
	}
	s.config = &Config{}
	s.unlocked = false
}

// Get returns a copy of the current Config. Returns an empty Config
// when the store is locked — handlers treat empty Config as "logged out"
// per the design rule.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.unlocked {
		return Config{}
	}
	return *s.config
}

// Save re-encrypts and atomically writes config.enc. Refuses to write
// when locked (no derived key available).
func (s *Store) Save(cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.unlocked || s.derivedKey == nil {
		return ErrLocked
	}

	plaintext, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	blob, err := seal(s.derivedKey, plaintext)
	if err != nil {
		return fmt.Errorf("seal: %w", err)
	}
	if err := writeAtomic(filepath.Join(s.dir, configFilename), blob, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	s.config = cfg
	return nil
}

// ApplyBranding atomically patches the favicon and theme-color fields for the
// tracker identified by definitionName. Unlike Save, it holds the write lock
// for the full read-modify-write cycle, so a concurrent user-triggered Save
// cannot race with a background branding goroutine.
func (s *Store) ApplyBranding(definitionName, faviconDataURI, themeColor string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.unlocked || s.derivedKey == nil {
		return false, ErrLocked
	}
	changed := false
	for i, t := range s.config.Trackers {
		if t.DefinitionName != definitionName {
			continue
		}
		if faviconDataURI != "" && s.config.Trackers[i].FaviconDataURI != faviconDataURI {
			s.config.Trackers[i].FaviconDataURI = faviconDataURI
			changed = true
		}
		if themeColor != "" && s.config.Trackers[i].ThemeColor != themeColor {
			s.config.Trackers[i].ThemeColor = themeColor
			changed = true
		}
		break
	}
	if !changed {
		return false, nil
	}
	plaintext, err := json.Marshal(s.config)
	if err != nil {
		return false, fmt.Errorf("marshal: %w", err)
	}
	blob, err := seal(s.derivedKey, plaintext)
	if err != nil {
		return false, fmt.Errorf("seal: %w", err)
	}
	if err := writeAtomic(filepath.Join(s.dir, configFilename), blob, 0600); err != nil {
		return false, fmt.Errorf("write config: %w", err)
	}
	return true, nil
}

// writeAtomic writes data to a temp file in the same directory, fsyncs,
// then renames over the target. On POSIX the rename is atomic — a
// concurrent reader sees either the old file or the new one, never a
// truncated mid-write state.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if rename succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
