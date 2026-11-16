//go:build linux

package linuxfs

import (
	"errors"
	"syscall"
	"time"
)

// FSUsedPct returns the used percentage of the filesystem holding path, the
// way df computes it (used / (used + available)).
func FSUsedPct(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	if st.Bsize <= 0 {
		return 0, errors.New("linuxfs: filesystem reports a non-positive block size")
	}
	bsize := uint64(st.Bsize)
	used := (st.Blocks - st.Bfree) * bsize
	avail := st.Bavail * bsize
	if used+avail == 0 {
		return 0, errors.New("linuxfs: filesystem reports zero size")
	}
	return float64(used) / float64(used+avail) * 100, nil
}

// Kernel time status values returned by adjtimex.
const (
	timeError = 5      // TIME_ERROR: clock not synchronized
	staNano   = 0x2000 // STA_NANO: offset field is in nanoseconds
)

// KernelTime is the clock discipline state the kernel keeps on behalf of
// whichever NTP daemon is running.
type KernelTime struct {
	Synced bool
	Offset time.Duration
}

// KernelTimeStatus queries adjtimex with no modifications, which needs no
// privilege. Both chrony and systemd-timesyncd clear STA_UNSYNC once they
// trust the clock, so this is a daemon independent fallback.
func KernelTimeStatus() (KernelTime, error) {
	var tx syscall.Timex
	state, err := syscall.Adjtimex(&tx)
	if err != nil {
		return KernelTime{}, err
	}
	kt := KernelTime{Synced: state != timeError}
	if tx.Status&staNano != 0 {
		kt.Offset = time.Duration(tx.Offset)
	} else {
		kt.Offset = time.Duration(tx.Offset) * time.Microsecond
	}
	return kt, nil
}
