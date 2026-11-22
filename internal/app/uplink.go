package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/wxalerts/pws-gateway/internal/claim"
	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/uplink"
	"github.com/wxalerts/pws-gateway/internal/web"
)

// caFileName is the optional pinned broker CA inside the data directory.
const caFileName = "uplink-ca.pem"

// sdrMissingEvery limits sdr_missing events: the supervisor retries a missing
// dongle every few seconds.
const sdrMissingEvery = time.Hour

// credentialPath resolves the credential file named in the config. A relative
// name lives in the data directory.
func (a *App) credentialPath(cfg config.Uplink) string {
	return claim.CredentialPath(a.deps.DataDir, cfg)
}

// setupUplink builds the uplink when the device is claimed: the config names a
// broker and a device id and the credential file exists. Otherwise it logs
// once at info and leaves the device collecting locally. With UplinkDryRun it
// builds and enqueues messages but never connects, and needs no credential.
func (a *App) setupUplink() {
	cfg := a.deps.Config.Get().Uplink
	if cfg.DeviceID == "" || (cfg.BrokerURL == "" && !a.deps.UplinkDryRun) {
		a.log.Info("device is unclaimed (no broker or device id in the uplink config), collecting locally only")
		return
	}
	opts := uplink.Options{
		DeviceID: cfg.DeviceID, BrokerURL: cfg.BrokerURL,
		Config: a.deps.Config, Store: a.store, Platform: a.deps.Platform, Collector: a.pipeline,
		SDRs: a.sdrList, FlushRollups: a.writer.Flush,
		Now: a.now, Logger: a.log, DryRun: a.deps.UplinkDryRun,
		UpdateState: a.updateState, UpdateCheck: a.updateCheckCmd,
	}
	if !a.deps.UplinkDryRun {
		path := a.credentialPath(cfg)
		if path == "" {
			a.log.Info("device is unclaimed (uplink.credential_file is empty), collecting locally only")
			return
		}
		cred, err := uplink.LoadCredentials(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			a.log.Info("device is unclaimed (credential file not found), collecting locally only", "path", path)
			return
		case err != nil:
			a.log.Error("uplink disabled: credential file is unusable", "path", path, "err", err)
			return
		}
		opts.Credentials = cred
		opts.UserID = cred.UserID
		if ca := filepath.Join(a.deps.DataDir, caFileName); fileExists(ca) {
			opts.CAFile = ca
		}
	}
	key, src, err := uplink.LoadCommandKey(a.deps.DataDir)
	switch {
	case err == nil:
		opts.CommandKey = key
		a.log.Info("command-signing key pinned", "source", src)
	case errors.Is(err, uplink.ErrNoCommandKey):
		a.log.Info("no command-signing key is pinned, server commands will be ignored")
	default:
		a.log.Error("command-signing key is unusable, server commands will be ignored", "err", err)
	}
	u, err := uplink.New(opts)
	if err != nil {
		a.log.Error("uplink disabled", "err", err)
		return
	}
	a.uplink.Store(u)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// sdrList describes the supervised radios for tel. Decoders is left unset: the
// supervisor does not know how many decoders the installed rtl_433 build
// enables.
func (a *App) sdrList() []contract.SDR {
	var out []contract.SDR
	for _, r := range a.deps.Config.Get().Radios {
		if r.Role != config.RoleRTL433 {
			continue
		}
		band := r.BandMHz
		s := contract.SDR{RadioID: r.Serial, Driver: contract.DriverRTL433, BandMHz: &band}
		if sup := a.supervisor(r.Serial); sup != nil {
			n := sup.Status().PacketsLast15m
			s.Packets15m = &n
		}
		out = append(out, s)
	}
	return out
}

// launchUplinkLocked runs the uplink when one is built and Run has started.
// The caller holds upMu.
func (a *App) launchUplinkLocked() {
	u := a.uplink.Load()
	if u == nil || a.runCtx == nil || a.upDone != nil {
		return
	}
	ctx, cancel := context.WithCancel(a.runCtx)
	done := make(chan struct{})
	a.upCancel, a.upDone = cancel, done
	go func() {
		defer close(done)
		if err := u.Run(ctx); err != nil && ctx.Err() == nil {
			a.log.Error("uplink stopped", "err", err)
		}
	}()
}

// startUplink builds and runs the uplink after a claim, without a restart. It
// does nothing when an uplink is already running.
func (a *App) startUplink() {
	a.upMu.Lock()
	defer a.upMu.Unlock()
	if a.uplink.Load() == nil {
		a.setupUplink()
	}
	a.launchUplinkLocked()
}

// stopUplink stops the running uplink, waits for its clean disconnect and
// drops it, so the device is unclaimed as far as the web layer can tell.
func (a *App) stopUplink() {
	a.upMu.Lock()
	defer a.upMu.Unlock()
	if a.upCancel != nil {
		a.upCancel()
		<-a.upDone
	}
	a.upCancel, a.upDone = nil, nil
	a.uplink.Store(nil)
}

// Uplink returns the uplink, or nil while the device is unclaimed.
func (a *App) Uplink() *uplink.Uplink { return a.uplink.Load() }

// uplinkStatus implements the Uplink and Update parts of web.StatusSource.
func (a *App) uplinkStatus() web.UplinkStatus {
	u := a.uplink.Load()
	if u == nil {
		return web.UplinkStatus{}
	}
	st := u.Status()
	return web.UplinkStatus{Connected: st.Connected, Broker: st.Broker, DeviceID: st.DeviceID, LastPublish: st.LastPublish}
}

func (a *App) updateStatus() (web.UpdateStatus, bool) {
	u := a.uplink.Load()
	if u == nil {
		return web.UpdateStatus{}, false
	}
	m := u.UpdateMessage()
	return web.UpdateStatus{Current: m.Current, Target: m.Target, State: m.State, Error: m.Error, TS: m.TS.Format(time.RFC3339)}, true
}

// noteSDRMissing forwards a missing dongle to the uplink as an sdr_missing
// event, at most once an hour per radio.
func (a *App) noteSDRMissing(e rtl433.Event) {
	if u := a.uplink.Load(); u != nil {
		u.EmitThrottled(context.Background(), contract.EventSDRMissing, e.Radio, sdrMissingEvery, map[string]any{"radio": e.Radio})
	}
}
