package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/atomicfile"
)

// SessionsFileName is the persisted session table inside the data directory.
const SessionsFileName = "sessions.json"

// Session lifetime defaults. A home appliance is opened a few times a month,
// so a week of idleness and a month overall keep the owner logged in across
// normal use while still expiring a forgotten browser tab.
const (
	DefaultSessionMaxLifetime = 30 * 24 * time.Hour
	DefaultSessionIdleTimeout = 7 * 24 * time.Hour
	// sessionTouchInterval limits how often a session's last-seen update is
	// flushed to the SD card.
	sessionTouchInterval = 5 * time.Minute
	sessionIDBytes       = 32
)

// Session is one logged-in browser.
type Session struct {
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// SessionsOptions tunes a Sessions store. Zero values take the defaults.
type SessionsOptions struct {
	MaxLifetime time.Duration
	IdleTimeout time.Duration
	Now         func() time.Time
}

// Sessions is the server-side session table. The cookie carries a random id;
// the table is keyed by its SHA-256 so the persisted file never holds a
// usable credential.
type Sessions struct {
	mu       sync.Mutex
	path     string
	opts     SessionsOptions
	byHash   map[string]*Session
	lastSave time.Time
}

type sessionsFile struct {
	Sessions map[string]*Session `json:"sessions"`
}

// NewSessions points a store at <dataDir>/sessions.json. Call Load before use.
func NewSessions(dataDir string, opts SessionsOptions) *Sessions {
	if opts.MaxLifetime <= 0 {
		opts.MaxLifetime = DefaultSessionMaxLifetime
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = DefaultSessionIdleTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Sessions{
		path:   filepath.Join(dataDir, SessionsFileName),
		opts:   opts,
		byHash: map[string]*Session{},
	}
}

// Load restores persisted sessions, dropping any that have expired. A missing
// or unreadable file simply starts empty; sessions are not worth failing
// startup over.
func (s *Sessions) Load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f sessionsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("auth: parse %s: %w", s.path, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash = map[string]*Session{}
	for k, v := range f.Sessions {
		if v != nil && !s.expired(v) {
			s.byHash[k] = v
		}
	}
	return nil
}

// Create opens a session and returns the id to put in the cookie.
func (s *Sessions) Create(username string) (string, error) {
	raw := make([]byte, sessionIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: session id: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	now := s.opts.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.byHash[hashID(id)] = &Session{Username: username, CreatedAt: now, LastSeen: now}
	return id, s.saveLocked()
}

// Get validates an id, refreshes its idle timer and returns the session.
func (s *Sessions) Get(id string) (Session, bool) {
	if id == "" {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byHash[hashID(id)]
	if !ok {
		return Session{}, false
	}
	if s.expired(sess) {
		delete(s.byHash, hashID(id))
		s.saveOrLog()
		return Session{}, false
	}
	now := s.opts.Now()
	if now.Sub(sess.LastSeen) >= sessionTouchInterval {
		sess.LastSeen = now
		s.saveOrLog()
	}
	return *sess, true
}

// Delete ends one session (logout).
func (s *Sessions) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byHash[hashID(id)]; !ok {
		return nil
	}
	delete(s.byHash, hashID(id))
	return s.saveLocked()
}

// DeleteAll ends every session, for example after a password change.
func (s *Sessions) DeleteAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash = map[string]*Session{}
	return s.saveLocked()
}

// Prune drops expired sessions and reports how many were removed.
func (s *Sessions) Prune() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.pruneLocked()
	if n > 0 {
		s.saveOrLog()
	}
	return n
}

// Count returns the number of live sessions.
func (s *Sessions) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byHash)
}

func (s *Sessions) expired(sess *Session) bool {
	now := s.opts.Now()
	return now.Sub(sess.CreatedAt) > s.opts.MaxLifetime || now.Sub(sess.LastSeen) > s.opts.IdleTimeout
}

func (s *Sessions) pruneLocked() int {
	n := 0
	for k, v := range s.byHash {
		if s.expired(v) {
			delete(s.byHash, k)
			n++
		}
	}
	return n
}

// saveOrLog persists the table on paths that cannot return an error to the
// caller (Get, Prune). A failed save only loses idle-timer updates or an
// expiry on disk; the in-memory table stays authoritative, so log and go on.
func (s *Sessions) saveOrLog() {
	if err := s.saveLocked(); err != nil {
		slog.Warn("auth: could not persist sessions", "err", err)
	}
}

func (s *Sessions) saveLocked() error {
	data, err := json.MarshalIndent(sessionsFile{Sessions: s.byHash}, "", "  ")
	if err != nil {
		return err
	}
	s.lastSave = s.opts.Now()
	return atomicfile.WriteFile(s.path, append(data, '\n'), 0o600)
}

func hashID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}
