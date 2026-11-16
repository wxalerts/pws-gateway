package store

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

// MinFlushInterval is the shortest history cadence the owner may set. The
// SD card is the limiting part on a Pi, and ten minutes already means
// under 150 commits a day.
const MinFlushInterval = 10 * time.Minute

// DefaultFlushInterval matches the uplink cadence.
const DefaultFlushInterval = 15 * time.Minute

// WriterOptions configures a Writer.
type WriterOptions struct {
	// Interval between flushes. Values under MinFlushInterval are raised to
	// it unless AllowShortInterval is set (tests only).
	Interval           time.Duration
	AllowShortInterval bool
	Logger             *slog.Logger
	// MaxPending bounds the in-memory queue; when exceeded a flush happens
	// immediately regardless of the interval. Default 2000 buckets.
	MaxPending int
}

// Writer accumulates buckets in memory and commits them in one transaction
// per interval.
type Writer struct {
	store *Store
	opts  WriterOptions

	mu      sync.Mutex
	pending []model.Bucket
	kick    chan struct{}
	flushes int
}

// NewWriter builds a Writer.
func NewWriter(s *Store, opts WriterOptions) *Writer {
	if opts.Interval <= 0 {
		opts.Interval = DefaultFlushInterval
	}
	if opts.Interval < MinFlushInterval && !opts.AllowShortInterval {
		opts.Interval = MinFlushInterval
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.MaxPending <= 0 {
		opts.MaxPending = 2000
	}
	return &Writer{store: s, opts: opts, kick: make(chan struct{}, 1)}
}

// Interval returns the effective flush interval after clamping.
func (w *Writer) Interval() time.Duration { return w.opts.Interval }

// Add queues buckets for the next flush.
func (w *Writer) Add(buckets ...model.Bucket) {
	if len(buckets) == 0 {
		return
	}
	w.mu.Lock()
	w.pending = append(w.pending, buckets...)
	over := len(w.pending) >= w.opts.MaxPending
	w.mu.Unlock()
	if over {
		select {
		case w.kick <- struct{}{}:
		default:
		}
	}
}

// Pending returns how many buckets wait for the next flush.
func (w *Writer) Pending() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pending)
}

// Flushes returns how many commits have happened, for telemetry.
func (w *Writer) Flushes() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushes
}

// Flush commits everything pending now. On error the buckets stay queued
// for the next attempt.
func (w *Writer) Flush(ctx context.Context) error {
	w.mu.Lock()
	batch := w.pending
	w.pending = nil
	w.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	if err := w.store.WriteRollups(ctx, batch); err != nil {
		w.mu.Lock()
		w.pending = append(batch, w.pending...)
		w.mu.Unlock()
		return err
	}
	w.mu.Lock()
	w.flushes++
	w.mu.Unlock()
	return nil
}

// Run flushes on the interval until ctx ends, then flushes one last time
// with a short grace context so shutdown does not lose the open batch.
func (w *Writer) Run(ctx context.Context) error {
	t := time.NewTicker(w.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.finalFlush()
			return ctx.Err()
		case <-t.C:
		case <-w.kick:
		}
		if err := w.Flush(ctx); err != nil {
			w.opts.Logger.Error("rollup flush failed, will retry", "err", err, "pending", w.Pending())
		}
	}
}

// finalFlush writes the open batch with a fresh context, because the run
// context is already canceled when shutdown begins.
func (w *Writer) finalFlush() {
	fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.Flush(fctx); err != nil {
		w.opts.Logger.Error("final rollup flush failed", "err", err)
	}
}
