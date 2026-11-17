// Package chrony reads which time source chronyd has selected and feeds it
// the LTE network time. chronyd is the only thing that sets the clock; the
// daemon never does (the unit has no CAP_SYS_TIME).
package chrony

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Runner runs a command and returns its standard output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner is the production Runner.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // fixed binaries and arguments chosen by this package
}

// Source classes reported to the UI and telemetry.
const (
	RefNTP = "ntp"
	RefGPS = "gps"
	RefLTE = "lte"
	RefRTC = "rtc"
)

// Status is the part of chrony's state the daemon uses.
type Status struct {
	// Ref is the class of the selected source, empty when none is selected.
	Ref string
	// LastSync is when chronyd last updated the clock, zero when unknown.
	LastSync time.Time
}

// Query asks chronyc for the selected source and the last update time.
func Query(ctx context.Context, run Runner) (Status, error) {
	out, err := run(ctx, "chronyc", "-c", "sources")
	if err != nil {
		return Status{}, err
	}
	st := Status{Ref: SelectedClass(string(out))}
	if tr, err := run(ctx, "chronyc", "-c", "tracking"); err == nil {
		st.LastSync = refTime(string(tr))
	}
	return st, nil
}

// SelectedClass parses "chronyc -c sources" output and classifies the
// selected source (state '*'). It returns "" when none is selected.
func SelectedClass(csv string) string {
	for _, line := range strings.Split(csv, "\n") {
		f := strings.Split(strings.TrimSpace(line), ",")
		if len(f) < 3 || f[1] != "*" {
			continue
		}
		if f[0] != "#" { // '#' is a local reference clock, '^' and '=' are network peers
			return RefNTP
		}
		return ClassifyRefclock(f[2])
	}
	return ""
}

// ClassifyRefclock maps a reference clock id to a class. The ids come from
// deploy/chrony/pwsgw.conf (GPS, LTE) and gpsd's own refclocks (NMEA, PPS).
func ClassifyRefclock(refid string) string {
	id := strings.ToUpper(refid)
	switch {
	case strings.Contains(id, "LTE"), strings.Contains(id, "MODEM"):
		return RefLTE
	case strings.Contains(id, "RTC"):
		return RefRTC
	case strings.Contains(id, "GPS"), strings.Contains(id, "PPS"), strings.Contains(id, "NMEA"),
		strings.Contains(id, "SHM"), strings.Contains(id, "GNSS"):
		return RefGPS
	default:
		return RefNTP
	}
}

// refTime reads the "Ref time" column of "chronyc -c tracking".
func refTime(csv string) time.Time {
	f := strings.Split(strings.TrimSpace(csv), ",")
	if len(f) < 4 {
		return time.Time{}
	}
	secs, err := strconv.ParseFloat(f[3], 64)
	if err != nil || secs <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(secs), 0).UTC()
}

// sockMagic ends every sample, from chrony's refclock_sock.c.
const sockMagic = 0x534f434b

// SockFeeder writes samples to a chronyd "refclock SOCK" socket.
type SockFeeder struct {
	Path string
}

// Send reports that the true time is at+offset when the system clock read at.
func (f *SockFeeder) Send(at time.Time, offset time.Duration) error {
	if f.Path == "" {
		return errors.New("chrony: no socket path")
	}
	var d net.Dialer
	c, err := d.DialContext(context.Background(), "unixgram", f.Path)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write(EncodeSample(at, offset))
	return err
}

// EncodeSample builds the 40 byte struct sock_sample for a 64 bit host.
func EncodeSample(at time.Time, offset time.Duration) []byte {
	b := make([]byte, 40)
	e := binary.NativeEndian
	e.PutUint64(b[0:], uint64(max(at.Unix(), 0)))            //nolint:gosec // clamped non-negative
	e.PutUint64(b[8:], uint64(max(at.Nanosecond()/1000, 0))) //nolint:gosec // 0..999999
	e.PutUint64(b[16:], math.Float64bits(offset.Seconds()))
	// pulse (0), leap (0) and the pad are zero.
	e.PutUint32(b[36:], sockMagic)
	return b
}
