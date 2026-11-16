package rtl433

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// DefaultBinary is the rtl_433 executable name looked up on PATH.
const DefaultBinary = "rtl_433"

// Band frequencies in Hz. US weather stations use 433.92 MHz for most
// brands and 915 MHz for Fine Offset, Ecowitt and Ambient Weather models.
const (
	Band433MHz int64 = 433920000
	Band915MHz int64 = 915000000
)

// Radio describes one SDR dongle and what it listens to.
type Radio struct {
	// ID is the logical name used in readings and events, for example
	// "radio0". It stays stable across replugs while Serial identifies the
	// physical dongle.
	ID string
	// Serial selects the dongle with "-d :<serial>". Empty means the first
	// device rtl_433 finds, which is only safe with one dongle.
	Serial string
	// FrequencyHz is the center frequency, normally Band433MHz or Band915MHz.
	FrequencyHz int64
	// Role is free text for the operator ("primary", "915 band").
	Role string
}

// Args builds the rtl_433 argument list for the radio.
//
// Flags checked against src/rtl_433.c usage() and option parsing on
// 2026-11-16:
//
//	-d :<serial>   select the dongle by USB serial
//	-f <hz>        center frequency in Hz; an integer avoids locale issues
//	-F json        one JSON object per line on stdout
//	-M level       add rssi, snr and noise in dB to every line
//	-M protocol    add the decoder number
//	-M time:iso:usec:utc:tz
//	               ISO 8601 with microseconds in UTC, with "Z" appended so
//	               a recorded line is unambiguous without knowing the flags
//
// "-C si" is deliberately absent: it renames keys, and the mapping package
// does its own conversion from native units so recordings stay faithful.
func (r Radio) Args() []string {
	args := make([]string, 0, 12)
	if r.Serial != "" {
		args = append(args, "-d", ":"+r.Serial)
	}
	if r.FrequencyHz > 0 {
		args = append(args, "-f", strconv.FormatInt(r.FrequencyHz, 10))
	}
	args = append(args,
		"-F", "json",
		"-M", "level",
		"-M", "protocol",
		"-M", "time:iso:usec:utc:tz",
	)
	return args
}

// Process is a running rtl_433. The interface exists so tests can supply a
// fake without the binary.
type Process interface {
	Stdout() io.Reader
	Stderr() io.Reader
	// Wait blocks until the process exits and returns its exit error, if
	// any. Stdout and Stderr must be drained before or concurrently with
	// Wait, as with os/exec.
	Wait() error
	Kill() error
	PID() int
}

// StartFunc launches a process. OSStart is the real one.
type StartFunc func(ctx context.Context, name string, args []string) (Process, error)

// OSStart runs the binary with os/exec. Canceling ctx kills it.
func OSStart(ctx context.Context, name string, args []string) (Process, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osProcess{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

type osProcess struct {
	cmd    *exec.Cmd
	stdout io.Reader
	stderr io.Reader
}

func (p *osProcess) Stdout() io.Reader { return p.stdout }
func (p *osProcess) Stderr() io.Reader { return p.stderr }
func (p *osProcess) Wait() error       { return p.cmd.Wait() }
func (p *osProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
func (p *osProcess) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Typed reasons for an rtl_433 exit, classified from stderr. Each is wrapped
// with the stderr line that triggered it so errors.Is works and the message
// is still readable.
var (
	// ErrNoDevice means rtl_433 found no dongle or could not open the one
	// selected by serial.
	ErrNoDevice = errors.New("rtl_433: no supported devices found")
	// ErrDeviceLost means the dongle stopped delivering samples mid run,
	// which is what a USB disconnect or a brown-out looks like.
	ErrDeviceLost = errors.New("rtl_433: device lost")
	// ErrUSB is a libusb level failure, usually cabling or power.
	ErrUSB = errors.New("rtl_433: usb error")
)

// stderrPatterns map substrings of rtl_433 stderr lines to typed errors.
// Strings checked against src/sdr.c and src/rtl_433.c on 2026-11-16.
var stderrPatterns = []struct {
	substr string
	err    error
}{
	{"No supported devices found", ErrNoDevice},
	{"Could not find device with serial", ErrNoDevice},
	{"Failed to open rtlsdr device", ErrNoDevice},
	{"Failed to open sdr device", ErrNoDevice},
	{"Unable to open a device", ErrNoDevice},
	{"Failed to setup sdr device", ErrNoDevice},
	{"Async read stalled", ErrDeviceLost},
	{"async read failed", ErrDeviceLost},
	{"Input device start failed", ErrDeviceLost},
	{"Check your RTL-SDR dongle, USB cables, and power supply", ErrUSB},
	{"LIBUSB_ERROR", ErrUSB},
	{"usb_claim_interface", ErrUSB},
	{"usb_open error", ErrUSB},
}

// ClassifyStderr returns a typed error for a stderr line that signals a
// device problem, or nil for informational output.
func ClassifyStderr(line string) error {
	for _, p := range stderrPatterns {
		if strings.Contains(line, p.substr) {
			return fmt.Errorf("%w: %s", p.err, strings.TrimSpace(line))
		}
	}
	return nil
}
