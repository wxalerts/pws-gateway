package cwop

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/relay"
)

// Timing defaults. CWOP asks for no more than one packet per 5 minutes per
// station; the Pi sends every 10 minutes.
const (
	DefaultInterval = 10 * time.Minute
	DefaultTick     = 30 * time.Second
	maxBackoff      = 15 * time.Minute
	firstBackoff    = time.Minute
)

// Source supplies the newest closed observation of a station and the rolling
// rain totals. The app adapts the rollup store to it.
type Source interface {
	Latest(ctx context.Context, stationKey string, now time.Time) (relay.Observation, bool, error)
}

// RunnerOptions are the collaborators of a Runner.
type RunnerOptions struct {
	// Config returns the live relay.cwop section.
	Config func() Config
	Source Source
	Sender Sender
	// LoadPasscode returns the passcode for cfg, or "" for none. A missing
	// file is not an error.
	LoadPasscode func(Config) (string, error)
	TimeSynced   func() bool
	Version      string
	Now          func() time.Time
	Logger       *slog.Logger
	Interval     time.Duration
	Tick         time.Duration
}

// Runner drives the plugin: every tick it decides whether a send is due and
// delivers the newest observation. It never sends for a station whose relay
// owner is not "device".
type Runner struct {
	o     RunnerOptions
	track relay.Tracker

	mu         sync.Mutex
	lastBucket time.Time
	next       time.Time
}

// NewRunner builds a runner.
func NewRunner(o RunnerOptions) *Runner {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Interval <= 0 {
		o.Interval = DefaultInterval
	}
	if o.Tick <= 0 {
		o.Tick = DefaultTick
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Runner{o: o}
}

// Run ticks until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.o.Tick)
	defer t.Stop()
	for {
		r.Step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *Runner) plugin(cfg Config) (*Plugin, error) {
	pass := ""
	if ClassifyID(cfg.ID) == IDHam && r.o.LoadPasscode != nil {
		p, err := r.o.LoadPasscode(cfg)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, relay.Errorf(relay.CodeBadID, "the credential file is unusable")
		}
		pass = strings.TrimSpace(p)
	}
	return New(PluginOptions{
		Config: cfg, Passcode: pass, Sender: r.o.Sender, Version: r.o.Version,
		Tracker: &r.track, Now: r.o.Now, Logger: r.o.Logger, TimeSynced: r.o.TimeSynced,
	}), nil
}

// Step runs one decision. Exported for tests.
func (r *Runner) Step(ctx context.Context) {
	cfg := r.o.Config()
	if !cfg.Active() || len(cfg.Problems()) > 0 {
		return
	}
	now := r.o.Now()
	r.mu.Lock()
	wait := now.Before(r.next)
	r.mu.Unlock()
	if wait {
		return
	}
	p, err := r.plugin(cfg)
	if err != nil {
		r.track.Fail(now, relay.CodeOf(err), err.Error(), false)
		return
	}
	// The time guard comes first so an unsynced Pi neither reads nor sends.
	if r.o.TimeSynced != nil && !r.o.TimeSynced() {
		r.track.Fail(now, relay.CodeTimeUnsynced, "the clock is not synced, nothing is sent", false)
		return
	}
	obs, ok, err := r.o.Source.Latest(ctx, cfg.Station, now)
	if err != nil {
		r.track.Fail(now, relay.CodeNoData, p.red.Redact(err.Error()), false)
		return
	}
	r.mu.Lock()
	seen := !obs.Start.After(r.lastBucket)
	r.mu.Unlock()
	if !ok || seen {
		return
	}
	msg, err := p.Map(obs)
	if err != nil {
		r.track.Fail(now, relay.CodeOf(err), err.Error(), false)
		return
	}
	if err := p.Send(ctx, msg); err != nil {
		r.mu.Lock()
		f := r.track.Health().Failures
		d := firstBackoff << min(max(f-1, 0), 4)
		r.next = now.Add(min(d, maxBackoff))
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	r.lastBucket = obs.Start
	r.next = now.Add(r.o.Interval)
	r.mu.Unlock()
}

// Status is the relay page's view of the device-owned relay. It never carries
// the passcode.
type Status struct {
	Owner         string          `json:"owner"`
	Station       string          `json:"station"`
	ID            string          `json:"id"`
	Active        bool            `json:"active"`
	PositionOptIn bool            `json:"position_opt_in"`
	PasscodeSet   bool            `json:"passcode_set"`
	Problems      []relay.Problem `json:"problems"`
	State         string          `json:"state"`
	Health        relay.Health    `json:"health"`
	NextAttempt   *time.Time      `json:"next_attempt"`
}

// State names: off (not device owned), server (relayed by WxAlerts), invalid,
// waiting_for_time, ok, error, idle (nothing sent yet).
func (r *Runner) Status(hasPasscode bool) Status {
	cfg := r.o.Config()
	h := r.track.Health()
	st := Status{
		Owner: cfg.Owner, Station: cfg.Station, ID: cfg.ID, Active: cfg.Active(),
		PositionOptIn: cfg.PositionOptIn, PasscodeSet: hasPasscode,
		Problems: cfg.Problems(), Health: h,
	}
	if st.Problems == nil {
		st.Problems = []relay.Problem{}
	}
	r.mu.Lock()
	if !r.next.IsZero() {
		n := r.next
		st.NextAttempt = &n
	}
	r.mu.Unlock()
	switch {
	case relay.Owner(cfg.Owner).ServerSends():
		st.State = "server"
	case !cfg.Active():
		st.State = "off"
	case len(st.Problems) > 0:
		st.State = "invalid"
	case r.o.TimeSynced != nil && !r.o.TimeSynced():
		st.State = "waiting_for_time"
	case h.LastError != nil && (h.LastSuccess == nil || h.LastError.At.After(*h.LastSuccess)):
		st.State = "error"
	case h.LastSuccess != nil:
		st.State = "ok"
	default:
		st.State = "idle"
	}
	return st
}
