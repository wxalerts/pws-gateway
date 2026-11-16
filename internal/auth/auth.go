// Package auth implements the local administrator account, browser sessions
// and login throttling for the pwsgw web UI.
//
// There is exactly one account. Its password is stored as an argon2id hash in
// <data-dir>/auth.json together with the parameters used, so the parameters
// can be raised in a later release without invalidating existing accounts.
//
// Password policy: at least 12 characters (counted as Unicode code points),
// at most 1024 bytes, and nothing else. There are no composition rules
// (digits, symbols, mixed case) because NIST SP 800-63B found they push users
// toward predictable patterns while adding little entropy; length is what
// matters, and the login rate limiter closes off online guessing.
//
// Sessions live in memory and are mirrored to <data-dir>/sessions.json so a
// daemon restart or update does not log every browser out. Login attempts are
// throttled per source IP with an exponential lockout. Every outcome is
// written to the audit log through Auditor.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/wxalerts/pws-gateway/internal/atomicfile"
)

// Password policy limits.
const (
	MinPasswordLength = 12
	MaxPasswordBytes  = 1024
	MaxUsernameLength = 64
)

// FileName is the account file inside the data directory.
const FileName = "auth.json"

// Errors returned by this package.
var (
	ErrNoAccount       = errors.New("auth: no account exists")
	ErrAccountExists   = errors.New("auth: an account already exists")
	ErrPasswordShort   = fmt.Errorf("auth: password must be at least %d characters", MinPasswordLength)
	ErrPasswordLong    = fmt.Errorf("auth: password must be at most %d bytes", MaxPasswordBytes)
	ErrPasswordInvalid = errors.New("auth: password is not valid UTF-8")
	ErrUsernameInvalid = errors.New("auth: username must be 1 to 64 characters of letters, digits, '.', '_', '-' or '@'")
)

// Params are the argon2id cost parameters.
type Params struct {
	Time      uint32
	MemoryKiB uint32
	Threads   uint8
	KeyLen    uint32
	SaltLen   int
}

// DefaultParams are tuned for the Pi 3B+ floor: 64 MiB and three passes take
// well under a second there, and the Store only runs one hash at a time so
// the memory cost is bounded whatever the login rate. This matches the OWASP
// "first recommended" argon2id configuration except for the lane count.
var DefaultParams = Params{Time: 3, MemoryKiB: 64 * 1024, Threads: 2, KeyLen: 32, SaltLen: 16}

// TestParams are deliberately weak so unit tests stay fast. Never use them in
// production code.
var TestParams = Params{Time: 1, MemoryKiB: 8 * 1024, Threads: 1, KeyLen: 32, SaltLen: 16}

// Hash is a stored password hash with the parameters that produced it.
type Hash struct {
	Algorithm string `json:"alg"`
	Version   int    `json:"v"`
	Time      uint32 `json:"t"`
	MemoryKiB uint32 `json:"m"`
	Threads   uint8  `json:"p"`
	Salt      []byte `json:"salt"`
	Hash      []byte `json:"hash"`
}

// Account is the on-disk record.
type Account struct {
	Username  string    `json:"username"`
	Hash      Hash      `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var reUsername = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// CheckUsername enforces the username shape.
func CheckUsername(u string) error {
	if !reUsername.MatchString(u) {
		return ErrUsernameInvalid
	}
	return nil
}

// CheckPassword enforces the password policy described in the package doc.
func CheckPassword(pw string) error {
	if !utf8.ValidString(pw) {
		return ErrPasswordInvalid
	}
	if len(pw) > MaxPasswordBytes {
		return ErrPasswordLong
	}
	if utf8.RuneCountInString(pw) < MinPasswordLength {
		return ErrPasswordShort
	}
	return nil
}

// HashPassword derives an argon2id hash with a fresh random salt. It does not
// apply the policy; callers run CheckPassword first.
func HashPassword(pw string, p Params) (Hash, error) {
	if p.Time == 0 || p.MemoryKiB == 0 || p.Threads == 0 || p.KeyLen == 0 || p.SaltLen <= 0 {
		return Hash{}, errors.New("auth: invalid argon2id parameters")
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return Hash{}, fmt.Errorf("auth: salt: %w", err)
	}
	return Hash{
		Algorithm: "argon2id",
		Version:   argon2.Version,
		Time:      p.Time,
		MemoryKiB: p.MemoryKiB,
		Threads:   p.Threads,
		Salt:      salt,
		Hash:      argon2.IDKey([]byte(pw), salt, p.Time, p.MemoryKiB, p.Threads, p.KeyLen),
	}, nil
}

// maxHashLen bounds a stored hash so its length converts safely to uint32.
const maxHashLen = 1 << 20

// Verify recomputes the hash with the stored parameters and compares in
// constant time.
func (h Hash) Verify(pw string) bool {
	if h.Algorithm != "argon2id" || len(h.Hash) == 0 || len(h.Salt) == 0 {
		return false
	}
	// Stored hashes are 32 bytes; reject anything that could not fit a uint32 key length.
	if len(h.Hash) > maxHashLen {
		return false
	}
	keyLen := uint32(len(h.Hash)) //nolint:gosec // bounded by the maxHashLen check above
	got := argon2.IDKey([]byte(pw), h.Salt, h.Time, h.MemoryKiB, h.Threads, keyLen)
	return subtle.ConstantTimeCompare(got, h.Hash) == 1
}

// PHC renders the hash in the PHC string format other tools understand:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash> with unpadded base64.
func (h Hash) PHC() string {
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		h.Algorithm, h.Version, h.MemoryKiB, h.Time, h.Threads,
		enc.EncodeToString(h.Salt), enc.EncodeToString(h.Hash))
}

// Store holds the account and serializes hashing.
type Store struct {
	mu      sync.RWMutex
	path    string
	params  Params
	account *Account
	// hashSem allows one argon2 computation at a time so an attacker cannot
	// multiply the 64 MiB working set by opening parallel logins.
	hashSem chan struct{}
	// decoy is hashed against when no account exists or the username is
	// wrong, so timing does not reveal whether a username is valid.
	decoy Hash
	now   func() time.Time
}

// Option configures NewStore.
type Option func(*Store)

// WithParams overrides the argon2id parameters used for new hashes.
func WithParams(p Params) Option { return func(s *Store) { s.params = p } }

// WithClock overrides the clock used for timestamps.
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// NewStore points a store at <dataDir>/auth.json. Call Load before use.
func NewStore(dataDir string, opts ...Option) (*Store, error) {
	s := &Store{
		path:    filepath.Join(dataDir, FileName),
		params:  DefaultParams,
		hashSem: make(chan struct{}, 1),
		now:     time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	decoy, err := HashPassword("decoy-"+time.Now().String(), s.params)
	if err != nil {
		return nil, err
	}
	s.decoy = decoy
	return s, nil
}

// Path returns the account file path.
func (s *Store) Path() string { return s.path }

// Load reads the account file. A missing file is not an error: the UI then
// shows the first-run setup page.
func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var acct Account
	if err := json.Unmarshal(data, &acct); err != nil {
		return fmt.Errorf("auth: parse %s: %w", s.path, err)
	}
	if acct.Username == "" || acct.Hash.Algorithm == "" {
		return fmt.Errorf("auth: %s is incomplete", s.path)
	}
	s.mu.Lock()
	s.account = &acct
	s.mu.Unlock()
	return nil
}

// Exists reports whether an account has been created.
func (s *Store) Exists() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.account != nil
}

// Username returns the account name, or "" when none exists.
func (s *Store) Username() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.account == nil {
		return ""
	}
	return s.account.Username
}

// Create makes the first account. It fails with ErrAccountExists once one is
// present; use SetPassword to change credentials afterwards.
func (s *Store) Create(username, password string) error {
	if s.Exists() {
		return ErrAccountExists
	}
	return s.write(username, password, true)
}

// SetPassword creates or replaces the account after applying the policy.
func (s *Store) SetPassword(username, password string) error {
	return s.write(username, password, false)
}

// write validates, hashes outside the lock (hashing is slow by design), then
// swaps the record in. With createOnly the presence check is repeated under
// the lock so two concurrent first-run submissions cannot both succeed.
func (s *Store) write(username, password string, createOnly bool) error {
	if err := CheckUsername(username); err != nil {
		return err
	}
	if err := CheckPassword(password); err != nil {
		return err
	}
	h, err := s.hash(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if createOnly && s.account != nil {
		return ErrAccountExists
	}
	now := s.now().UTC()
	acct := Account{Username: username, Hash: h, CreatedAt: now, UpdatedAt: now}
	if s.account != nil {
		acct.CreatedAt = s.account.CreatedAt
	}
	data, err := json.MarshalIndent(acct, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(s.path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	s.account = &acct
	return nil
}

// Verify checks a username and password pair. It always performs one argon2
// computation so a wrong username costs the same as a wrong password.
func (s *Store) Verify(username, password string) bool {
	s.mu.RLock()
	acct := s.account
	s.mu.RUnlock()

	target := s.decoy
	nameOK := 0
	if acct != nil {
		nameOK = subtle.ConstantTimeCompare([]byte(acct.Username), []byte(username))
		if nameOK == 1 {
			target = acct.Hash
		}
	}
	if len(password) > MaxPasswordBytes {
		// Refuse to hash oversized input, but do not reveal that we skipped it
		// by returning early without the usual cost: hash a truncated decoy.
		password = password[:MaxPasswordBytes]
		target = s.decoy
		nameOK = 0
	}

	s.hashSem <- struct{}{}
	ok := target.Verify(password)
	<-s.hashSem

	return ok && nameOK == 1
}

func (s *Store) hash(password string) (Hash, error) {
	s.hashSem <- struct{}{}
	defer func() { <-s.hashSem }()
	return HashPassword(password, s.params)
}
