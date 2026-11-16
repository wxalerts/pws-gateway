// Package logging configures the process-wide slog logger for pwsgw.
//
// Two outputs are always wired: the primary stream (stderr by default) and an
// in-memory ring buffer that the web UI reads through GET /api/logs. The
// primary stream is JSON when the process runs under systemd or a container
// and human-readable text when stderr is a terminal. The level comes from
// PWSGW_LOG_LEVEL (debug, info, warn, error; default info) and the format can
// be forced with PWSGW_LOG_FORMAT (json or text).
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Environment variables read by this package.
const (
	EnvLevel  = "PWSGW_LOG_LEVEL"
	EnvFormat = "PWSGW_LOG_FORMAT"
)

// DefaultRingLines is the number of recent log lines kept for GET /api/logs.
const DefaultRingLines = 1000

// Options controls New. The zero value is a sensible production setup.
type Options struct {
	// Level is the minimum level written to both outputs.
	Level slog.Level
	// Format is "json", "text" or "" (auto: text on a terminal, JSON otherwise).
	Format string
	// Output receives the primary stream. Defaults to os.Stderr.
	Output io.Writer
	// RingLines is the ring buffer capacity. Defaults to DefaultRingLines.
	RingLines int
}

// FromEnv builds Options from the environment, leaving Output nil.
func FromEnv() Options {
	return Options{
		Level:  ParseLevel(os.Getenv(EnvLevel)),
		Format: strings.ToLower(strings.TrimSpace(os.Getenv(EnvFormat))),
	}
}

// ParseLevel maps a level name to a slog.Level. Unknown or empty input is info
// so that a typo in an environment file never silences the daemon.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New returns a logger that writes to the primary output and to the returned
// ring buffer. It does not call slog.SetDefault; the caller decides.
func New(opts Options) (*slog.Logger, *Ring) {
	out := opts.Output
	if out == nil {
		out = os.Stderr
	}
	lines := opts.RingLines
	if lines <= 0 {
		lines = DefaultRingLines
	}
	ring := NewRing(lines)

	hopts := &slog.HandlerOptions{Level: opts.Level}
	var primary slog.Handler
	switch {
	case opts.Format == "json":
		primary = slog.NewJSONHandler(out, hopts)
	case opts.Format == "text":
		primary = slog.NewTextHandler(out, hopts)
	case isTerminal(out):
		primary = slog.NewTextHandler(out, hopts)
	default:
		primary = slog.NewJSONHandler(out, hopts)
	}

	return slog.New(Multi(primary, ring.Handler(opts.Level))), ring
}

// isTerminal reports whether w is a character device such as a TTY.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
