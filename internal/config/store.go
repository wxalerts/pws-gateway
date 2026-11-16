package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wxalerts/pws-gateway/internal/atomicfile"
)

// Errors returned by Store.
var (
	ErrUnknownSection   = errors.New("config: unknown section")
	ErrCommitInProgress = errors.New("config: a network change is awaiting confirmation")
)

// filePerm keeps the file readable by the pwsgw group (for support tooling)
// but not by others. It contains no secrets, only topology.
const filePerm = 0o640

// Store is the in-memory copy of config.json plus the lock that serialises
// writers. Readers get a deep copy so they can never mutate shared state.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// NewStore points a store at <dataDir>/config.json. Call Load before use.
func NewStore(dataDir string) *Store {
	return &Store{path: filepath.Join(dataDir, FileName), cfg: Defaults()}
}

// Path returns the file the store reads and writes.
func (s *Store) Path() string { return s.path }

// Load reads, migrates and validates the file. A missing file yields the
// defaults and created=true so the caller can decide whether to write them.
func (s *Store) Load() (created bool, err error) {
	cfg, err := Load(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.mu.Lock()
		s.cfg = Defaults()
		s.mu.Unlock()
		return true, nil
	}
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return false, nil
}

// Get returns a deep copy of the current configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Clone()
}

// Save validates and writes the current in-memory configuration.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Save(s.path, s.cfg)
}

// Update applies fn to a copy, validates the result, writes it, and only then
// replaces the in-memory copy. A failing fn or validation leaves the store
// untouched.
func (s *Store) Update(fn func(*Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg.Clone()
	if err := fn(&next); err != nil {
		return Config{}, err
	}
	if err := Save(s.path, next); err != nil {
		return Config{}, err
	}
	s.cfg = next
	return next.Clone(), nil
}

// Apply replaces one section from raw JSON (the body of PUT
// /api/config/{section}). Unknown fields are rejected so a typo in the UI
// cannot silently drop a setting. The network section only accepts the
// pending candidate; the active settings change through the commit-confirm
// flow, never by direct write.
func (s *Store) Apply(section string, raw json.RawMessage) (Config, error) {
	return s.Update(func(c *Config) error {
		return c.applySection(section, raw)
	})
}

func (c *Config) applySection(section string, raw json.RawMessage) error {
	var target any
	switch section {
	case "web":
		target = &c.Web
	case "radios":
		c.Radios = nil
		target = &c.Radios
	case "stations":
		c.Stations = nil
		target = &c.Stations
	case "uplink":
		target = &c.Uplink
	case "history":
		target = &c.History
	case "network":
		if c.Network.ConfirmDeadline != nil {
			return ErrCommitInProgress
		}
		var in struct {
			Pending *NetworkSettings `json:"pending"`
		}
		if err := decodeStrict(raw, &in); err != nil {
			return err
		}
		c.Network.Pending = in.Pending
		return c.Validate()
	case "updates":
		target = &c.Updates
	case "homeassistant":
		target = &c.HomeAssistant
	default:
		return fmt.Errorf("%w: %q", ErrUnknownSection, section)
	}
	if err := decodeStrict(raw, target); err != nil {
		return err
	}
	if c.Radios == nil {
		c.Radios = []Radio{}
	}
	if c.Stations == nil {
		c.Stations = map[string]Station{}
	}
	return c.Validate()
}

// DecodeError wraps a JSON shape problem so the API can answer 400.
type DecodeError struct{ Err error }

func (e *DecodeError) Error() string { return "config: invalid JSON: " + e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &DecodeError{Err: err}
	}
	if dec.More() {
		return &DecodeError{Err: errors.New("trailing data after JSON value")}
	}
	return nil
}

// Load reads one file through the migration chain and validates it.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

// Parse decodes config bytes, migrating older schema versions. Sections that
// are missing from the file keep their defaults, which is how new sections
// are introduced without a migration.
func Parse(data []byte) (Config, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Config{}, &DecodeError{Err: err}
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	if err := migrate(raw); err != nil {
		return Config{}, err
	}
	merged, err := json.Marshal(raw)
	if err != nil {
		return Config{}, err
	}
	cfg := Defaults()
	if err := decodeStrict(merged, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Radios == nil {
		cfg.Radios = []Radio{}
	}
	if cfg.Stations == nil {
		cfg.Stations = map[string]Station{}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save validates cfg and writes it atomically.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.WriteFile(path, data, filePerm)
}

// Clone returns a deep copy.
func (c Config) Clone() Config {
	out := c
	out.Radios = append([]Radio(nil), c.Radios...)
	if out.Radios == nil {
		out.Radios = []Radio{}
	}
	out.Stations = make(map[string]Station, len(c.Stations))
	for k, v := range c.Stations {
		out.Stations[k] = v
	}
	out.Network = c.Network.clone()
	return out
}

func (n Network) clone() Network {
	out := n
	out.Current = n.Current.clone()
	if n.Pending != nil {
		p := n.Pending.clone()
		out.Pending = &p
	}
	if n.ConfirmDeadline != nil {
		d := *n.ConfirmDeadline
		out.ConfirmDeadline = &d
	}
	return out
}

func (n NetworkSettings) clone() NetworkSettings {
	out := n
	out.DNS = append([]string(nil), n.DNS...)
	return out
}

// migration upgrades the raw document from version v to v+1. Working on the
// raw map means a migration can rename or move keys that the current Config
// type no longer has.
type migration func(raw map[string]json.RawMessage) error

// migrations is keyed by the version a migration starts from.
var migrations = map[int]migration{
	0: migrateV0ToV1,
}

// migrateV0ToV1 handles files written before schema_version existed. Nothing
// else changed between the two layouts, so this only exists to show the shape
// future migrations take.
func migrateV0ToV1(map[string]json.RawMessage) error {
	return nil
}

func migrate(raw map[string]json.RawMessage) error {
	v := 0
	if sv, ok := raw["schema_version"]; ok {
		if err := json.Unmarshal(sv, &v); err != nil {
			return &DecodeError{Err: fmt.Errorf("schema_version: %w", err)}
		}
	}
	if v > SchemaVersion {
		return fmt.Errorf("config: schema_version %d is newer than this build supports (%d)", v, SchemaVersion)
	}
	for v < SchemaVersion {
		m, ok := migrations[v]
		if !ok {
			return fmt.Errorf("config: no migration from schema_version %d", v)
		}
		if err := m(raw); err != nil {
			return fmt.Errorf("config: migrate %d to %d: %w", v, v+1, err)
		}
		v++
	}
	sv, _ := json.Marshal(v)
	raw["schema_version"] = sv
	return nil
}
