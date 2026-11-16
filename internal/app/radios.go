package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wxalerts/pws-gateway/internal/collector"
	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

func isUnsupported(err error) bool { return errors.Is(err, platform.ErrUnsupported) }

// radioSerial is the identifier the web layer uses for a dongle: the USB
// serial when it has one, else the port name.
func radioSerial(d platform.USBDevice) string {
	if d.Serial != "" {
		return d.Serial
	}
	return d.Port
}

// chooseDefaultRadio adds a radio entry when the config has none and exactly
// one RTL-SDR is attached, so a one-dongle kit works without visiting the
// Radios page. With zero or several dongles it does nothing: guessing which
// of two dongles listens to which band would be wrong half the time.
func (a *App) chooseDefaultRadio(ctx context.Context) {
	if len(a.deps.Config.Get().Radios) > 0 {
		return
	}
	devs, err := a.deps.Platform.USBDevices(ctx)
	if err != nil {
		if !isUnsupported(err) {
			a.log.Warn("could not list USB devices for the default radio", "err", err)
		}
		return
	}
	var sdrs []platform.USBDevice
	for _, d := range devs {
		if d.IsRTLSDR() {
			sdrs = append(sdrs, d)
		}
	}
	if len(sdrs) != 1 {
		a.log.Info("no radios configured and RTL-SDR count is not one, not choosing a default", "detected", len(sdrs))
		return
	}
	serial := radioSerial(sdrs[0])
	if _, err := a.deps.Config.Update(func(c *config.Config) error {
		if len(c.Radios) == 0 {
			c.Radios = []config.Radio{{Serial: serial, Role: config.RoleRTL433, BandMHz: defaultBandMHz}}
		}
		return nil
	}); err != nil {
		a.log.Error("could not save the default radio", "err", err)
		return
	}
	a.log.Info("no radios configured, chose the only detected RTL-SDR as the default: role rtl_433 at 433.92 MHz; change it on the Radios page",
		"serial", serial, "product", sdrs[0].RTLSDRName())
}

func (a *App) supervisor(serial string) *rtl433.Supervisor {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r := a.runs[serial]; r != nil {
		return r.sup
	}
	return nil
}

// manageRadios reconciles supervisors with the config at start and after
// every config save, until ctx ends.
func (a *App) manageRadios(ctx context.Context) {
	defer a.stopAll()
	for {
		a.reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-a.wake:
		}
	}
}

// reconcile starts supervisors for enabled radios that lack one and restarts
// those whose role or band changed. Stopping waits for the child to exit so
// a restarted rtl_433 never fights its predecessor for the USB interface.
func (a *App) reconcile(ctx context.Context) {
	want := map[string]config.Radio{}
	for _, r := range a.deps.Config.Get().Radios {
		if r.Role == config.RoleRTL433 {
			want[r.Serial] = r
		}
	}
	var stale []*radioRun
	a.mu.Lock()
	for serial, run := range a.runs {
		if w, ok := want[serial]; !ok || w != run.cfg {
			stale = append(stale, run)
			delete(a.runs, serial)
		}
	}
	a.mu.Unlock()
	for _, run := range stale {
		a.log.Info("stopping rtl_433 for changed radio config", "serial", run.cfg.Serial)
		run.cancel()
		<-run.done
	}
	if ctx.Err() != nil {
		return
	}
	for serial, r := range want {
		a.mu.Lock()
		_, exists := a.runs[serial]
		a.mu.Unlock()
		if !exists {
			a.startRadio(ctx, r)
		}
	}
}

func (a *App) startRadio(ctx context.Context, r config.Radio) {
	radio := rtl433.Radio{
		ID:          r.Serial,
		FrequencyHz: hz(r.BandMHz),
		Role:        r.Role,
	}
	if a.knownSerial(ctx, r.Serial) {
		radio.Serial = r.Serial
	}
	start := a.deps.Start
	if start == nil {
		start = execStart(a.log, a.deps.KillGrace)
	}
	sup := rtl433.New(rtl433.Config{
		Radio: radio, Binary: a.deps.Rtl433Bin, Start: start,
		Events: a.onRadioEvent, Logger: a.log,
	})
	rctx, cancel := context.WithCancel(ctx)
	run := &radioRun{cfg: r, sup: sup, cancel: cancel, done: make(chan struct{})}
	a.mu.Lock()
	a.runs[r.Serial] = run
	a.mu.Unlock()
	go func() {
		defer close(run.done)
		fwdDone := make(chan struct{})
		go func() {
			defer close(fwdDone)
			for line := range sup.Lines() {
				select {
				case a.lines <- line:
				case <-ctx.Done():
				}
			}
		}()
		if err := sup.Run(rctx); err != nil && rctx.Err() == nil {
			a.log.Error("rtl_433 supervisor stopped", "radio", r.Serial, "err", err)
		}
		<-fwdDone
	}()
}

// knownSerial reports whether serial is a real USB serial string (as opposed
// to the port name used when a dongle has none), so "-d :<serial>" is only
// passed when rtl_433 can match it.
func (a *App) knownSerial(ctx context.Context, serial string) bool {
	devs, err := a.deps.Platform.USBDevices(ctx)
	if err != nil {
		return false
	}
	for _, d := range devs {
		if d.IsRTLSDR() && d.Serial != "" && d.Serial == serial {
			return true
		}
	}
	return false
}

func (a *App) stopAll() {
	a.mu.Lock()
	runs := make([]*radioRun, 0, len(a.runs))
	for _, r := range a.runs {
		runs = append(runs, r)
	}
	a.runs = map[string]*radioRun{}
	a.mu.Unlock()
	for _, r := range runs {
		r.cancel()
	}
	for _, r := range runs {
		<-r.done
	}
}

func (a *App) onRadioEvent(e rtl433.Event) {
	switch e.Type {
	case rtl433.EventStarted:
		a.log.Info("rtl_433 started", "radio", e.Radio, "pid", e.PID)
	case rtl433.EventRestartScheduled:
		a.log.Warn("rtl_433 restart scheduled", "radio", e.Radio, "in", e.Backoff.String(), "reason", errString(e.Err))
	default:
		attrs := []any{"radio", e.Radio, "event", string(e.Type), "err", errString(e.Err)}
		if errors.Is(e.Err, rtl433.ErrNoDevice) {
			a.log.Warn("sdr_missing: "+e.Message, attrs...)
			a.noteSDRMissing(e)
			return
		}
		a.log.Warn(e.Message, attrs...)
	}
}

func (a *App) onPipelineEvent(e collector.Event) {
	if u := a.uplink.Load(); u != nil {
		u.HandleCollectorEvent(e)
	}
	switch e.Type {
	case collector.EventSensorFound:
		a.log.Info("sensor_found", "station", e.Message, "model", stationModel(e))
	case collector.EventSensorLost:
		a.log.Warn("sensor_lost", "station", e.Message)
	case collector.EventStoreError, collector.EventHoldOverflow, collector.EventHoldExpired:
		a.log.Warn(string(e.Type), "message", e.Message, "count", e.Count)
	default:
		a.log.Info(string(e.Type), "count", e.Count, "message", e.Message)
	}
}

func stationModel(e collector.Event) string {
	if e.Station == nil {
		return ""
	}
	return fmt.Sprintf("%s id=%s ch=%s", e.Station.Model, e.Station.ID, strings.TrimSpace(e.Station.Channel))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
