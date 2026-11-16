package config

import (
	"errors"
	"sync"
	"time"
)

// Phase is where a Commit is in its lifecycle.
type Phase string

// Commit phases.
const (
	// PhaseIdle: the active value is in effect and nothing is staged.
	PhaseIdle Phase = "idle"
	// PhaseStaged: a candidate is stored but not applied.
	PhaseStaged Phase = "staged"
	// PhaseApplied: the candidate is in effect and must be confirmed before
	// the deadline or it is rolled back.
	PhaseApplied Phase = "applied"
)

// Errors returned by Commit.
var (
	ErrNothingStaged    = errors.New("commit: nothing staged")
	ErrNothingApplied   = errors.New("commit: nothing applied")
	ErrAlreadyApplied   = errors.New("commit: a change is already applied and awaiting confirmation")
	ErrDeadlinePassed   = errors.New("commit: confirmation deadline passed, change rolled back")
	ErrApplyFailed      = errors.New("commit: apply callback failed")
	ErrRollbackFailed   = errors.New("commit: rollback callback failed")
	ErrInvalidWindow    = errors.New("commit: confirmation window must be positive")
	ErrCommitNilApplier = errors.New("commit: nil apply callback")
)

// DefaultConfirmWindow is how long the operator has to confirm a network
// change before it is undone. Two minutes is enough to reload the UI from the
// new address and short enough to recover a mistyped static IP without a trip
// to the device.
const DefaultConfirmWindow = 2 * time.Minute

// Commit is the generic commit-confirm-rollback state machine used for
// settings that can lock the operator out (network today). The flow is:
//
//	Stage(candidate)  ->  Apply(window)  ->  Confirm()
//	                                    \->  Expire() or Rollback()
//
// apply is called with the candidate on Apply and with the previous value on
// rollback. Commit never starts timers itself: the owner calls Expire from a
// ticker or on demand, and supplies the clock, so tests stay deterministic.
type Commit[T any] struct {
	mu       sync.Mutex
	now      func() time.Time
	apply    func(T) error
	phase    Phase
	active   T
	previous T
	pending  *T
	deadline time.Time
}

// State is a snapshot of a Commit for the API.
type State[T any] struct {
	Phase    Phase      `json:"phase"`
	Active   T          `json:"active"`
	Pending  *T         `json:"pending,omitempty"`
	Deadline *time.Time `json:"deadline,omitempty"`
}

// NewCommit returns a machine in PhaseIdle with active in effect. apply may
// be nil for a pure state tracker (the caller applies the value itself); now
// defaults to time.Now.
func NewCommit[T any](active T, apply func(T) error, now func() time.Time) *Commit[T] {
	if now == nil {
		now = time.Now
	}
	if apply == nil {
		apply = func(T) error { return nil }
	}
	return &Commit[T]{now: now, apply: apply, phase: PhaseIdle, active: active}
}

// Stage stores a candidate without applying it. Staging again replaces the
// candidate. It fails while a previous change is applied and unconfirmed.
func (c *Commit[T]) Stage(candidate T) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == PhaseApplied {
		return ErrAlreadyApplied
	}
	c.pending = &candidate
	c.phase = PhaseStaged
	return nil
}

// Apply puts the staged candidate into effect and starts the confirmation
// window. If the apply callback fails the machine stays in PhaseStaged.
func (c *Commit[T]) Apply(window time.Duration) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if window <= 0 {
		return time.Time{}, ErrInvalidWindow
	}
	switch c.phase {
	case PhaseApplied:
		return time.Time{}, ErrAlreadyApplied
	case PhaseIdle:
		return time.Time{}, ErrNothingStaged
	}
	if err := c.apply(*c.pending); err != nil {
		return time.Time{}, errors.Join(ErrApplyFailed, err)
	}
	c.previous = c.active
	c.active = *c.pending
	c.deadline = c.now().Add(window)
	c.phase = PhaseApplied
	return c.deadline, nil
}

// Confirm makes the applied candidate permanent. Called after the deadline it
// rolls back instead and returns ErrDeadlinePassed.
func (c *Commit[T]) Confirm() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase != PhaseApplied {
		return ErrNothingApplied
	}
	if c.now().After(c.deadline) {
		if err := c.rollbackLocked(); err != nil {
			return errors.Join(ErrDeadlinePassed, err)
		}
		return ErrDeadlinePassed
	}
	var zero T
	c.previous = zero
	c.pending = nil
	c.deadline = time.Time{}
	c.phase = PhaseIdle
	return nil
}

// Rollback discards the staged candidate, or reinstates the previous value if
// the candidate was applied.
func (c *Commit[T]) Rollback() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == PhaseIdle {
		return nil
	}
	return c.rollbackLocked()
}

func (c *Commit[T]) rollbackLocked() error {
	var retErr error
	if c.phase == PhaseApplied {
		if err := c.apply(c.previous); err != nil {
			retErr = errors.Join(ErrRollbackFailed, err)
		}
		c.active = c.previous
	}
	var zero T
	c.previous = zero
	c.pending = nil
	c.deadline = time.Time{}
	c.phase = PhaseIdle
	return retErr
}

// Expire rolls back an applied change whose deadline has passed. It returns
// true when a rollback happened. Owners call it periodically.
func (c *Commit[T]) Expire() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase != PhaseApplied || !c.now().After(c.deadline) {
		return false, nil
	}
	return true, c.rollbackLocked()
}

// State returns a snapshot.
func (c *Commit[T]) State() State[T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := State[T]{Phase: c.phase, Active: c.active}
	if c.pending != nil {
		p := *c.pending
		st.Pending = &p
	}
	if c.phase == PhaseApplied {
		d := c.deadline
		st.Deadline = &d
	}
	return st
}
