package uplink

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"time"
)

// Interval is the uplink cadence for obs and tel.
const Interval = 15 * time.Minute

// Offset returns the fixed offset into each 15 minute window at which this
// device publishes, so that 10,000 devices do not all land on :00 and :15.
//
// The formula is
//
//	offset = (big-endian uint64 of the first 8 bytes of SHA-256(device_id)) mod 900   [seconds]
//
// It is deterministic (a device keeps its slot across restarts and the server
// can predict it), lies in [0, 900) seconds, and spreads uniformly because
// SHA-256 output is.
func Offset(deviceID string) time.Duration {
	sum := sha256.Sum256([]byte(deviceID))
	n := binary.BigEndian.Uint64(sum[:8])
	return time.Duration(n%uint64(Interval.Seconds())) * time.Second // #nosec G115 (the remainder is below 900)
}

// NextSend returns the first send instant strictly after t: the first
// instant of the form k*15min + Offset(deviceID).
func NextSend(deviceID string, t time.Time) time.Time {
	next := t.UTC().Truncate(Interval).Add(Offset(deviceID))
	if !next.After(t) {
		next = next.Add(Interval)
	}
	return next
}

// Reconnect backoff bounds from the contract: exponential with jitter from
// 5 seconds to 5 minutes.
const (
	MinBackoff = 5 * time.Second
	MaxBackoff = 5 * time.Minute
)

// Backoff returns the delay before reconnect attempt number attempt (1 is the
// first retry after a failure; 0 means no delay, the first ever attempt). The
// nominal delay doubles from MinBackoff up to MaxBackoff and rnd, a value in
// [0, 1), spreads it by plus or minus 20 percent so a fleet that lost its
// broker together does not return together. The result is always within
// [MinBackoff, MaxBackoff].
func Backoff(attempt int, rnd func() float64) time.Duration {
	if attempt <= 0 {
		return 0
	}
	nominal := float64(MinBackoff) * math.Pow(2, float64(attempt-1))
	if nominal > float64(MaxBackoff) || math.IsInf(nominal, 0) {
		nominal = float64(MaxBackoff)
	}
	d := time.Duration(nominal * (0.8 + 0.4*rnd()))
	return min(max(d, MinBackoff), MaxBackoff)
}
