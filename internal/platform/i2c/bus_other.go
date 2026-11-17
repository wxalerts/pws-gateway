//go:build !linux

package i2c

// OpenDev is not available off Linux.
func OpenDev(int) (Bus, error) { return nil, ErrNoBus }
