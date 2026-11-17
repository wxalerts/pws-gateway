package update_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/fake"
	"github.com/wxalerts/pws-gateway/internal/update"
	"github.com/wxalerts/pws-gateway/internal/update/updatetest"
)

// clock is a fake clock: Sleep advances it instead of waiting.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
	return nil
}
func (c *clock) Set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

type event struct {
	Type   string
	Detail map[string]any
}

type fakeApps struct {
	installed []string
	reverts   int
	commits   int
	installEr error
}

func (a *fakeApps) Install(_ context.Context, staged, version string, _ int) error {
	if a.installEr != nil {
		return a.installEr
	}
	if _, err := os.Stat(staged); err != nil {
		return err
	}
	a.installed = append(a.installed, version)
	return nil
}
func (a *fakeApps) Revert(context.Context) error { a.reverts++; return nil }
func (a *fakeApps) Commit()                      { a.commits++ }

// harness is one device plus the manifest service it talks to.
type harness struct {
	t     *testing.T
	clk   *clock
	dir   string
	srv   *httptest.Server
	slots *fake.Slots
	apps  *fakeApps

	mu          sync.Mutex
	files       map[string][]byte // path under /stable/ to body
	artHits     int
	artStatus   int // when non-zero, artifact requests answer this
	healthErr   error
	link        string
	window      string
	synced      bool
	events      []event
	states      []update.State
	restarts    int
	maxFailures int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t: t, clk: &clock{t: updatetest.Epoch}, dir: t.TempDir(),
		slots: fake.NewSlots(), apps: &fakeApps{}, files: map[string][]byte{},
		link: "ethernet", window: "00:00-00:00", synced: true, maxFailures: 3,
	}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		body, ok := h.files[strings.TrimPrefix(r.URL.Path, "/stable/")]
		isArt := !strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/stable/"), "manifest") &&
			!strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/stable/"), "delegation")
		status := h.artStatus
		if isArt {
			h.artHits++
		}
		h.mu.Unlock()
		if isArt && status != 0 {
			http.Error(w, "boom", status)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "f", time.Time{}, strings.NewReader(string(body)))
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) publish(b update.Bundle, arts map[string][]byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files["manifest.json"], h.files["manifest.json.sig"] = b.Manifest, b.ManifestSig
	h.files["delegation.json"], h.files["delegation.json.sig"] = b.Delegation, b.DelegationSig
	for k, v := range arts {
		h.files[k] = v
	}
}

// updater builds an Updater for a process running version v.
func (h *harness) updater(v string) *update.Updater {
	h.t.Helper()
	u, err := update.New(update.Options{
		DataDir: h.dir, DeviceID: "d_test", Version: v, GOOS: "linux", GOARCH: "arm64",
		Source: &update.HTTPSource{Base: h.srv.URL, AllowInsecure: true}, Trust: updatetest.Trust(),
		Slots: h.slots, Apps: h.apps, Restart: func() { h.mu.Lock(); h.restarts++; h.mu.Unlock() },
		Health:     func(context.Context) error { h.mu.Lock(); defer h.mu.Unlock(); return h.healthErr },
		Link:       func(context.Context) string { h.mu.Lock(); defer h.mu.Unlock(); return h.link },
		TimeSynced: func() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.synced },
		Settings: func() update.Settings {
			h.mu.Lock()
			defer h.mu.Unlock()
			return update.Settings{Window: h.window, Channel: "stable"}
		},
		Events: func(_ context.Context, typ string, d map[string]any) {
			h.mu.Lock()
			h.events = append(h.events, event{typ, d})
			h.mu.Unlock()
		},
		OnState: func(s update.State) { h.mu.Lock(); h.states = append(h.states, s); h.mu.Unlock() },
		Now:     h.clk.Now, Sleep: h.clk.Sleep, Loc: time.UTC,
		Downloader: &update.Downloader{MaxFailures: h.maxFailures},
		DeferFor:   time.Hour, HealthTimeout: time.Minute, HealthEvery: 10 * time.Second,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return u
}

func (h *harness) eventTypes() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, e := range h.events {
		out = append(out, e.Type)
	}
	return out
}

func (h *harness) sawState(s string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.ContainsFunc(h.states, func(x update.State) bool { return x.State == s })
}

// release builds a manifest for v with an app artifact and optionally an OS
// image (osNum > 0).
func release(v string, osNum int) (update.Bundle, map[string][]byte) {
	app := []byte("#!pwsgw " + v)
	arts := []update.Artifact{updatetest.Artifact(update.KindApp, "pwsgw-linux-arm64", "pwsgw-linux-arm64", app, 8)}
	files := map[string][]byte{"pwsgw-linux-arm64": app}
	if osNum > 0 {
		for _, k := range []struct{ kind, name, raw string }{
			{update.KindOSBoot, "os-boot.img.gz", strings.Repeat("BOOT", 300)},
			{update.KindOSRoot, "os-root.img.gz", strings.Repeat("ROOT", 900)},
		} {
			gz := updatetest.Gzip([]byte(k.raw))
			a := updatetest.Artifact(k.kind, k.name, k.name, gz, 64)
			a.RawSize = int64(len(k.raw))
			arts = append(arts, a)
			files[k.name] = gz
		}
	}
	m := updatetest.Manifest(v, arts...)
	m.OSVersionNumber = osNum
	return updatetest.StandardBundle(m), files
}

func TestUpdateOutsideWindowIsNotStartedInsideIs(t *testing.T) {
	h := newHarness(t)
	b, files := release("1.3.0", 0)
	h.publish(b, files)
	h.window = "02:00-05:00"
	u := h.updater("1.2.3")

	h.clk.Set(time.Date(2026, 11, 17, 22, 0, 0, 0, time.UTC)) // 22:00
	if err := u.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	st := u.Status()
	if st.State != update.StateDeferred || st.DeferKind != update.DeferWindow || st.Target == nil || *st.Target != "1.3.0" {
		t.Fatalf("state = %+v", st)
	}
	if h.artHits != 0 || len(h.apps.installed) != 0 {
		t.Fatal("update started outside the window")
	}
	// The scheduler wakes when the window opens, not at the next interval.
	wake := u.NextWake(h.clk.Now())
	if wake.Hour() != 2 || wake.Day() != 18 {
		t.Fatalf("next wake %v", wake)
	}

	h.clk.Set(time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC)) // 03:00, inside
	if err := u.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if len(h.apps.installed) != 1 || h.apps.installed[0] != "1.3.0" || h.restarts != 1 {
		t.Fatalf("installed=%v restarts=%d state=%+v", h.apps.installed, h.restarts, u.Status())
	}
	if u.Status().State != update.StateRebooting || u.Status().Pending == nil {
		t.Fatalf("state = %+v", u.Status())
	}
}

func TestForceIgnoresWindow(t *testing.T) {
	h := newHarness(t)
	b, files := release("1.3.0", 0)
	h.publish(b, files)
	h.window = "02:00-05:00"
	u := h.updater("1.2.3")
	if err := u.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if len(h.apps.installed) != 1 {
		t.Fatal("forced update did not run")
	}
}

// On an LTE link an OS image update is deferred and an app update proceeds.
func TestLTEDefersOSImageAndAppProceeds(t *testing.T) {
	h := newHarness(t)
	h.link = "lte"
	t.Run("app proceeds alongside a deferred image", func(t *testing.T) {
		b, files := release("1.3.0", 5)
		h.publish(b, files)
		u := h.updater("1.2.3")
		if err := u.Check(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(h.apps.installed, []string{"1.3.0"}) {
			t.Fatalf("app not installed: %v", h.apps.installed)
		}
		if len(h.slots.Written) != 0 || len(h.slots.Calls) != 0 {
			t.Fatalf("OS image touched over LTE: %v", h.slots.Calls)
		}
		if !u.Status().OSDeferred {
			t.Fatal("the deferred OS image is not recorded")
		}
	})
	t.Run("image only is deferred, not failed", func(t *testing.T) {
		h := newHarness(t)
		h.link = "lte"
		m := updatetest.Manifest("1.2.3") // same app version, newer image
		_, files := release("1.2.3", 7)
		var arts []update.Artifact
		for _, a := range func() update.Manifest {
			b, _ := release("1.2.3", 7)
			mm, _ := update.ParseManifest(b.Manifest)
			return *mm
		}().Artifacts {
			if a.Kind != update.KindApp {
				arts = append(arts, a)
			}
		}
		m.Artifacts, m.OSVersionNumber = arts, 7
		h.publish(updatetest.StandardBundle(m), files)
		u := h.updater("1.2.3")
		if err := u.Check(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		st := u.Status()
		if st.State != update.StateDeferred || st.DeferKind != update.DeferLTE {
			t.Fatalf("state = %+v", st)
		}
		if h.artHits != 0 || len(h.slots.Calls) != 0 {
			t.Fatal("image was downloaded or written over LTE")
		}
		// On Wi-Fi the same manifest installs.
		h.link = "wifi"
		if err := u.Check(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(h.slots.Calls[len(h.slots.Calls)-1:], []string{"reboot-into b"}) {
			t.Fatalf("calls = %v", h.slots.Calls)
		}
	})
}

func osRelease(h *harness) *update.Updater {
	b, files := release("1.3.0", 5)
	h.publish(b, files)
	return h.updater("1.2.3")
}

func TestOSUpdateStagesBootsAndCommitsOnHealthy(t *testing.T) {
	h := newHarness(t)
	u := osRelease(h)
	if err := u.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if string(h.slots.Written["b/boot"]) != strings.Repeat("BOOT", 300) || string(h.slots.Written["b/root"]) != strings.Repeat("ROOT", 900) {
		t.Fatal("slot b does not hold the decompressed images")
	}
	if !h.sawState(update.StateDownloading) || !h.sawState(update.StateStaged) || !h.sawState(update.StateRebooting) {
		t.Fatalf("states seen: %+v", h.states)
	}
	// Reboot into the trial, new process runs 1.3.0.
	h.slots.Boot()
	u2 := h.updater("1.3.0")
	u2.Resume(context.Background())
	st := u2.Status()
	if st.State != update.StateGood || st.OSVersionNumber != 5 || st.Pending != nil {
		t.Fatalf("state = %+v", st)
	}
	bs, _ := h.slots.BootState(context.Background())
	if bs.Default != platform.SlotB || bs.Running != platform.SlotB {
		t.Fatalf("boot state %+v", bs)
	}
	if !slices.Contains(h.slots.Calls, "commit b") {
		t.Fatal("slot was not committed")
	}
	if _, err := os.Stat(filepath.Join(h.dir, "update", "staging")); err == nil {
		t.Fatal("staging not cleaned")
	}
}

// A failed post-boot health check rolls back automatically, the update topic
// shows rolled_back and a rollback event is published. The A/B mechanics are
// the platform's; this is the whole decision chain above them.
func TestOSHealthFailureRollsBack(t *testing.T) {
	h := newHarness(t)
	u := osRelease(h)
	if err := u.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	h.slots.Boot()
	h.healthErr = errors.New("mqtt: connection refused")
	u2 := h.updater("1.3.0")
	u2.Resume(context.Background())
	if !h.slots.RebootRequested() || h.slots.Calls[len(h.slots.Calls)-1] != "reboot-default" {
		t.Fatalf("no rollback restart: %v", h.slots.Calls)
	}
	if slices.Contains(h.slots.Calls, "commit b") {
		t.Fatal("unhealthy slot was committed")
	}
	// The firmware boots the default slot (a) again, old version runs.
	h.slots.Boot()
	if bs, _ := h.slots.BootState(context.Background()); bs.Running != platform.SlotA {
		t.Fatalf("running %v", bs.Running)
	}
	u3 := h.updater("1.2.3")
	u3.Resume(context.Background())
	st := u3.Status()
	if st.State != update.StateRolledBack || st.Error == nil || !strings.Contains(*st.Error, "mqtt: connection refused") {
		t.Fatalf("state = %+v", st)
	}
	if !h.sawState(update.StateRolledBack) || !slices.Contains(h.eventTypes(), "rollback") {
		t.Fatalf("states=%v events=%v", h.states, h.eventTypes())
	}
	// The bad version is not tried again by the scheduler.
	if err := u3.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := u3.Status(); got.State == update.StateDownloading || len(h.slots.Calls) > 6 {
		t.Fatalf("retried a rolled back version: %+v calls=%v", got, h.slots.Calls)
	}
}

// If the new slot never boots at all the firmware returns to the old one
// without any help, and the old version reports the rollback.
func TestOSTrialThatNeverBootedIsReportedAsRollback(t *testing.T) {
	h := newHarness(t)
	u := osRelease(h)
	if err := u.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	h.slots.DropTrial()
	h.slots.Boot()
	u2 := h.updater("1.2.3")
	u2.Resume(context.Background())
	if u2.Status().State != update.StateRolledBack || !slices.Contains(h.eventTypes(), "rollback") {
		t.Fatalf("state=%+v events=%v", u2.Status(), h.eventTypes())
	}
}

// A new system that keeps restarting without reaching a health verdict is
// abandoned after MaxBoots, even if it would be healthy the next time.
func TestOSNewSlotRestartLoopRollsBack(t *testing.T) {
	h := newHarness(t)
	u := osRelease(h)
	if err := u.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	h.slots.Boot()
	h.healthErr = errors.New("still starting")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for range 3 { // each start is cut short before the health verdict
		h.updater("1.3.0").Resume(canceled)
	}
	h.healthErr = nil
	h.updater("1.3.0").Resume(context.Background())
	if h.slots.Calls[len(h.slots.Calls)-1] != "reboot-default" || slices.Contains(h.slots.Calls, "commit b") {
		t.Fatalf("calls = %v", h.slots.Calls)
	}
}

func TestAppUpdateGoodAndRollback(t *testing.T) {
	t.Run("good", func(t *testing.T) {
		h := newHarness(t)
		b, files := release("1.3.0", 0)
		h.publish(b, files)
		if err := h.updater("1.2.3").Check(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		u2 := h.updater("1.3.0") // the restarted daemon runs the new binary
		u2.Resume(context.Background())
		if u2.Status().State != update.StateGood || h.apps.commits != 1 || h.apps.reverts != 0 {
			t.Fatalf("state=%+v commits=%d reverts=%d", u2.Status(), h.apps.commits, h.apps.reverts)
		}
	})
	t.Run("health check fails", func(t *testing.T) {
		h := newHarness(t)
		b, files := release("1.3.0", 0)
		h.publish(b, files)
		if err := h.updater("1.2.3").Check(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		h.healthErr = errors.New("no mqtt")
		u2 := h.updater("1.3.0")
		u2.Resume(context.Background())
		if u2.Status().State != update.StateRolledBack || h.apps.reverts != 1 || h.restarts != 2 {
			t.Fatalf("state=%+v reverts=%d restarts=%d", u2.Status(), h.apps.reverts, h.restarts)
		}
		if !slices.Contains(h.eventTypes(), "rollback") {
			t.Fatal("no rollback event")
		}
	})
	t.Run("old binary still running means the handover failed", func(t *testing.T) {
		h := newHarness(t)
		b, files := release("1.3.0", 0)
		h.publish(b, files)
		if err := h.updater("1.2.3").Check(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		u2 := h.updater("1.2.3")
		u2.Resume(context.Background())
		if u2.Status().State != update.StateRolledBack || h.apps.reverts != 1 {
			t.Fatalf("state=%+v", u2.Status())
		}
	})
}

// Every rejection is its own state and event, checked end to end.
func TestRejectionsAreDistinctStatesAndEvents(t *testing.T) {
	cases := map[string]func() (update.Bundle, string){
		"expired": func() (update.Bundle, string) {
			m := updatetest.Manifest("1.3.0")
			m.Expires = updatetest.Epoch.Add(-time.Hour)
			return updatetest.StandardBundle(m), update.CodeManifestExpiry
		},
		"lower": func() (update.Bundle, string) {
			return updatetest.StandardBundle(updatetest.Manifest("1.1.0")), update.CodeVersionLower
		},
		"unknown key": func() (update.Bundle, string) {
			return updatetest.Sign(updatetest.Manifest("1.3.0"), updatetest.Stranger, updatetest.Root,
				updatetest.Delegation(updatetest.DelegationOptions{Keys: []updatetest.Key{updatetest.Release}})), update.CodeUnknownKey
		},
	}
	seen := map[string]bool{}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			b, want := mk()
			h.publish(b, nil)
			u := h.updater("1.2.3")
			err := u.Check(context.Background(), false)
			if update.CodeOf(err) != want {
				t.Fatalf("err = %v, want code %s", err, want)
			}
			st := u.Status()
			if st.State != update.StateFailed || st.Code != want || st.Error == nil || !strings.HasPrefix(*st.Error, want) {
				t.Fatalf("state = %+v", st)
			}
			if len(h.events) != 1 || h.events[0].Type != "update_failed" || h.events[0].Detail["code"] != want {
				t.Fatalf("events = %+v", h.events)
			}
			if h.artHits != 0 {
				t.Fatal("downloaded after a rejection")
			}
			seen[want] = true
		})
	}
	if len(seen) != 3 {
		t.Fatalf("rejections were not distinct: %v", seen)
	}
}

func TestRollbackAuthorizedManifestIsInstalled(t *testing.T) {
	h := newHarness(t)
	m := updatetest.Manifest("1.2.0", updatetest.Artifact(update.KindApp, "pwsgw-linux-arm64", "pwsgw-linux-arm64", []byte("#!pwsgw 1.2.0"), 8))
	m.Rollback = &update.Rollback{FromVersionNumbers: []int{updatetest.Installed}}
	h.publish(updatetest.StandardBundle(m), map[string][]byte{"pwsgw-linux-arm64": []byte("#!pwsgw 1.2.0")})
	if err := h.updater("1.2.3").Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.apps.installed, []string{"1.2.0"}) {
		t.Fatalf("installed %v", h.apps.installed)
	}
}

// After the configured failures the state is deferred, not failed, and the
// retry waits for the back-off.
func TestDownloadFailuresDeferThenRetry(t *testing.T) {
	h := newHarness(t)
	h.maxFailures = 2
	b, files := release("1.3.0", 0)
	h.publish(b, files)
	h.artStatus = http.StatusServiceUnavailable
	u := h.updater("1.2.3")
	if err := u.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	st := u.Status()
	if st.State != update.StateDeferred || st.DeferKind != update.DeferDownload || st.Failures != 2 || st.DeferUntil.IsZero() {
		t.Fatalf("state = %+v", st)
	}
	if h.sawState(update.StateFailed) || slices.Contains(h.eventTypes(), "update_failed") {
		t.Fatal("deferral must not look like a failure")
	}
	hits := h.artHits
	if err := u.Check(context.Background(), false); err != nil || h.artHits != hits {
		t.Fatalf("retried during back-off: err=%v hits %d -> %d", err, hits, h.artHits)
	}
	h.artStatus = 0
	h.clk.Set(st.DeferUntil.Add(time.Second))
	if err := u.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if len(h.apps.installed) != 1 {
		t.Fatalf("not installed after back-off: %+v", u.Status())
	}
}

func TestCheckGuards(t *testing.T) {
	h := newHarness(t)
	b, files := release("1.3.0", 0)
	h.publish(b, files)
	if err := h.updater("dev").Check(context.Background(), false); !errors.Is(err, update.ErrDevBuild) {
		t.Fatalf("dev build: %v", err)
	}
	h.synced = false
	if err := h.updater("1.2.3").Check(context.Background(), false); !errors.Is(err, update.ErrClockUnsynced) {
		t.Fatalf("unsynced: %v", err)
	}
	h.synced = true
	u := h.updater("1.3.0") // already current
	if err := u.Check(context.Background(), false); err != nil || len(h.apps.installed) != 0 || u.Status().State != update.StateIdle {
		t.Fatalf("up to date: %v %+v", err, u.Status())
	}
}

func TestPendingStateSurvivesRestartOfUpdater(t *testing.T) {
	h := newHarness(t)
	b, files := release("1.3.0", 0)
	h.publish(b, files)
	if err := h.updater("1.2.3").Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	st := h.updater("1.2.3").Status()
	if st.Pending == nil || st.Pending.Kind != "app" || st.State != update.StateRebooting {
		t.Fatalf("state = %+v", st)
	}
}
