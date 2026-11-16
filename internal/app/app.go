// Package app builds the runtime graph of the daemon: the store, the
// collector pipeline, one rtl_433 supervisor per enabled radio, and the
// adapters that expose them to the web layer.
//
// Construction and running are separate so that main can hand the adapters to
// the web server before anything starts: New opens the store and builds the
// pipeline, Run drives everything until the context ends.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wxalerts/pws-gateway/internal/collector"
	"github.com/wxalerts/pws-gateway/internal/collector/mapping"
	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/model"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/store"
	"github.com/wxalerts/pws-gateway/internal/uplink"
	"github.com/wxalerts/pws-gateway/internal/version"
	"github.com/wxalerts/pws-gateway/internal/web"
)

// DBFile is the database file name inside the data directory.
const DBFile = "pwsgw.db"

// defaultBandMHz is the band chosen for an auto-created radio entry.
const defaultBandMHz = 433.92

// timeSyncTTL bounds how often the platform is asked about NTP state.
const timeSyncTTL = 5 * time.Second

// Deps are the collaborators of the runtime.
type Deps struct {
	DataDir  string
	Config   *config.Store
	Platform platform.Platform
	Logger   *slog.Logger

	// Rtl433Bin is the executable, default "rtl_433" resolved from PATH.
	Rtl433Bin string
	// ReplayFile, when set, feeds a recorded JSONL file through
	// rtl433.Replay instead of starting rtl_433 (development mode).
	ReplayFile string
	// ReplaySpeed paces the replay: 1 is real time, 0 is as fast as the
	// pipeline reads. ReplayRewriteTime shifts the recorded timestamps so
	// the first line lands now, which makes a stale recording look live.
	ReplaySpeed       float64
	ReplayRewriteTime bool

	// UplinkDryRun builds and enqueues uplink messages but never connects to
	// the broker, logging payload sizes (bench use).
	UplinkDryRun bool

	// Test hooks. Nil means the real thing.
	Start     rtl433.StartFunc
	Now       func() time.Time
	KillGrace time.Duration
}

// App is the built runtime. Call Run exactly once.
type App struct {
	deps     Deps
	log      *slog.Logger
	now      func() time.Time
	store    *store.Store
	writer   *store.Writer
	pipeline *collector.Pipeline
	// uplink is nil until setupUplink finds a claimed device.
	uplink atomic.Pointer[uplink.Uplink]

	// lines merges the output of every supervisor into one Source.
	lines chan rtl433.Line
	wake  chan struct{}

	mu   sync.Mutex
	runs map[string]*radioRun
}

type radioRun struct {
	cfg    config.Radio
	sup    *rtl433.Supervisor
	cancel context.CancelFunc
	done   chan struct{}
}

// New opens the store and builds the pipeline. It starts nothing.
func New(deps Deps) (*App, error) {
	if deps.Config == nil || deps.Platform == nil || deps.DataDir == "" {
		return nil, fmt.Errorf("app: DataDir, Config and Platform are required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Rtl433Bin == "" {
		deps.Rtl433Bin = rtl433.DefaultBinary
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.KillGrace <= 0 {
		deps.KillGrace = killGrace
	}
	a := &App{
		deps: deps, log: deps.Logger, now: deps.Now,
		lines: make(chan rtl433.Line, 1024),
		wake:  make(chan struct{}, 1),
		runs:  map[string]*radioRun{},
	}
	st, err := store.Open(filepath.Join(deps.DataDir, DBFile))
	if err != nil {
		return nil, err
	}
	a.store = st
	a.writer = store.NewWriter(st, store.WriterOptions{Logger: a.log})
	a.pipeline = collector.New(collector.Config{
		Store: st, Writer: a.writer,
		TimeSynced: a.timeSynced(),
		Now:        deps.Now,
		Events:     a.onPipelineEvent,
		Selection:  a.selection,
		Logger:     a.log,
	})
	a.setupUplink()
	return a, nil
}

// selection tells the pipeline which stations and fields to store. It reads
// the live config on every call, so ticking a station in the UI takes effect
// on the next reading without a restart.
func (a *App) selection(st model.Station) (bool, []string) {
	ann, ok := a.deps.Config.Get().Stations[st.Key]
	if !ok || !ann.Selected {
		return false, nil
	}
	return true, ann.EffectiveFields(mapping.DefaultFields(st.Model, st.Fields))
}

// Stations, Readings and Status are the adapters for web.Options.
func (a *App) Stations() web.StationSource { return stationSource{p: a.pipeline, now: a.now} }

// Readings returns the web.ReadingsSource backed by the pipeline.
func (a *App) Readings() web.ReadingsSource { return readingsSource{p: a.pipeline} }

// Status returns the web.StatusSource backed by the pipeline and supervisors.
func (a *App) Status() web.StatusSource {
	return statusSource{NopStatus: web.NopStatus{Version: version.Version}, app: a}
}

// Pipeline exposes the collector for tests and diagnostics.
func (a *App) Pipeline() *collector.Pipeline { return a.pipeline }

// timeSynced wraps the platform's NTP check with a short cache. A platform
// that cannot tell (ErrUnsupported, for example a laptop) counts as synced so
// development machines are not stuck in the unsynced hold.
func (a *App) timeSynced() func() bool {
	var mu sync.Mutex
	var at time.Time
	var val bool
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		if !at.IsZero() && time.Since(at) < timeSyncTTL {
			return val
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ts, err := a.deps.Platform.TimeSync(ctx)
		switch {
		case err == nil:
			val = ts.Synced
		case isUnsupported(err):
			val = true
		default:
			a.log.Warn("time sync check failed", "err", err)
			val = false
		}
		at = time.Now()
		return val
	}
}

// Run drives the runtime until ctx ends, then stops every child process,
// flushes the pipeline and closes the store. It returns after everything has
// stopped. In replay mode the web side keeps serving after the file ends.
func (a *App) Run(ctx context.Context) error {
	defer func() {
		if err := a.store.Close(); err != nil {
			a.log.Warn("close store", "err", err)
		}
	}()

	var wg sync.WaitGroup
	wg.Go(func() {
		if err := a.writer.Run(ctx); err != nil && ctx.Err() == nil {
			a.log.Error("rollup writer stopped", "err", err)
		}
	})

	var src collector.Source = collector.ChanSource(a.lines)
	if a.deps.ReplayFile != "" {
		a.log.Info("replay mode: not starting rtl_433", "file", a.deps.ReplayFile, "speed", a.deps.ReplaySpeed)
		rp := &rtl433.Replay{Radio: "replay", Speed: a.deps.ReplaySpeed, RewriteTime: a.deps.ReplayRewriteTime}
		src = collector.NewReplayFile(ctx, rp, a.deps.ReplayFile)
	} else {
		a.chooseDefaultRadio(ctx)
		a.deps.Config.Subscribe(func(config.Config) {
			select {
			case a.wake <- struct{}{}:
			default:
			}
		})
		wg.Go(func() { a.manageRadios(ctx) })
	}
	if u := a.uplink.Load(); u != nil {
		wg.Go(func() {
			if err := u.Run(ctx); err != nil && ctx.Err() == nil {
				a.log.Error("uplink stopped", "err", err)
			}
		})
	}
	wg.Go(func() {
		if err := a.pipeline.Run(ctx, src); err != nil && ctx.Err() == nil {
			a.log.Error("pipeline stopped", "err", err)
		}
	})
	<-ctx.Done()
	wg.Wait()
	return nil
}

func hz(mhz float64) int64 { return int64(math.Round(mhz * 1e6)) }
