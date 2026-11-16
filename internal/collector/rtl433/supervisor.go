package rtl433

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

// Line is one stdout line from a radio, with the radio it came from and the
// local receipt time. The receipt time carries a monotonic reading so the
// collector can stamp it later if the wall clock is not yet synced.
type Line struct {
	Radio string
	At    time.Time
	Text  []byte
}

// EventType classifies supervisor events.
type EventType string

const (
	// EventStarted fires after a successful process start.
	EventStarted EventType = "rtl433_started"
	// EventExited fires when the process ends for any reason. Err carries
	// the classified cause when stderr gave one, else the exit error.
	EventExited EventType = "rtl433_exited"
	// EventStartFailed fires when the binary could not be launched at all,
	// for example when it is not installed.
	EventStartFailed EventType = "rtl433_start_failed"
	// EventRestartScheduled fires with the backoff delay before a retry.
	EventRestartScheduled EventType = "rtl433_restart_scheduled"
)

// Event is something the operator should see.
type Event struct {
	Type  EventType
	Radio string
	At    time.Time
	PID   int
	Err   error
	// Message is a short human readable summary.
	Message string
	// Backoff is set on EventRestartScheduled.
	Backoff time.Duration
}

// Backoff controls restart pacing. Exponential with full jitter keeps a
// flapping dongle from spinning the CPU and from hammering the USB bus.
type Backoff struct {
	Min    time.Duration // first delay, default 1s
	Max    time.Duration // cap, default 5m
	Factor float64       // growth per attempt, default 2
	Jitter float64       // fraction of the delay randomized either way, default 0.2
	// ResetAfter is how long a process must run before the attempt counter
	// resets. A process that dies in under this time is still failing.
	ResetAfter time.Duration // default 60s
}

func (b Backoff) withDefaults() Backoff {
	if b.Min <= 0 {
		b.Min = time.Second
	}
	if b.Max <= 0 {
		b.Max = 5 * time.Minute
	}
	if b.Factor < 1 {
		b.Factor = 2
	}
	if b.Jitter < 0 || b.Jitter > 1 {
		b.Jitter = 0.2
	}
	if b.ResetAfter <= 0 {
		b.ResetAfter = time.Minute
	}
	return b
}

// Delay computes the delay for the given attempt (0 based) using r in
// [0, 1) for jitter.
func (b Backoff) Delay(attempt int, r float64) time.Duration {
	b = b.withDefaults()
	d := float64(b.Min) * math.Pow(b.Factor, float64(attempt))
	if d > float64(b.Max) {
		d = float64(b.Max)
	}
	// Spread across [d*(1-j), d*(1+j)].
	d *= 1 + b.Jitter*(2*r-1)
	if d < float64(b.Min) {
		d = float64(b.Min)
	}
	return time.Duration(d)
}

// Config configures a Supervisor.
type Config struct {
	Radio  Radio
	Binary string    // default DefaultBinary
	Start  StartFunc // default OSStart
	// ExtraArgs are appended after Radio.Args, for operator tuning such as
	// "-R" decoder filters or "-g" gain.
	ExtraArgs []string
	Backoff   Backoff
	// Events receives operator-visible events. It is called from the run
	// goroutine and must not block for long.
	Events func(Event)
	Logger *slog.Logger
	// LineBuffer is the capacity of the Lines channel, default 1024.
	LineBuffer int

	// Test hooks. Nil means the real thing.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	Rand  func() float64
}

// Status is a snapshot for the web UI and the telemetry message.
type Status struct {
	Radio         string        `json:"radio"`
	Serial        string        `json:"serial"`
	FrequencyHz   int64         `json:"frequency_hz"`
	Running       bool          `json:"running"`
	PID           int           `json:"pid"`
	StartedAt     time.Time     `json:"started_at"`
	Restarts      int           `json:"restarts"`
	LastExit      time.Time     `json:"last_exit"`
	LastError     string        `json:"last_error"`
	NextRestartIn time.Duration `json:"next_restart_in"`
	// PacketsLast15m counts stdout lines in the trailing 15 minutes, which
	// is the decode rate the telemetry message reports.
	PacketsLast15m int `json:"packets_last_15m"`
}

// Supervisor keeps one rtl_433 process alive for one radio.
type Supervisor struct {
	cfg   Config
	lines chan Line

	mu        sync.Mutex
	running   bool
	pid       int
	startedAt time.Time
	restarts  int
	lastExit  time.Time
	lastErr   error
	nextRetry time.Time
	packets   *minuteWindow
}

// New builds a supervisor. Call Run to start it.
func New(cfg Config) *Supervisor {
	if cfg.Binary == "" {
		cfg.Binary = DefaultBinary
	}
	if cfg.Start == nil {
		cfg.Start = OSStart
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepCtx
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Float64
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.LineBuffer <= 0 {
		cfg.LineBuffer = 1024
	}
	cfg.Backoff = cfg.Backoff.withDefaults()
	return &Supervisor{
		cfg:     cfg,
		lines:   make(chan Line, cfg.LineBuffer),
		packets: newMinuteWindow(15),
	}
}

// Lines returns the stdout stream. It is closed when Run returns.
func (s *Supervisor) Lines() <-chan Line { return s.lines }

// Status returns a snapshot.
func (s *Supervisor) Status() Status {
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Radio:          s.cfg.Radio.ID,
		Serial:         s.cfg.Radio.Serial,
		FrequencyHz:    s.cfg.Radio.FrequencyHz,
		Running:        s.running,
		PID:            s.pid,
		StartedAt:      s.startedAt,
		Restarts:       s.restarts,
		LastExit:       s.lastExit,
		PacketsLast15m: s.packets.count(now),
	}
	if s.lastErr != nil {
		st.LastError = s.lastErr.Error()
	}
	if !s.running && !s.nextRetry.IsZero() && s.nextRetry.After(now) {
		st.NextRestartIn = s.nextRetry.Sub(now)
	}
	return st
}

// Run supervises the process until ctx is canceled. It always returns
// ctx.Err() and closes Lines on the way out.
func (s *Supervisor) Run(ctx context.Context) error {
	defer close(s.lines)
	attempt := 0
	for {
		ranFor, reason := s.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ranFor >= s.cfg.Backoff.ResetAfter {
			attempt = 0
		}
		delay := s.cfg.Backoff.Delay(attempt, s.cfg.Rand())
		attempt++
		now := s.cfg.Now()
		s.mu.Lock()
		s.nextRetry = now.Add(delay)
		s.mu.Unlock()
		s.emit(Event{
			Type:    EventRestartScheduled,
			Radio:   s.cfg.Radio.ID,
			At:      now,
			Err:     reason,
			Backoff: delay,
			Message: fmt.Sprintf("restarting rtl_433 in %s", delay.Round(time.Millisecond)),
		})
		if err := s.cfg.Sleep(ctx, delay); err != nil {
			return ctx.Err()
		}
	}
}

// runOnce starts the process, pumps its output and waits for it to exit.
// It returns how long the process ran and the classified exit reason.
func (s *Supervisor) runOnce(ctx context.Context) (time.Duration, error) {
	start := s.cfg.Now()
	args := append(s.cfg.Radio.Args(), s.cfg.ExtraArgs...)
	proc, err := s.cfg.Start(ctx, s.cfg.Binary, args)
	if err != nil {
		s.mu.Lock()
		s.lastErr = err
		s.lastExit = start
		s.restarts++
		s.mu.Unlock()
		s.emit(Event{Type: EventStartFailed, Radio: s.cfg.Radio.ID, At: start, Err: err,
			Message: "could not start " + s.cfg.Binary + ": " + err.Error()})
		return 0, err
	}

	s.mu.Lock()
	s.running = true
	s.pid = proc.PID()
	s.startedAt = start
	s.lastErr = nil
	s.mu.Unlock()
	s.emit(Event{Type: EventStarted, Radio: s.cfg.Radio.ID, At: start, PID: proc.PID(),
		Message: fmt.Sprintf("rtl_433 started (pid %d): %s %s", proc.PID(), s.cfg.Binary, strings.Join(args, " "))})

	// Kill the process if the context ends while we are blocked in reads.
	killDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if err := proc.Kill(); err != nil {
				s.cfg.Logger.Debug("rtl_433 kill on context end", "radio", s.cfg.Radio.ID, "err", err)
			}
		case <-killDone:
		}
	}()

	var stderrErr error
	var stderrTail []string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stderrTail, stderrErr = s.pumpStderr(proc.Stderr())
	}()
	s.pumpStdout(ctx, proc.Stdout())
	wg.Wait()
	waitErr := proc.Wait()
	close(killDone)

	end := s.cfg.Now()
	reason := stderrErr
	if reason == nil {
		reason = waitErr
	}
	if reason == nil && ctx.Err() == nil {
		reason = errors.New("rtl_433 exited without error")
	}

	s.mu.Lock()
	s.running = false
	s.pid = 0
	s.lastExit = end
	s.lastErr = reason
	s.restarts++
	s.mu.Unlock()

	msg := "rtl_433 exited"
	if reason != nil {
		msg += ": " + reason.Error()
	}
	if len(stderrTail) > 0 && stderrErr == nil && waitErr != nil {
		msg += " (stderr: " + strings.Join(stderrTail, " | ") + ")"
	}
	if ctx.Err() == nil {
		s.emit(Event{Type: EventExited, Radio: s.cfg.Radio.ID, At: end, PID: proc.PID(), Err: reason, Message: msg})
	}
	return end.Sub(start), reason
}

func (s *Supervisor) pumpStdout(ctx context.Context, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		text := sc.Bytes()
		if len(strings.TrimSpace(string(text))) == 0 {
			continue
		}
		cp := make([]byte, len(text))
		copy(cp, text)
		now := s.cfg.Now()
		s.mu.Lock()
		s.packets.add(now)
		s.mu.Unlock()
		select {
		case s.lines <- Line{Radio: s.cfg.Radio.ID, At: now, Text: cp}:
		case <-ctx.Done():
			return
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		s.cfg.Logger.Warn("rtl_433 stdout read error", "radio", s.cfg.Radio.ID, "err", err)
	}
}

// pumpStderr logs stderr and returns the first classified device error plus
// the last few lines for diagnostics.
func (s *Supervisor) pumpStderr(r io.Reader) ([]string, error) {
	const tailLen = 8
	var first error
	var tail []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 16*1024), 256*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.cfg.Logger.Debug("rtl_433 stderr", "radio", s.cfg.Radio.ID, "line", line)
		if err := ClassifyStderr(line); err != nil && first == nil {
			first = err
		}
		tail = append(tail, line)
		if len(tail) > tailLen {
			tail = tail[1:]
		}
	}
	return tail, first
}

func (s *Supervisor) emit(e Event) {
	if e.Err != nil {
		s.cfg.Logger.Info("rtl_433 supervisor", "radio", e.Radio, "event", string(e.Type), "err", e.Err)
	} else {
		s.cfg.Logger.Info("rtl_433 supervisor", "radio", e.Radio, "event", string(e.Type))
	}
	if s.cfg.Events != nil {
		s.cfg.Events(e)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// minuteWindow counts events per minute over a trailing window. It uses a
// map keyed by minute so memory stays bounded regardless of the event rate.
type minuteWindow struct {
	minutes int
	counts  map[int64]int
}

func newMinuteWindow(minutes int) *minuteWindow {
	return &minuteWindow{minutes: minutes, counts: make(map[int64]int)}
}

func (w *minuteWindow) add(t time.Time) {
	w.counts[t.Unix()/60]++
}

func (w *minuteWindow) count(now time.Time) int {
	cutoff := now.Unix()/60 - int64(w.minutes)
	total := 0
	for m, c := range w.counts {
		if m <= cutoff {
			delete(w.counts, m)
			continue
		}
		total += c
	}
	return total
}
