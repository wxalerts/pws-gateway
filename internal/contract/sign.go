package contract

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Command verification errors. Each one means the command must be dropped
// silently without an ack.
var (
	ErrBadSignature   = errors.New("contract: command signature is invalid")
	ErrExpired        = errors.New("contract: command has expired")
	ErrDeviceMismatch = errors.New("contract: command is addressed to another device")
	ErrReplay         = errors.New("contract: command id was already seen")
	ErrBadKey         = errors.New("contract: key has the wrong size")
)

// signedFields is the object whose canonical JSON a command signature covers.
// The field order here does not matter because canonicalization sorts keys.
type signedFields struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	DeviceID string          `json:"device_id"`
	Exp      json.RawMessage `json:"exp"`
	Body     json.RawMessage `json:"body"`
}

// SigningInput returns the bytes a command signature is computed over: the
// RFC 8785 canonical JSON of {id, type, device_id, exp, body}.
//
// For a command obtained from ParseCommand the input is derived from the
// bytes that were received, so the signature survives any difference in how
// this package would re-serialize a timestamp. For a command built in Go the
// input is derived from the fields, which is also exactly what Marshal puts
// on the wire.
func SigningInput(c *Command) ([]byte, error) {
	if c.signingInput != nil {
		return c.signingInput, nil
	}
	body := c.Body
	if len(body) == 0 {
		body = json.RawMessage("{}")
	}
	exp, err := json.Marshal(c.Exp)
	if err != nil {
		return nil, fmt.Errorf("contract: encode exp: %w", err)
	}
	raw, err := json.Marshal(signedFields{
		ID: c.ID, Type: c.Type, DeviceID: c.DeviceID, Exp: exp, Body: body,
	})
	if err != nil {
		return nil, fmt.Errorf("contract: encode signing input: %w", err)
	}
	return Canonicalize(raw)
}

// signingInputFromJSON extracts the five signed members from a received
// command document without re-encoding their values.
func signingInputFromJSON(data []byte) ([]byte, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	picked := make(map[string]json.RawMessage, 5)
	for _, k := range []string{"id", "type", "device_id", "exp", "body"} {
		v, ok := all[k]
		if !ok {
			return nil, fmt.Errorf("contract: command is missing signed field %q", k)
		}
		picked[k] = v
	}
	raw, err := json.Marshal(picked)
	if err != nil {
		return nil, fmt.Errorf("contract: encode signing input: %w", err)
	}
	return Canonicalize(raw)
}

// SignCommand computes the Ed25519 signature over SigningInput(c) with priv
// and stores it in c.Sig as padded standard base64. An empty Body is treated
// as {} and written back so the signed and published bodies agree.
func SignCommand(priv ed25519.PrivateKey, c *Command) error {
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key is %d bytes", ErrBadKey, len(priv))
	}
	if len(c.Body) == 0 {
		c.Body = json.RawMessage("{}")
	}
	c.signingInput = nil // the fields are authoritative when signing
	input, err := SigningInput(c)
	if err != nil {
		return err
	}
	c.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, input))
	return nil
}

// ReplayCache remembers command ids so a captured command cannot be applied
// twice. Seen records id and reports whether it was already present. exp is
// the command expiry, after which the id can be forgotten because expired
// commands are rejected before the cache is consulted.
type ReplayCache interface {
	Seen(id string, exp time.Time) (bool, error)
}

// VerifyOptions configures VerifyCommand.
type VerifyOptions struct {
	// DeviceID is this device's id. The command's device_id must match it.
	// Empty skips the check, which is only appropriate in tooling.
	DeviceID string
	// Now returns the current time. nil means time.Now.
	Now func() time.Time
	// Replay is consulted after the signature verifies. nil disables the
	// replay check, which is only appropriate in tooling.
	Replay ReplayCache
}

// VerifyCommand applies the device side checks the contract requires, in
// this order: device_id match, expiry, signature, replay. The replay cache is
// consulted last so that an attacker cannot fill it with unsigned ids. Any
// error means the command must be ignored and not acked.
func VerifyCommand(pub ed25519.PublicKey, c *Command, opts VerifyOptions) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key is %d bytes", ErrBadKey, len(pub))
	}
	if opts.DeviceID != "" && c.DeviceID != opts.DeviceID {
		return fmt.Errorf("%w: got %q, want %q", ErrDeviceMismatch, c.DeviceID, opts.DeviceID)
	}
	now := time.Now()
	if opts.Now != nil {
		now = opts.Now()
	}
	if !c.Exp.After(now) {
		return fmt.Errorf("%w: exp %s, now %s", ErrExpired, c.Exp.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	sig, err := base64.StdEncoding.DecodeString(c.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: not a base64 encoded 64 byte signature", ErrBadSignature)
	}
	input, err := SigningInput(c)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, input, sig) {
		return ErrBadSignature
	}
	if opts.Replay != nil {
		seen, err := opts.Replay.Seen(c.ID, c.Exp)
		if err != nil {
			return fmt.Errorf("contract: replay cache: %w", err)
		}
		if seen {
			return fmt.Errorf("%w: %q", ErrReplay, c.ID)
		}
	}
	return nil
}

// MemoryReplayCache is a ReplayCache that keeps ids in memory until their
// command expiry has passed. It is safe for concurrent use. A device that
// must survive a reboot within a command's validity window should back the
// cache with its database instead.
type MemoryReplayCache struct {
	mu  sync.Mutex
	ids map[string]time.Time
	now func() time.Time
}

// NewMemoryReplayCache returns an empty cache. now may be nil for time.Now.
func NewMemoryReplayCache(now func() time.Time) *MemoryReplayCache {
	if now == nil {
		now = time.Now
	}
	return &MemoryReplayCache{ids: make(map[string]time.Time), now: now}
}

// Seen implements ReplayCache.
func (m *MemoryReplayCache) Seen(id string, exp time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for k, e := range m.ids {
		if !e.After(now) {
			delete(m.ids, k)
		}
	}
	if _, ok := m.ids[id]; ok {
		return true, nil
	}
	m.ids[id] = exp
	return false, nil
}

// Len reports how many unexpired ids the cache holds.
func (m *MemoryReplayCache) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.ids)
}
