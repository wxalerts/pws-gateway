package claim_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/claim"
	"github.com/wxalerts/pws-gateway/internal/claim/claimtest"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
	"github.com/wxalerts/pws-gateway/internal/uplink"
)

type rig struct {
	t       *testing.T
	dir     string
	cfg     *config.Store
	plat    *mock.Platform
	api     *claimtest.Fake
	m       *claim.Manager
	claimed atomic.Int32
	unlinks atomic.Int32
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	r := &rig{t: t, dir: dir, plat: mock.New(), api: claimtest.New(t)}
	r.cfg = config.NewStore(dir)
	if _, err := r.cfg.Load(); err != nil {
		t.Fatal(err)
	}
	r.api.ExpiresIn = 600
	r.api.Interval = 1
	r.build()
	return r
}

// build (re)creates the manager over the same data directory, as a restart does.
func (r *rig) build() {
	r.t.Helper()
	m, err := claim.New(claim.Options{
		DataDir: r.dir, Config: r.cfg, Platform: r.plat,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIBase: r.api.URL, FW: "0.5.0", TimeUnit: 2 * time.Millisecond,
		OnClaimed: func() { r.claimed.Add(1) },
		OnUnlink:  func() { r.unlinks.Add(1) },
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.m = m
}

// run starts the manager and returns a function that stops it. Cleanup stops
// it too.
func (r *rig) run() (stop func()) {
	r.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.m.Run(ctx); close(done) }()
	stop = func() { cancel(); <-done }
	r.t.Cleanup(stop)
	return stop
}

func (r *rig) waitState(want claim.State) claim.Status {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := r.m.Status(); s.State == want {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	r.t.Fatalf("state never became %s, is %+v", want, r.m.Status())
	return claim.Status{}
}

func TestStartShowsTheCodeAndPersistsIt(t *testing.T) {
	r := newRig(t)
	r.run()
	st, err := r.m.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != claim.Pending || st.UserCode != "K7MP-2XQD" || st.VerificationURI != "https://wxalerts.org/pws/claim" || st.ExpiresAt == nil {
		t.Fatalf("status = %+v", st)
	}
	raw, err := os.ReadFile(filepath.Join(r.dir, claim.StateFile))
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil || p["state"] != "pending" || p["user_code"] != "K7MP-2XQD" {
		t.Fatalf("claim.json = %s", raw)
	}
	if fi, _ := os.Stat(filepath.Join(r.dir, claim.StateFile)); fi.Mode().Perm() != 0o600 {
		t.Errorf("claim.json mode = %v", fi.Mode().Perm())
	}
}

func TestPendingThenSuccessWritesCredentialsConfigAndStartsUplink(t *testing.T) {
	r := newRig(t)
	r.api.Pending()
	r.api.Pending()
	r.api.Approve("d_abcdefghij", "s3cret-password-value")
	r.run()
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := r.waitState(claim.Claimed)
	if st.DeviceID != "d_abcdefghij" || st.UserCode != "" {
		t.Fatalf("claimed status = %+v", st)
	}
	deadline := time.Now().Add(time.Second)
	for r.claimed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.claimed.Load() != 1 {
		t.Fatalf("OnClaimed calls = %d", r.claimed.Load())
	}
	// The file the uplink reads.
	credPath := claim.CredentialPath(r.dir, r.cfg.Get().Uplink)
	cred, err := uplink.LoadCredentials(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Username != "d_abcdefghij" || cred.Password != "s3cret-password-value" {
		t.Fatalf("credentials = %v", cred)
	}
	if fi, _ := os.Stat(credPath); fi.Mode().Perm() != 0o600 {
		t.Errorf("credential mode = %v", fi.Mode().Perm())
	}
	up := r.cfg.Get().Uplink
	if up.DeviceID != "d_abcdefghij" || up.BrokerURL != "wss://pws-mqtt.wxalerts.org/mqtt" || up.CredentialFile == "" {
		t.Fatalf("uplink config = %+v", up)
	}
	// The password is not in claim.json or config.json.
	for _, f := range []string{claim.StateFile, "config.json"} {
		if raw, err := os.ReadFile(filepath.Join(r.dir, f)); err == nil && strings.Contains(string(raw), "s3cret-password-value") {
			t.Errorf("%s holds the password", f)
		}
	}
	for _, c := range r.api.PollCodes() {
		if !strings.HasPrefix(c, "device-code-secret-") {
			t.Fatalf("poll used code %q", c)
		}
	}
}

func TestSlowDownRaisesTheInterval(t *testing.T) {
	r := newRig(t)
	r.api.Interval = 1
	r.api.SlowDown(6) // 6 units of 2ms
	r.api.Pending()
	r.api.Approve("d_abcdefghij", "pw-pw-pw-pw-pw")
	r.run()
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.waitState(claim.Claimed)
	ts := r.api.PollTimes()
	if len(ts) != 3 {
		t.Fatalf("polls = %d", len(ts))
	}
	first, second := ts[1].Sub(ts[0]), ts[2].Sub(ts[1])
	if second < 12*time.Millisecond {
		t.Fatalf("interval after slow_down = %v (first gap %v), want at least 12ms", second, first)
	}
}

func TestSlowDownWithoutIntervalAddsFive(t *testing.T) {
	r := newRig(t)
	r.api.Interval = 1
	r.api.SlowDown(0)
	r.api.Pending()
	r.api.Approve("d_abcdefghij", "pw-pw-pw-pw-pw")
	r.run()
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.waitState(claim.Claimed)
	ts := r.api.PollTimes()
	if gap := ts[2].Sub(ts[1]); gap < 12*time.Millisecond {
		t.Fatalf("gap = %v, want at least 6 units", gap)
	}
}

func TestExpiredTokenAndLocalExpiryEndTheClaim(t *testing.T) {
	r := newRig(t)
	r.api.Expired()
	r.run()
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := r.waitState(claim.Unclaimed); st.Error != claim.ReasonExpired {
		t.Fatalf("status = %+v", st)
	}

	// No server answer, only the local clock: 1 unit expiry.
	r.api.ExpiresIn = 3
	r.api.Interval = 1
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := r.waitState(claim.Unclaimed); st.Error != claim.ReasonExpired {
		t.Fatalf("local expiry status = %+v", st)
	}
}

func TestAccessDenied(t *testing.T) {
	r := newRig(t)
	r.api.Denied()
	r.run()
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := r.waitState(claim.Unclaimed); st.Error != claim.ReasonDenied {
		t.Fatalf("status = %+v", st)
	}
	if r.claimed.Load() != 0 || r.cfg.Get().Uplink.DeviceID != "" {
		t.Fatal("a denied claim must not link anything")
	}
}

func TestOutageKeepsPolling(t *testing.T) {
	r := newRig(t)
	r.api.Outage()
	r.api.Approve("d_abcdefghij", "pw-pw-pw-pw-pw")
	r.run()
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.waitState(claim.Claimed)
}

func TestStartErrors(t *testing.T) {
	r := newRig(t)
	r.api.StartStatus, r.api.StartError = 409, "already_claimed"
	if _, err := r.m.Start(context.Background()); !errors.Is(err, claim.ErrAlreadyClaimed) {
		t.Fatalf("409: %v", err)
	}
	r.api.StartStatus, r.api.StartError = 429, "rate_limited"
	if _, err := r.m.Start(context.Background()); !errors.Is(err, claim.ErrRateLimited) {
		t.Fatalf("429: %v", err)
	}
	r.api.StartStatus, r.api.StartError = 503, "unavailable"
	if _, err := r.m.Start(context.Background()); !errors.Is(err, claim.ErrUnavailable) {
		t.Fatalf("503: %v", err)
	}
	if r.m.Status().State != claim.Unclaimed {
		t.Fatal("failed starts must leave the gateway unclaimed")
	}
}

func TestStartOverSupersedesAndCancelClears(t *testing.T) {
	r := newRig(t)
	r.run()
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(r.api.HWIDs()); got != 2 {
		t.Fatalf("starts = %d", got)
	}
	if st := r.m.Cancel(); st.State != claim.Unclaimed || st.UserCode != "" {
		t.Fatalf("cancel = %+v", st)
	}
	n := len(r.api.PollTimes())
	time.Sleep(30 * time.Millisecond)
	if len(r.api.PollTimes()) != n {
		t.Fatal("polling continued after cancel")
	}
}

func TestPendingClaimResumesAfterRestart(t *testing.T) {
	r := newRig(t)
	stop := r.run()
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Stopping and building a second manager over the same directory is a restart.
	stop()
	r.build()
	if st := r.m.Status(); st.State != claim.Pending || st.UserCode != "K7MP-2XQD" {
		t.Fatalf("after restart = %+v", st)
	}
	r.api.Approve("d_abcdefghij", "pw-pw-pw-pw-pw")
	r.run()
	r.waitState(claim.Claimed)
}

func TestExpiredPendingStateIsDroppedOnLoad(t *testing.T) {
	r := newRig(t)
	p := `{"state":"pending","user_code":"AAAA-BBBB","device_code":"x","expires_at":"2020-01-01T00:00:00Z","interval":5}`
	if err := os.WriteFile(filepath.Join(r.dir, claim.StateFile), []byte(p), 0o600); err != nil {
		t.Fatal(err)
	}
	r.build()
	if st := r.m.Status(); st.State != claim.Unclaimed || st.Error != claim.ReasonExpired {
		t.Fatalf("status = %+v", st)
	}
}

func TestUnlinkDeletesCredentialsAndClearsConfig(t *testing.T) {
	r := newRig(t)
	r.api.Approve("d_abcdefghij", "pw-pw-pw-pw-pw")
	r.run()
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.waitState(claim.Claimed)
	credPath := claim.CredentialPath(r.dir, r.cfg.Get().Uplink)
	if _, err := r.m.Start(context.Background()); !errors.Is(err, claim.ErrClaimed) {
		t.Fatalf("start while claimed: %v", err)
	}
	if err := r.m.Unlink(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential file still there: %v", err)
	}
	up := r.cfg.Get().Uplink
	if up.DeviceID != "" || up.BrokerURL != "" {
		t.Fatalf("uplink config = %+v", up)
	}
	if r.unlinks.Load() != 1 || r.m.Status().State != claim.Unclaimed {
		t.Fatalf("unlinks=%d status=%+v", r.unlinks.Load(), r.m.Status())
	}
	// And it survives a restart.
	r.build()
	if r.m.Status().State != claim.Unclaimed {
		t.Fatal("unlink did not persist")
	}
}

func TestHandProvisionedDeviceShowsAsClaimed(t *testing.T) {
	r := newRig(t)
	if err := os.WriteFile(filepath.Join(r.dir, "uplink.credential"), []byte(`{"username":"u","password":"p"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.cfg.Update(func(c *config.Config) error { c.Uplink.DeviceID = "d_bench"; return nil }); err != nil {
		t.Fatal(err)
	}
	r.build()
	if st := r.m.Status(); st.State != claim.Claimed || st.DeviceID != "d_bench" {
		t.Fatalf("status = %+v", st)
	}
}

func TestHardwareIDHashesTheSerial(t *testing.T) {
	b := platform.BoardInfo{Serial: "10000000abcdef01", MachineID: "ignored-when-serial-exists"}
	id, src, err := claim.HardwareID(b, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if src != claim.SourceSerial || !strings.HasPrefix(id, "hw1_") || len(id) != 68 {
		t.Fatalf("id = %q source = %q", id, src)
	}
	if strings.Contains(id, "abcdef01") {
		t.Fatal("hardware id leaks the serial")
	}
	again, _, _ := claim.HardwareID(b, t.TempDir())
	if again != id {
		t.Fatal("derivation is not stable")
	}
	// Pinned vector: a change here is a protocol break, since the server
	// derives device ids from this value.
	const want = "hw1_30f3ea3daec7bee69bb9c6cd95a6715f1aecc0f0f0cd27b0a50d898c5019b2c6"
	if id != want {
		t.Fatalf("id = %s, want the pinned vector %s", id, want)
	}
}

func TestHardwareIDFallsBackToMachineIDThenGenerated(t *testing.T) {
	dir := t.TempDir()
	id, src, err := claim.HardwareID(platform.BoardInfo{MachineID: "0123456789abcdef0123456789abcdef"}, dir)
	if err != nil || src != claim.SourceMachineID {
		t.Fatalf("machine-id: %q %q %v", id, src, err)
	}
	serialID, _, _ := claim.HardwareID(platform.BoardInfo{Serial: "0123456789abcdef0123456789abcdef"}, dir)
	if serialID == id {
		t.Fatal("serial and machine id must not collide")
	}
	g1, src, err := claim.HardwareID(platform.BoardInfo{}, dir)
	if err != nil || src != claim.SourceGenerated {
		t.Fatalf("generated: %q %q %v", g1, src, err)
	}
	g2, _, _ := claim.HardwareID(platform.BoardInfo{}, dir)
	if g1 != g2 {
		t.Fatal("generated id must be stable across calls")
	}
	other, _, _ := claim.HardwareID(platform.BoardInfo{}, t.TempDir())
	if other == g1 {
		t.Fatal("generated ids must differ between devices")
	}
}

func TestStartSendsTheHashedHardwareID(t *testing.T) {
	r := newRig(t)
	if _, err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ids := r.api.HWIDs()
	want, _, _ := claim.HardwareID(r.plat.Board(), r.dir)
	if len(ids) != 1 || ids[0] != want || strings.Contains(ids[0], r.plat.Board().Serial) {
		t.Fatalf("hw_id = %v want %s", ids, want)
	}
}
