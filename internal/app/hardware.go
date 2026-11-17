package app

import (
	"context"
	"fmt"
	"time"

	"github.com/wxalerts/pws-gateway/internal/bootchange"
	"github.com/wxalerts/pws-gateway/internal/collector"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/chrony"
)

// Intervals of the direct-attached hardware loops.
const (
	bootExpireEvery   = 30 * time.Second
	rtcCheckEvery     = 10 * time.Minute
	lteFeedEvery      = time.Minute
	hardwareCallLimit = 10 * time.Second
)

// LTESocket is the chronyd "refclock SOCK" path the LTE network time is
// written to; deploy/chrony/pwsgw.conf declares the same path.
const LTESocket = "/run/pwsgw-chrony/lte.sock"

// setupHardware builds the boot-change machine. The current I2C setting is
// read from the platform; a platform that cannot tell starts as disabled.
func (a *App) setupHardware() error {
	ctx, cancel := context.WithTimeout(context.Background(), hardwareCallLimit)
	defer cancel()
	cur := false
	if st, err := a.deps.Platform.I2C(ctx); err == nil {
		cur = st.Enabled
	}
	m, err := bootchange.New(a.deps.Platform, a.deps.DataDir, cur, a.deps.BootWindow, a.now)
	if err != nil {
		return err
	}
	a.boot = m
	return nil
}

// Boot returns the boot configuration commit-confirm machine for the web
// layer.
func (a *App) Boot() *bootchange.Manager { return a.boot }

// runHardware drives the loops that need the platform's modem, I2C and clock
// facilities. It returns when ctx ends.
func (a *App) runHardware(ctx context.Context) {
	go a.expireBootChanges(ctx)
	go a.keepRTCExclusive(ctx)
	go a.feedLTETime(ctx)
	(&collector.PressureSource{
		Find: a.findBarometers,
		Sink: a.pipeline.IngestReading,
		Now:  a.now,
		Log:  a.log,
	}).Run(ctx)
}

func (a *App) expireBootChanges(ctx context.Context) {
	t := time.NewTicker(bootExpireEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if done, err := a.boot.Expire(); err != nil {
				a.log.Error("boot change rollback failed", "err", err)
			} else if done {
				a.log.Warn("boot change was not confirmed in time and was rolled back")
			}
		}
	}
}

// keepRTCExclusive disables fake-hwclock once a DS3231 answers. Both keep a
// clock across reboots; with an RTC the fake one would only add a stale
// time. It is idempotent and re-checked, because an OS update can bring the
// service back.
func (a *App) keepRTCExclusive(ctx context.Context) {
	for {
		a.disableFakeHwclockIfRTC(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(rtcCheckEvery):
		}
	}
}

func (a *App) disableFakeHwclockIfRTC(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	rtc, err := a.deps.Platform.RTC(cctx)
	if err != nil || rtc == nil {
		return
	}
	off, err := a.deps.Platform.FakeHwclockDisabled(cctx)
	if err != nil || off {
		return
	}
	if err := a.deps.Platform.DisableFakeHwclock(cctx); err != nil {
		a.log.Warn("could not disable fake-hwclock", "err", err)
		return
	}
	a.log.Info("a DS3231 is present: fake-hwclock disabled")
}

// feedLTETime offers the carrier's network time to chrony as a source.
func (a *App) feedLTETime(ctx context.Context) {
	f := &chrony.SockFeeder{Path: LTESocket}
	t := time.NewTicker(lteFeedEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cctx, cancel := context.WithTimeout(ctx, hardwareCallLimit)
		m, err := a.deps.Platform.Modem(cctx)
		cancel()
		if err != nil || m == nil || m.NetworkTime == nil {
			continue
		}
		now := time.Now()
		if err := f.Send(now, m.NetworkTime.Sub(now)); err != nil {
			a.log.Debug("lte time not delivered to chrony", "err", err)
		}
	}
}

// barometer is one BMP280 as a collector.PressureSensor.
type barometer struct {
	plat platform.Platform
	addr int
}

func (b barometer) ID() string { return fmt.Sprintf("i2c1-0x%02x", b.addr) }

func (b barometer) ReadHPa(ctx context.Context) (float64, error) {
	cctx, cancel := context.WithTimeout(ctx, hardwareCallLimit)
	defer cancel()
	r, err := b.plat.BMP280(cctx, b.addr)
	return r.StationHPa, err
}

func (a *App) findBarometers(ctx context.Context) []collector.PressureSensor {
	st, err := a.deps.Platform.I2C(ctx)
	if err != nil {
		return nil
	}
	var out []collector.PressureSensor
	for _, d := range st.Devices {
		if d.Kind == platform.I2CKindBMP280 {
			out = append(out, barometer{plat: a.deps.Platform, addr: d.Addr})
		}
	}
	return out
}
