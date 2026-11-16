package auth

import (
	"sync"
	"time"
)

// Rate limiter defaults. Five wrong passwords lock the source address for 30
// seconds; every further failure doubles the lockout up to an hour. A real
// owner who mistypes twice never notices; a guessing script gets at most a
// few dozen attempts a day.
const (
	DefaultLockoutThreshold = 5
	DefaultBaseLockout      = 30 * time.Second
	DefaultMaxLockout       = time.Hour
	// DefaultForgetAfter drops the failure history of an address that has
	// been quiet for this long.
	DefaultForgetAfter = 24 * time.Hour
	// maxTrackedAddresses bounds memory if a flood of spoofed sources shows
	// up; the oldest quiet entries are evicted first.
	maxTrackedAddresses = 10000
)

// LimiterOptions tunes a Limiter. Zero values take the defaults.
type LimiterOptions struct {
	Threshold   int
	BaseLockout time.Duration
	MaxLockout  time.Duration
	ForgetAfter time.Duration
	Now         func() time.Time
}

// Limiter throttles login attempts per source address.
type Limiter struct {
	mu      sync.Mutex
	opts    LimiterOptions
	entries map[string]*limiterEntry
}

type limiterEntry struct {
	failures    int
	lockedUntil time.Time
	lastSeen    time.Time
}

// NewLimiter returns a limiter with the given options.
func NewLimiter(opts LimiterOptions) *Limiter {
	if opts.Threshold <= 0 {
		opts.Threshold = DefaultLockoutThreshold
	}
	if opts.BaseLockout <= 0 {
		opts.BaseLockout = DefaultBaseLockout
	}
	if opts.MaxLockout <= 0 {
		opts.MaxLockout = DefaultMaxLockout
	}
	if opts.ForgetAfter <= 0 {
		opts.ForgetAfter = DefaultForgetAfter
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Limiter{opts: opts, entries: map[string]*limiterEntry{}}
}

// Check reports whether addr may attempt a login now. When it may not,
// retryAfter says how long the lockout has left (for the Retry-After header).
func (l *Limiter) Check(addr string) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[addr]
	if !ok {
		return true, 0
	}
	now := l.opts.Now()
	if now.Before(e.lockedUntil) {
		return false, e.lockedUntil.Sub(now)
	}
	return true, 0
}

// Failure records a wrong password. It returns whether addr is now locked
// out and for how long.
func (l *Limiter) Failure(addr string) (locked bool, lockout time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.opts.Now()
	l.pruneLocked(now)
	e, ok := l.entries[addr]
	if !ok {
		e = &limiterEntry{}
		l.entries[addr] = e
	}
	e.failures++
	e.lastSeen = now
	over := e.failures - l.opts.Threshold
	if over < 0 {
		return false, 0
	}
	lockout = l.opts.BaseLockout
	for i := 0; i < over && lockout < l.opts.MaxLockout; i++ {
		lockout *= 2
	}
	if lockout > l.opts.MaxLockout {
		lockout = l.opts.MaxLockout
	}
	e.lockedUntil = now.Add(lockout)
	return true, lockout
}

// Success clears the history for addr after a correct login.
func (l *Limiter) Success(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, addr)
}

// Failures returns the recorded failure count for addr (for tests and the
// audit log).
func (l *Limiter) Failures(addr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.entries[addr]; ok {
		return e.failures
	}
	return 0
}

func (l *Limiter) pruneLocked(now time.Time) {
	for k, e := range l.entries {
		if now.Sub(e.lastSeen) > l.opts.ForgetAfter && !now.Before(e.lockedUntil) {
			delete(l.entries, k)
		}
	}
	if len(l.entries) < maxTrackedAddresses {
		return
	}
	// Still too many: evict the quietest unlocked entries.
	for k, e := range l.entries {
		if !now.Before(e.lockedUntil) {
			delete(l.entries, k)
		}
		if len(l.entries) < maxTrackedAddresses {
			return
		}
	}
}
