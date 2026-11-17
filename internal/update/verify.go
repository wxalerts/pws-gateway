package update

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/wxalerts/pws-gateway/keys"
)

// Rejection codes. Each distinct reason a manifest is refused has its own
// code, shown in the update message error text and in the update_failed event
// detail, so an operator can tell a stale manifest from a forged one.
const (
	CodeBadDelegation  = "delegation_invalid"
	CodeUnknownKey     = "unknown_signing_key"
	CodeKeyRevoked     = "signing_key_revoked"
	CodeKeyExpired     = "signing_key_expired"
	CodeBadManifest    = "manifest_invalid"
	CodeManifestExpiry = "manifest_expired"
	CodeVersionLower   = "version_lower"
)

// Error is a rejection with a stable Code.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string { return e.Code + ": " + e.Err.Error() }

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Err }

func reject(code, format string, a ...any) error {
	return &Error{Code: code, Err: fmt.Errorf(format, a...)}
}

// CodeOf returns the rejection code of err, or "" when err is not a
// rejection.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Trust is the device's pinned trust anchor: the root public keys. Two are
// pinned so a lost or leaked root can be replaced (docs/release-signing.md).
type Trust struct {
	Roots []ed25519.PublicKey
}

// Embedded file names under keys/.
const (
	RootKeyFile       = "root.pub"
	RootBackupKeyFile = "root-backup.pub"
)

// ErrNoRoots means no root key is pinned, so updates are disabled.
var ErrNoRoots = errors.New("update: no root public key is pinned")

// LoadTrust returns the pinned roots: the files embedded from keys/ by the
// release build, otherwise <dataDir>/keys/ (the same bench fallback the
// command-signing key has). It returns ErrNoRoots when neither exists.
func LoadTrust(dataDir string) (Trust, error) {
	var t Trust
	for _, name := range []string{RootKeyFile, RootBackupKeyFile} {
		b, err := keys.FS.ReadFile(name)
		if err != nil {
			b, err = os.ReadFile(filepath.Join(dataDir, "keys", name))
		}
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Trust{}, err
		}
		k, err := ParseKeyFile(b)
		if err != nil {
			return Trust{}, fmt.Errorf("%s: %w", name, err)
		}
		t.Roots = append(t.Roots, k)
	}
	if len(t.Roots) == 0 {
		return Trust{}, ErrNoRoots
	}
	return t, nil
}

// Decision is what the version rules say about a verified manifest.
type Decision int

// Decisions.
const (
	// DecisionNewer: the manifest is for a higher version than installed.
	DecisionNewer Decision = iota
	// DecisionSame: the device already runs this version.
	DecisionSame
	// DecisionRollback: lower than installed, and the signed manifest
	// authorizes this device to take it.
	DecisionRollback
)

// Verify runs the device checks in order and stops at the first failure:
// delegation signed by a pinned root, manifest signed by a delegated key that
// is neither revoked nor expired, manifest structure, expiry, then the
// version rule against installed (a version_number). now is the device clock,
// which the caller must have confirmed as synced.
func (t Trust) Verify(b Bundle, now time.Time, installed int) (*Manifest, Decision, error) {
	if !t.rootSigned(b.Delegation, b.DelegationSig) {
		return nil, 0, reject(CodeBadDelegation, "delegation is not signed by a pinned root key")
	}
	var d Delegation
	if err := json.Unmarshal(b.Delegation, &d); err != nil || d.Schema != SchemaVersion {
		return nil, 0, reject(CodeBadDelegation, "delegation is not valid schema %d JSON", SchemaVersion)
	}
	if err := t.checkManifestSig(&d, b, now); err != nil {
		return nil, 0, err
	}
	m, err := ParseManifest(b.Manifest)
	if err != nil {
		return nil, 0, reject(CodeBadManifest, "%v", err)
	}
	if !now.Before(m.Expires) {
		return nil, 0, reject(CodeManifestExpiry, "manifest expired at %s", m.Expires.UTC().Format(time.RFC3339))
	}
	switch {
	case m.VersionNumber > installed:
		return m, DecisionNewer, nil
	case m.VersionNumber == installed:
		return m, DecisionSame, nil
	case m.Rollback != nil && slices.Contains(m.Rollback.FromVersionNumbers, installed):
		return m, DecisionRollback, nil
	default:
		return nil, 0, reject(CodeVersionLower, "manifest version %s (%d) is lower than installed %d and does not authorize a rollback",
			m.Version, m.VersionNumber, installed)
	}
}

func (t Trust) rootSigned(msg, sig []byte) bool {
	for _, r := range t.Roots {
		if verifyRaw(r, msg, sig) {
			return true
		}
	}
	return false
}

// checkManifestSig finds the delegated key that signed the manifest. When
// only a revoked or expired key matches it says so, because that is a
// different problem (rotate the signer) from a key nobody delegated.
func (t Trust) checkManifestSig(d *Delegation, b Bundle, now time.Time) error {
	var revoked, expired bool
	for _, k := range d.OnlineKeys {
		pub, err := parsePubB64(k.Pub)
		if err != nil || KeyID(pub) != k.ID {
			continue // a malformed entry never vouches for anything
		}
		if !verifyRaw(pub, b.Manifest, b.ManifestSig) {
			continue
		}
		switch {
		case slices.Contains(d.Revoked, k.ID):
			revoked = true
		case !now.Before(k.NotAfter):
			expired = true
		default:
			return nil
		}
	}
	switch {
	case revoked:
		return reject(CodeKeyRevoked, "manifest is signed by a revoked release key")
	case expired:
		return reject(CodeKeyExpired, "manifest is signed by a release key past its not_after date")
	}
	return reject(CodeUnknownKey, "manifest signature does not match any delegated release key")
}
