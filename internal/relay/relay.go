// Package relay holds what every relay destination shares: the relay owner
// rule, the observation handed to a plugin, delivery health and coded errors.
// The specification is docs/relay/README.md and the golden vectors are in
// testdata/relay/. The Rust ingest worker implements the same specification.
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/contract"
)

// Owner says who relays a station to a destination. There is exactly one
// owner at a time, so one observation is never sent twice.
type Owner string

// The valid owners. Anything else, including the empty string, means nobody
// relays the station.
const (
	OwnerDevice Owner = "device"
	OwnerServer Owner = "server"
)

// ParseOwner normalises s. An unknown value becomes the empty owner.
func ParseOwner(s string) Owner {
	switch Owner(s) {
	case OwnerDevice, OwnerServer:
		return Owner(s)
	}
	return ""
}

// PiSends reports whether the Pi daemon relays a station with this owner.
func (o Owner) PiSends() bool { return ParseOwner(string(o)) == OwnerDevice }

// ServerSends reports whether the WxAlerts server relays it.
func (o Owner) ServerSends() bool { return ParseOwner(string(o)) == OwnerServer }

// Stable error codes shown by the relay page and shared with the server.
const (
	CodeNoData        = "no_data"
	CodeNoPosition    = "no_position"
	CodeBadID         = "bad_id"
	CodeBadOwner      = "bad_owner"
	CodeBadServer     = "bad_server"
	CodeTimeUnsynced  = "time_unsynced"
	CodeConnect       = "connect"
	CodeLogin         = "login"
	CodeSend          = "send"
	CodeNotConfigured = "not_configured"
)

// Error is an error with a stable code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

// Errorf builds an Error.
func Errorf(code, msg string) *Error { return &Error{Code: code, Msg: msg} }

// CodeOf returns the code of err, or "send" for an uncoded error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeSend
}

// RainTotals are the plugin's own rolling rain sums in millimeters. The obs
// bucket carries only the bucket's rain, so the relay keeps the sums.
type RainTotals struct {
	Last1hMM        float64 `json:"last_1h_mm"`
	Last24hMM       float64 `json:"last_24h_mm"`
	SinceMidnightMM float64 `json:"since_midnight_mm"`
}

// Observation is one station in one bucket plus the relay's own inputs.
type Observation struct {
	// Start is the bucket start (UTC) and IntervalS its width in seconds.
	Start     time.Time
	IntervalS int
	Station   contract.Station
	// Rain is nil when the totals are not known yet.
	Rain *RainTotals
}

// Plugin is one relay destination. The five operations mirror the
// specification: configure, validate, map, send, health.
type Plugin interface {
	Name() string
	// Configure loads destination settings from JSON. No network access.
	Configure(raw json.RawMessage) error
	// Validate lists every problem, or nil.
	Validate() []Problem
	// Map turns an observation into a destination message. It is a pure
	// function of its input.
	Map(obs Observation) (string, error)
	// Send delivers one message.
	Send(ctx context.Context, msg string) error
	Health() Health
}

// Problem is one validation finding.
type Problem struct {
	Code string `json:"code"`
	Msg  string `json:"message"`
}

// Failure is a recorded delivery error. Msg is already redacted.
type Failure struct {
	Code string    `json:"code"`
	Msg  string    `json:"message"`
	At   time.Time `json:"at"`
}

// Health is the delivery state the relay page shows.
type Health struct {
	LastAttempt *time.Time `json:"last_attempt"`
	LastSuccess *time.Time `json:"last_success"`
	LastError   *Failure   `json:"last_error"`
	// Failures counts consecutive failures since the last success.
	Failures int `json:"failures"`
}

// Tracker accumulates Health. It is safe for concurrent use and outlives the
// plugin instance, so a reconfiguration does not lose the history.
type Tracker struct {
	mu sync.Mutex
	h  Health
}

// Health returns a copy.
func (t *Tracker) Health() Health {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.h
}

// Success records a delivered message.
func (t *Tracker) Success(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.h.LastAttempt, t.h.LastSuccess = &at, &at
	t.h.Failures = 0
}

// Fail records a failed attempt. msg must already be redacted. attempt is
// false for a skipped send (for example unsynced time), which is recorded as
// the last error without counting as a failed attempt.
func (t *Tracker) Fail(at time.Time, code, msg string, attempt bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.h.LastError = &Failure{Code: code, Msg: msg, At: at}
	if attempt {
		t.h.LastAttempt = &at
		t.h.Failures++
	}
}
