package logging

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
)

// Ring is a fixed-capacity buffer of recent log lines. It implements io.Writer
// so a slog text handler can write into it, and Tail returns the newest lines
// for the web UI. Writes are split on newlines; a trailing partial line is
// held until the newline arrives.
type Ring struct {
	mu      sync.Mutex
	lines   []string
	next    int
	full    bool
	partial []byte
}

// NewRing allocates a ring that keeps the last n lines. n must be positive.
func NewRing(n int) *Ring {
	if n <= 0 {
		n = DefaultRingLines
	}
	return &Ring{lines: make([]string, n)}
}

// Write appends complete lines from p to the ring.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	buf := p
	if len(r.partial) > 0 {
		buf = append(append([]byte(nil), r.partial...), p...)
		r.partial = nil
	}
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		r.push(string(buf[:i]))
		buf = buf[i+1:]
	}
	if len(buf) > 0 {
		r.partial = append([]byte(nil), buf...)
	}
	return len(p), nil
}

func (r *Ring) push(line string) {
	r.lines[r.next] = line
	r.next++
	if r.next == len(r.lines) {
		r.next = 0
		r.full = true
	}
}

// Len returns the number of lines currently held.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lenLocked()
}

func (r *Ring) lenLocked() int {
	if r.full {
		return len(r.lines)
	}
	return r.next
}

// Tail returns up to n of the most recent lines, oldest first. n <= 0 returns
// everything held.
func (r *Ring) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	have := r.lenLocked()
	if n <= 0 || n > have {
		n = have
	}
	out := make([]string, 0, n)
	start := r.next - n
	if start < 0 {
		start += len(r.lines)
	}
	for i := 0; i < n; i++ {
		out = append(out, r.lines[(start+i)%len(r.lines)])
	}
	return out
}

// Handler returns a slog handler that renders records as text lines into the
// ring. Records are formatted the same way as the text console output so the
// UI shows the same shape operators see in `journalctl`.
func (r *Ring) Handler(level slog.Leveler) slog.Handler {
	return slog.NewTextHandler(r, &slog.HandlerOptions{Level: level})
}

// Multi fans a record out to every handler that is enabled for its level.
func Multi(handlers ...slog.Handler) slog.Handler {
	return multiHandler(handlers)
}

type multiHandler []slog.Handler

func (m multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, rec slog.Record) error {
	var first error
	for _, h := range m {
		if !h.Enabled(ctx, rec.Level) {
			continue
		}
		if err := h.Handle(ctx, rec.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithGroup(name)
	}
	return out
}
