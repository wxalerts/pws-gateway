package update

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/platform"
)

// Errors returned by Check.
var (
	ErrBusy          = errors.New("update: an update is already in progress")
	ErrClockUnsynced = errors.New("update: the clock is not synced yet")
	ErrDevBuild      = errors.New("update: this is a development build, self-update is off")
)

// Defaults.
const (
	DefaultCheckInterval = 6 * time.Hour
	DefaultDeferFor      = 6 * time.Hour
	DefaultHealthTimeout = 5 * time.Minute
	DefaultHealthEvery   = 10 * time.Second
	DefaultMaxBoots      = 3
	maxJitter            = 30 * time.Minute
)

// Event types published for the update flow (contract event types).
const (
	EventUpdateFailed = "update_failed"
	EventRollback     = "rollback"
)

// Settings are the operator's update preferences (config updates section).
type Settings struct {
	// Window is "HH:MM-HH:MM" local time.
	Window  string
	Channel string
}

// AppInstaller switches the app binary. AppSlots implements it.
type AppInstaller interface {
	Install(ctx context.Context, staged, version string, versionNumber int) error
	Revert(ctx context.Context) error
	Commit()
}

// Options wires the updater. Source, Trust, Version, DataDir, Health, Link,
// Settings and TimeSynced are required; Slots may be nil on a host without
// A/B slots, which turns OS image updates off.
type Options struct {
	DataDir  string
	DeviceID string
	Source   Source
	Trust    Trust
	// Version is the running firmware version, semver.
	Version      string
	GOOS, GOARCH string

	Slots platform.BootSlots
	Apps  AppInstaller
	// Restart asks the daemon to exit so the service manager starts the new
	// binary. Required when Apps is set.
	Restart func()

	// Health is the post-boot check: services up, MQTT connected, radio
	// decoding. nil error means healthy.
	Health     func(ctx context.Context) error
	Link       func(ctx context.Context) string
	TimeSynced func() bool
	Settings   func() Settings
	// Events publishes an event. OnState sees every state change.
	Events  func(ctx context.Context, typ string, detail map[string]any)
	OnState func(State)

	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	Loc   *time.Location

	Downloader    *Downloader
	CheckInterval time.Duration
	DeferFor      time.Duration
	HealthTimeout time.Duration
	HealthEvery   time.Duration
	MaxBoots      int
	Logger        *slog.Logger
}

// Updater runs the update state machine.
type Updater struct {
	o         Options
	log       *slog.Logger
	installed int // version_number of the running firmware, 0 for a dev build

	opMu sync.Mutex // one check or install at a time
	mu   sync.Mutex // guards st
	st   State

	wake chan struct{}
}

// New builds an Updater and loads its persisted state.
func New(o Options) (*Updater, error) {
	if o.Source == nil || o.Health == nil || o.Link == nil || o.Settings == nil || o.TimeSynced == nil || o.DataDir == "" {
		return nil, errors.New("update: Source, Health, Link, Settings, TimeSynced and DataDir are required")
	}
	if o.Apps != nil && o.Restart == nil {
		return nil, errors.New("update: Restart is required with Apps")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Loc == nil {
		o.Loc = time.Local
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	o.CheckInterval = durOr(o.CheckInterval, DefaultCheckInterval)
	o.DeferFor = durOr(o.DeferFor, DefaultDeferFor)
	o.HealthTimeout = durOr(o.HealthTimeout, DefaultHealthTimeout)
	o.HealthEvery = durOr(o.HealthEvery, DefaultHealthEvery)
	if o.MaxBoots <= 0 {
		o.MaxBoots = DefaultMaxBoots
	}
	if o.Downloader == nil {
		o.Downloader = &Downloader{}
	}
	if o.Downloader.Sleep == nil {
		o.Downloader.Sleep = o.sleep
	}
	u := &Updater{o: o, log: o.Logger, wake: make(chan struct{}, 1)}
	if n, err := VersionNumber(o.Version); err == nil {
		u.installed = n
	}
	st, err := loadState(o.DataDir)
	if err != nil {
		return nil, fmt.Errorf("update: load state: %w", err)
	}
	if st.State == "" {
		st.State = StateIdle
		st.TS = o.Now().UTC()
	}
	st.Current = o.Version
	u.st = st
	return u, nil
}

func durOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func (o Options) sleep(ctx context.Context, d time.Duration) error {
	if o.Sleep != nil {
		return o.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Status returns a copy of the current state.
func (u *Updater) Status() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

// set mutates the state, stamps and persists it, then notifies OnState.
func (u *Updater) set(f func(*State)) {
	u.mu.Lock()
	f(&u.st)
	u.st.TS = u.o.Now().UTC().Truncate(time.Second)
	u.st.Current = u.o.Version
	cp := u.st
	u.mu.Unlock()
	if err := saveState(u.o.DataDir, cp); err != nil {
		u.log.Error("update: save state", "err", err)
	}
	if u.o.OnState != nil {
		u.o.OnState(cp)
	}
}

func (u *Updater) emit(ctx context.Context, typ string, detail map[string]any) {
	if u.o.Events != nil {
		u.o.Events(ctx, typ, detail)
	}
}

func strp(s string) *string { return &s }

// CheckNow asks Run to check without waiting for the next interval.
func (u *Updater) CheckNow() {
	select {
	case u.wake <- struct{}{}:
	default:
	}
}

// ---- check and apply ------------------------------------------------------

// Check fetches the manifest, verifies it and, when an update applies and the
// rules allow it now, downloads, stages and switches to it. force skips the
// update window and the deferral back-off (the operator pressed Install now),
// never the signature, expiry or version rules.
func (u *Updater) Check(ctx context.Context, force bool) error {
	if !u.opMu.TryLock() {
		return ErrBusy
	}
	defer u.opMu.Unlock()
	set := u.o.Settings()
	m, dec, err := u.fetchVerified(ctx, set.Channel)
	if err != nil {
		return err
	}
	st := u.Status()

	osBoot, okB := m.Find(KindOSBoot, u.o.GOOS, u.o.GOARCH)
	osRoot, okR := m.Find(KindOSRoot, u.o.GOOS, u.o.GOARCH)
	app, okA := m.Find(KindApp, u.o.GOOS, u.o.GOARCH)
	needOS := u.o.Slots != nil && okB && okR && m.OSVersionNumber > st.OSVersionNumber
	needApp := u.o.Apps != nil && okA && dec != DecisionSame
	if !force && (needOS || needApp) && slices.Contains(st.FailedVersions, m.VersionNumber) {
		u.set(func(s *State) {
			s.State, s.Target, s.Error = StateIdle, nil, nil
			s.Detail = "version " + m.Version + " was rolled back earlier and is skipped until a newer release"
		})
		return nil
	}
	if !needOS && !needApp {
		u.settleIdle()
		return nil
	}
	if needOS && u.o.Link(ctx) == "lte" {
		if !needApp {
			u.setDeferred(m.Version, DeferLTE, "OS image updates wait for an Ethernet or Wi-Fi link", 0)
			return nil
		}
		needOS = false
		u.set(func(s *State) { s.OSDeferred = true })
	}

	if !force && !u.windowOpen(m.Version, set) {
		return nil
	}
	u.set(func(s *State) { s.Failures, s.DeferUntil = 0, time.Time{} })

	if needOS {
		return u.installOS(ctx, m, set.Channel, osBoot, osRoot)
	}
	return u.installApp(ctx, m, set.Channel, app)
}

// settleIdle clears a deferral or a failure once there is nothing to do.
func (u *Updater) settleIdle() {
	switch u.Status().State {
	case StateDeferred, StateFailed, StateIdle:
		u.set(func(s *State) {
			s.State, s.Target, s.Error, s.Detail, s.DeferKind, s.Code = StateIdle, nil, nil, "", "", ""
			s.DeferUntil, s.Failures = time.Time{}, 0
		})
	}
}

func (u *Updater) setDeferred(target, kind, detail string, backoff time.Duration) {
	u.set(func(s *State) {
		s.State, s.Target, s.Error = StateDeferred, strp(target), nil
		s.DeferKind, s.Detail, s.Code = kind, detail, ""
		if backoff > 0 {
			s.DeferUntil = u.o.Now().Add(backoff).UTC()
		}
	})
}

// reject records a manifest rejection as state failed with a code, and an
// update_failed event carrying the same code.
func (u *Updater) reject(ctx context.Context, err error) {
	code := CodeOf(err)
	msg := err.Error()
	u.set(func(s *State) {
		s.State, s.Target, s.Error, s.Code = StateFailed, nil, strp(msg), code
		s.Detail, s.DeferKind = "", ""
	})
	u.emit(ctx, EventUpdateFailed, map[string]any{"error": msg, "code": code})
}

func (u *Updater) fail(ctx context.Context, target, code string, err error) {
	msg := code + ": " + err.Error()
	u.set(func(s *State) {
		s.State, s.Target, s.Error, s.Code = StateFailed, strp(target), strp(msg), code
		s.Detail, s.DeferKind = "", ""
	})
	u.emit(ctx, EventUpdateFailed, map[string]any{"error": msg, "code": code, "target": target})
}

func (u *Updater) stagingDir(m *Manifest) string {
	return filepath.Join(u.o.DataDir, "update", "staging", strconv.Itoa(m.VersionNumber))
}

// download fetches the artifacts into staging, resuming partial files. On
// ErrDeferred it records the deferral and returns it.
func (u *Updater) download(ctx context.Context, m *Manifest, channel string, arts ...Artifact) ([]string, error) {
	dir := u.stagingDir(m)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	u.set(func(s *State) {
		s.State, s.Target, s.Error, s.Code = StateDownloading, strp(m.Version), nil, ""
		s.Detail, s.DeferKind, s.Progress = "", "", 0
	})
	var total, done int64
	for i := range arts {
		total += arts[i].Size
	}
	var paths []string
	for _, a := range arts { //nolint:gocritic // small list, copies are fine here
		ref, err := u.o.Source.ArtifactURL(channel, a.URL)
		if err != nil {
			return nil, err
		}
		final := filepath.Join(dir, a.Name)
		if f, oerr := os.Open(final); oerr == nil {
			verr := verifyWhole(f, a)
			_ = f.Close()
			if verr == nil {
				done += a.Size
				paths = append(paths, final)
				continue
			}
			_ = os.Remove(final)
		}
		fails := u.Status().Failures
		base := done
		err = u.o.Downloader.Fetch(ctx, a, ref, final+".part", &fails, func(n, _ int64) {
			u.mu.Lock()
			u.st.Progress = int((base + n) * 100 / total)
			u.mu.Unlock()
		})
		u.set(func(s *State) { s.Failures = fails })
		if err != nil {
			if errors.Is(err, ErrDeferred) {
				u.setDeferred(m.Version, DeferDownload, "the download kept failing, it will be retried later", u.o.DeferFor)
			}
			return nil, err
		}
		if err := os.Rename(final+".part", final); err != nil {
			return nil, err
		}
		done += a.Size
		paths = append(paths, final)
	}
	return paths, nil
}

func (u *Updater) installApp(ctx context.Context, m *Manifest, channel string, app Artifact) error {
	paths, err := u.download(ctx, m, channel, app)
	if err != nil {
		return u.downloadFailed(ctx, m, err)
	}
	u.set(func(s *State) { s.State, s.Detail = StateStaged, "" })
	if err := u.o.Apps.Install(ctx, paths[0], m.Version, m.VersionNumber); err != nil {
		u.fail(ctx, m.Version, "install_failed", err)
		return err
	}
	u.set(func(s *State) {
		s.Pending = &Pending{Kind: "app", Version: m.Version, VersionNumber: m.VersionNumber, From: u.o.Version}
		s.State = StateRebooting
	})
	u.o.Restart()
	return nil
}

func (u *Updater) installOS(ctx context.Context, m *Manifest, channel string, boot, root Artifact) error {
	paths, err := u.download(ctx, m, channel, boot, root)
	if err != nil {
		return u.downloadFailed(ctx, m, err)
	}
	u.set(func(s *State) { s.State, s.Detail = StateStaged, "" })
	bs, err := u.o.Slots.BootState(ctx)
	if err != nil {
		u.fail(ctx, m.Version, "boot_state_unavailable", err)
		return err
	}
	if bs.Tryboot || bs.Running != bs.Default {
		err := errors.New("the previous trial boot has not been confirmed or rolled back")
		u.fail(ctx, m.Version, "slot_busy", err)
		return err
	}
	target := bs.Running.Other()
	for i, part := range []platform.SlotPart{platform.PartBoot, platform.PartRoot} {
		art := []Artifact{boot, root}[i]
		if err := u.writeSlot(ctx, target, part, paths[i], art); err != nil {
			u.fail(ctx, m.Version, "slot_write_failed", err)
			return err
		}
	}
	u.set(func(s *State) {
		s.Pending = &Pending{
			Kind: "os", Version: m.Version, VersionNumber: m.VersionNumber, OSVersionNum: m.OSVersionNumber,
			From: u.o.Version, Slot: target,
		}
		s.State = StateRebooting
	})
	if err := u.o.Slots.RebootInto(ctx, target); err != nil {
		u.set(func(s *State) { s.Pending = nil })
		u.fail(ctx, m.Version, "reboot_failed", err)
		return err
	}
	return nil
}

func (u *Updater) writeSlot(ctx context.Context, slot platform.Slot, part platform.SlotPart, path string, art Artifact) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s is not gzip: %w", art.Name, err)
	}
	defer func() { _ = zr.Close() }()
	return u.o.Slots.WriteSlot(ctx, slot, part, zr, art.RawSize)
}

func (u *Updater) downloadFailed(ctx context.Context, m *Manifest, err error) error {
	switch {
	case errors.Is(err, ErrDeferred), ctx.Err() != nil:
		return nil // already recorded as deferred, or shutting down
	}
	u.fail(ctx, m.Version, "download_failed", err)
	return err
}

// ---- start up: confirm or roll back --------------------------------------

// Resume runs once at start. With no pending update it only records the
// running version. With one, it is the post-switch check: wait for the health
// check, then commit, or roll back.
func (u *Updater) Resume(ctx context.Context) {
	u.opMu.Lock()
	defer u.opMu.Unlock()
	st := u.Status()
	p := st.Pending
	if p == nil {
		return
	}
	u.set(func(s *State) { s.Pending.Boots++ })
	p = u.Status().Pending
	switch p.Kind {
	case "os":
		u.resumeOS(ctx, p)
	case "app":
		u.resumeApp(ctx, p)
	default:
		u.set(func(s *State) { s.Pending = nil })
	}
}

func (u *Updater) resumeOS(ctx context.Context, p *Pending) {
	bs, err := u.o.Slots.BootState(ctx)
	if err != nil {
		u.log.Error("update: boot state unavailable after an OS update", "err", err)
		return
	}
	if bs.Running != p.Slot {
		// The firmware went back to the old slot: the trial never passed.
		reason := p.Reason
		if reason == "" {
			reason = "the new system did not finish starting and the bootloader returned to the previous one"
		}
		u.finishRollback(ctx, p, reason)
		return
	}
	if p.Boots > u.o.MaxBoots {
		u.abandonOS(ctx, "the new system restarted too many times without becoming healthy")
		return
	}
	if err := u.waitHealthy(ctx); err != nil {
		if ctx.Err() != nil {
			return
		}
		u.abandonOS(ctx, "health check failed: "+err.Error())
		return
	}
	if err := u.o.Slots.Commit(ctx); err != nil {
		u.abandonOS(ctx, "could not commit the new slot: "+err.Error())
		return
	}
	u.markGood(p)
}

// abandonOS leaves the trial: it records why, then restarts into the default
// slot, where finishRollback reports it.
func (u *Updater) abandonOS(ctx context.Context, reason string) {
	u.set(func(s *State) { s.Pending.Reason = reason })
	u.log.Error("update: rolling back", "reason", reason)
	if err := u.o.Slots.RebootDefault(ctx); err != nil {
		u.log.Error("update: reboot into the previous slot failed", "err", err)
	}
}

func (u *Updater) resumeApp(ctx context.Context, p *Pending) {
	if u.installed != p.VersionNumber {
		// The new binary was installed but the old one is running, so the
		// hand over to the new one did not happen.
		u.rollbackApp(ctx, p, "the new binary did not start")
		return
	}
	if p.Boots > u.o.MaxBoots {
		u.rollbackApp(ctx, p, "the new version restarted too many times without becoming healthy")
		return
	}
	if err := u.waitHealthy(ctx); err != nil {
		if ctx.Err() != nil {
			return
		}
		u.rollbackApp(ctx, p, "health check failed: "+err.Error())
		return
	}
	u.markGood(p)
}

func (u *Updater) rollbackApp(ctx context.Context, p *Pending, reason string) {
	if err := u.o.Apps.Revert(ctx); err != nil {
		u.log.Error("update: revert app slot", "err", err)
	}
	u.finishRollback(ctx, p, reason)
	u.o.Restart()
}

func (u *Updater) waitHealthy(ctx context.Context) error {
	deadline := u.o.Now().Add(u.o.HealthTimeout)
	for {
		err := u.o.Health(ctx)
		if err == nil {
			return nil
		}
		if !u.o.Now().Add(u.o.HealthEvery).Before(deadline) {
			return err
		}
		if serr := u.o.sleep(ctx, u.o.HealthEvery); serr != nil {
			return serr
		}
	}
}

func (u *Updater) markGood(p *Pending) {
	if p.Kind == "app" && u.o.Apps != nil {
		u.o.Apps.Commit()
	}
	_ = os.RemoveAll(filepath.Join(u.o.DataDir, "update", "staging"))
	u.set(func(s *State) {
		s.State, s.Target, s.Error, s.Code, s.Pending = StateGood, nil, nil, "", nil
		s.Detail, s.DeferKind, s.Failures, s.DeferUntil = "", "", 0, time.Time{}
		if p.Kind == "os" {
			s.OSVersionNumber, s.OSDeferred = p.OSVersionNum, false
		}
	})
}

func (u *Updater) finishRollback(ctx context.Context, p *Pending, reason string) {
	_ = os.RemoveAll(filepath.Join(u.o.DataDir, "update", "staging"))
	u.set(func(s *State) {
		s.State, s.Target, s.Error, s.Code, s.Pending = StateRolledBack, nil, strp(reason), "", nil
		s.Detail, s.DeferKind = "", ""
		s.FailedVersions = append(s.FailedVersions, p.VersionNumber)
	})
	u.emit(ctx, EventRollback, map[string]any{"from": p.Version, "to": p.From, "error": reason, "kind": p.Kind})
}

// ---- scheduling -----------------------------------------------------------

// jitter is a stable per-device delay up to maxJitter, so a fleet does not
// hit the manifest service together.
func (u *Updater) jitter() time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(u.o.DeviceID))
	return time.Duration(h.Sum32()) % maxJitter
}

// NextWake returns when Run should look again: the regular interval, pulled
// earlier by a deferral back-off or the opening of the update window.
func (u *Updater) NextWake(now time.Time) time.Time {
	next := now.Add(u.o.CheckInterval)
	st := u.Status()
	if st.State == StateDeferred {
		switch st.DeferKind {
		case DeferDownload:
			if st.DeferUntil.After(now) && st.DeferUntil.Before(next) {
				next = st.DeferUntil
			}
		case DeferWindow:
			if w, err := ParseWindow(u.o.Settings().Window); err == nil {
				if open := w.NextStart(now, u.o.Loc).Add(u.jitter()); open.After(now) && open.Before(next) {
					next = open
				}
			}
		}
	}
	return next
}

// Run resumes a pending update, then checks on a schedule until ctx ends.
func (u *Updater) Run(ctx context.Context) {
	u.Resume(ctx)
	if err := u.o.sleep(ctx, u.jitter()); err != nil {
		return
	}
	for {
		if err := u.Check(ctx, false); err != nil && ctx.Err() == nil {
			switch {
			case errors.Is(err, ErrDevBuild):
				u.log.Info("update: development build, self-update is off")
				return
			case errors.Is(err, ErrBusy), errors.Is(err, ErrClockUnsynced):
				u.log.Debug("update: check skipped", "reason", err)
			case CodeOf(err) != "":
				u.log.Warn("update: manifest rejected", "err", err)
			default:
				u.log.Warn("update: check failed", "err", err)
			}
		}
		wait := max(u.NextWake(u.o.Now()).Sub(u.o.Now()), time.Minute)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-u.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

// windowOpen reports whether an update may start now. When it may not, it
// records the reason as a deferral.
func (u *Updater) windowOpen(target string, set Settings) bool {
	w, err := ParseWindow(set.Window)
	if err != nil {
		u.log.Warn("update: bad window, treating it as always open", "err", err)
	} else if !w.Contains(u.o.Now(), u.o.Loc) {
		u.setDeferred(target, DeferWindow, "waiting for the update window "+set.Window, 0)
		return false
	}
	return !u.o.Now().Before(u.Status().DeferUntil)
}

// fetchVerified applies the pre-flight guards, fetches the bundle and runs
// the signature, expiry and version rules.
func (u *Updater) fetchVerified(ctx context.Context, channel string) (*Manifest, Decision, error) {
	switch {
	case u.installed == 0:
		return nil, 0, ErrDevBuild
	case u.Status().Pending != nil:
		return nil, 0, ErrBusy
	case !u.o.TimeSynced():
		return nil, 0, ErrClockUnsynced
	}
	bundle, err := u.o.Source.Fetch(ctx, channel)
	if err != nil {
		// A server or network problem is not a failed update: keep the state.
		u.set(func(s *State) { s.LastCheck = u.o.Now().UTC() })
		return nil, 0, err
	}
	m, dec, err := u.o.Trust.Verify(bundle, u.o.Now(), u.installed)
	if err != nil {
		u.reject(ctx, err)
		return nil, 0, err
	}
	u.set(func(s *State) { s.LastCheck = u.o.Now().UTC() })
	return m, dec, nil
}

// Enabled reports whether this build can self-update. A development build
// (version "dev") cannot.
func (u *Updater) Enabled() bool { return u.installed != 0 }

// Busy reports whether a check or install is running.
func (u *Updater) Busy() bool {
	if u.opMu.TryLock() {
		u.opMu.Unlock()
		return false
	}
	return true
}
