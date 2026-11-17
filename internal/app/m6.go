package app

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"time"

	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/netcfg"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/update"
	"github.com/wxalerts/pws-gateway/internal/uplink"
	"github.com/wxalerts/pws-gateway/internal/version"
	"github.com/wxalerts/pws-gateway/internal/web"
)

// M6Deps are the optional collaborators of the updater and the network
// manager. Anything nil turns that feature off: no BootSlots means no OS
// image updates, no NetManager means the network page reports unavailable.
type M6Deps struct {
	Slots platform.BootSlots
	Net   platform.NetManager
	// ManifestBase overrides update.DefaultManifestBase.
	ManifestBase string
	// AllowInsecureManifest permits http manifests (bench only).
	AllowInsecureManifest bool
	// Restart asks the process to exit cleanly so systemd starts the new
	// binary. nil disables app updates.
	Restart func()
	// Loc is the time zone of the update window. nil means time.Local.
	Loc *time.Location
}

// setupUpdater builds the updater when a root key is pinned. Without one the
// device simply does not self-update.
func (a *App) setupUpdater() {
	m := a.deps.M6
	trust, err := update.LoadTrust(a.deps.DataDir)
	if err != nil {
		if errors.Is(err, update.ErrNoRoots) {
			a.log.Info("no update root keys are pinned, self-update is off")
		} else {
			a.log.Error("self-update is off: root keys are unusable", "err", err)
		}
		return
	}
	o := update.Options{
		DataDir: a.deps.DataDir, DeviceID: a.deps.Config.Get().Uplink.DeviceID,
		Source: &update.HTTPSource{Base: m.ManifestBase, AllowInsecure: m.AllowInsecureManifest}, Trust: trust,
		Version: version.Version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Slots: m.Slots, Health: a.updateHealth, Link: a.linkType, TimeSynced: a.timeSynced(),
		Settings: func() update.Settings {
			u := a.deps.Config.Get().Updates
			return update.Settings{Window: u.Window, Channel: u.Channel}
		},
		Events: func(ctx context.Context, typ string, detail map[string]any) {
			if u := a.uplink.Load(); u != nil {
				u.Emit(ctx, typ, detail)
			}
		},
		OnState: func(update.State) { go a.publishUpdate() },
		Now:     a.now, Loc: m.Loc, Logger: a.log,
	}
	if m.Restart != nil {
		o.Apps = &update.AppSlots{Dir: filepath.Join(a.deps.DataDir, "update", "app")}
		o.Restart = m.Restart
	}
	up, err := update.New(o)
	if err != nil {
		a.log.Error("self-update is off", "err", err)
		return
	}
	a.updater = up
}

// updateState is the contract update message for the uplink.
func (a *App) updateState() contract.Update {
	if a.updater == nil {
		return contract.Update{Current: version.Version, State: contract.UpdateIdle, TS: a.now().UTC().Truncate(time.Second)}
	}
	st := a.updater.Status()
	return contract.Update{Current: st.Current, Target: st.Target, State: st.State, Error: st.Error, TS: st.TS}
}

func (a *App) publishUpdate() {
	if u := a.uplink.Load(); u != nil {
		u.PublishUpdate(context.Background())
	}
}

// linkType is net.link for the update rules: lte, wifi, ethernet or unknown.
func (a *App) linkType(ctx context.Context) string {
	name, err := a.deps.Platform.DefaultRouteInterface(ctx)
	if err != nil {
		return "unknown"
	}
	return uplink.LinkFromInterface(name)
}

// updateHealth is the post-boot check: the uplink is connected (when the
// device is claimed) and a configured radio's decoder is running.
func (a *App) updateHealth(_ context.Context) error {
	if u := a.uplink.Load(); u != nil && !a.deps.UplinkDryRun && !u.Status().Connected {
		return errors.New("mqtt is not connected")
	}
	var want, running int
	for _, r := range a.deps.Config.Get().Radios {
		if r.Role != config.RoleRTL433 {
			continue
		}
		want++
		if sup := a.supervisor(r.Serial); sup != nil && sup.Status().Running {
			running++
		}
	}
	if want > 0 && running == 0 {
		return errors.New("the radio decoder is not running")
	}
	return nil
}

// setupNetcfg builds the network manager when the platform has one.
func (a *App) setupNetcfg() {
	if a.deps.M6.Net == nil {
		return
	}
	b := a.deps.Platform.Board()
	id := b.Serial
	if id == "" {
		id = b.MachineID
	}
	a.netcfg = netcfg.New(netcfg.Options{
		NM: a.deps.M6.Net, DataDir: a.deps.DataDir, Now: a.now, Logger: a.log,
		Fallback: netcfg.FallbackOptions{Identity: id},
	})
}

// Network returns the web.NetworkSource, nil when there is no network manager.
func (a *App) Network() web.NetworkSource {
	if a.netcfg == nil {
		return nil
	}
	return a.netcfg
}

// Updates returns the web.UpdateSource, nil when self-update is off.
func (a *App) Updates() web.UpdateSource {
	if a.updater == nil {
		return nil
	}
	return updateSource{a}
}

type updateSource struct{ a *App }

func (s updateSource) Status() update.State { return s.a.updater.Status() }

// StartCheck runs a check in the background.
func (s updateSource) StartCheck(force bool) error {
	if !s.a.updater.Enabled() {
		return web.ErrUpdateOff
	}
	if s.a.updater.Busy() {
		return web.ErrUpdateBusy
	}
	go func() {
		ctx := s.a.runContext()
		if err := s.a.updater.Check(ctx, force); err != nil && ctx.Err() == nil {
			s.a.log.Info("update check ended", "err", err)
		}
	}()
	return nil
}

func (a *App) runContext() context.Context {
	a.upMu.Lock()
	defer a.upMu.Unlock()
	if a.runCtx != nil {
		return a.runCtx
	}
	return context.Background()
}

// updateCheckCmd is the signed update-check command: check now, in the
// background, within the update window rules.
func (a *App) updateCheckCmd(context.Context) (map[string]any, error) {
	if a.updater == nil || !a.updater.Enabled() {
		return nil, errors.New("self-update is off on this device")
	}
	if a.updater.Busy() {
		return nil, errors.New("an update is already in progress")
	}
	go func() {
		ctx := a.runContext()
		if err := a.updater.Check(ctx, false); err != nil && ctx.Err() == nil {
			a.log.Info("update check ended", "err", err)
		}
	}()
	return map[string]any{"started": true}, nil
}

// runM6 starts the updater and the network manager loops.
func (a *App) runM6(ctx context.Context, start func(func())) {
	if a.updater != nil {
		start(func() { a.updater.Run(ctx) })
	}
	if a.netcfg != nil {
		start(func() { a.netcfg.Run(ctx) })
	}
}
