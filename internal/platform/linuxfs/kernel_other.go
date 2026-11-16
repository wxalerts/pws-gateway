//go:build !linux

package linuxfs

import (
	"errors"
	"time"
)

var errNotLinux = errors.New("linuxfs: only available on Linux")

// FSUsedPct is unavailable off Linux; the gateway only ships on Linux and
// this stub keeps `go vet ./...` working on developer laptops.
func FSUsedPct(string) (float64, error) { return 0, errNotLinux }

// KernelTime mirrors the Linux definition.
type KernelTime struct {
	Synced bool
	Offset time.Duration
}

// KernelTimeStatus is unavailable off Linux.
func KernelTimeStatus() (KernelTime, error) { return KernelTime{}, errNotLinux }
