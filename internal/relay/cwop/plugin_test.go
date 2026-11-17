package cwop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/relay"
)

const secret = "S3CRET-VALIDATION-7781"

type fakeSender struct {
	calls   int
	servers []string
	login   string
	packets []string
	err     error
}

func (f *fakeSender) Send(_ context.Context, servers []string, login, packet string) error {
	f.calls++
	f.servers, f.login = servers, login
	if f.err != nil {
		return f.err
	}
	f.packets = append(f.packets, packet)
	return nil
}

func goodCfg() Config {
	return Config{Owner: "device", Station: "Acurite-5n1:1234:A", ID: "CW1234", PositionOptIn: true, Lat: 39.1234, Lon: -94.5678}
}

func sampleObs(t *testing.T) relay.Observation {
	var fx packetFixture
	vector(t, "packet.json", &fx)
	return observation(t, fx.Cases[0].Obs, fx.Cases[0].Rain)
}

func TestConfigProblems(t *testing.T) {
	if p := goodCfg().Problems(); len(p) != 0 {
		t.Fatalf("good config has problems %v", p)
	}
	c := goodCfg()
	c.ID = "CW12"
	c.PositionOptIn = false
	codes := map[string]bool{}
	for _, p := range c.Problems() {
		codes[p.Code] = true
	}
	if !codes[relay.CodeBadID] || !codes[relay.CodeNoPosition] {
		t.Errorf("problems %v", c.Problems())
	}
	ham := goodCfg()
	ham.ID = "W5XYZ"
	if len(ham.Problems()) == 0 {
		t.Error("a ham callsign without a credential file must be flagged")
	}
	ham.CredentialFile = "cwop.credential"
	if p := ham.Problems(); len(p) != 0 {
		t.Errorf("ham with credential: %v", p)
	}
	if (Config{Owner: "server", ID: "bogus"}).Problems() != nil {
		t.Error("a server-owned config is not validated by the Pi")
	}
	if got := ham.Servers(); got[0] != ServerHam || got[1] != "rotate.aprs.net:23" {
		t.Errorf("servers %v", got)
	}
	if got := goodCfg().Servers(); got[0] != ServerNonHam || got[1] != "cwop.aprs.net:23" {
		t.Errorf("servers %v", got)
	}
}

func TestOwnerGate(t *testing.T) {
	for _, owner := range []string{"server", "", "both"} {
		c := goodCfg()
		c.Owner = owner
		if c.Active() {
			t.Errorf("owner %q must not make the Pi send", owner)
		}
		fs := &fakeSender{}
		err := New(PluginOptions{Config: c, Sender: fs}).Send(context.Background(), "x")
		if relay.CodeOf(err) != relay.CodeNotConfigured || fs.calls != 0 {
			t.Errorf("owner %q: err %v calls %d", owner, err, fs.calls)
		}
	}
}

func TestTimeUnsyncedSendsNothing(t *testing.T) {
	fs := &fakeSender{}
	tr := &relay.Tracker{}
	p := New(PluginOptions{Config: goodCfg(), Sender: fs, Tracker: tr, TimeSynced: func() bool { return false }})
	err := p.Send(context.Background(), "pkt")
	if relay.CodeOf(err) != relay.CodeTimeUnsynced || fs.calls != 0 {
		t.Fatalf("err %v calls %d", err, fs.calls)
	}
	if h := tr.Health(); h.LastError == nil || h.LastError.Code != relay.CodeTimeUnsynced || h.Failures != 0 {
		t.Errorf("health %+v", h)
	}
}

func TestCredentialNeverLeaks(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := goodCfg()
	cfg.ID = "W5XYZ"
	cfg.CredentialFile = "cwop.credential"
	fs := &fakeSender{err: errors.New("login failed for user W5XYZ pass " + secret + " (server said no)")}
	p := New(PluginOptions{Config: cfg, Passcode: secret, Sender: fs, Version: "1.0", Logger: log})
	if err := p.Send(context.Background(), "pkt"); err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(fs.login, secret) {
		t.Fatal("the login line given to the sender must carry the passcode")
	}
	info, _ := json.Marshal(p.SupportInfo())
	cj, _ := json.Marshal(cfg)
	h, _ := json.Marshal(p.Health())
	for name, blob := range map[string]string{"logs": logs.String(), "support": string(info), "config json": string(cj), "health": string(h), "redacted login": p.RedactedLogin()} {
		if strings.Contains(blob, secret) {
			t.Errorf("%s contains the passcode: %s", name, blob)
		}
	}
	if !strings.Contains(string(info), `"passcode_set":true`) {
		t.Errorf("support info should say a passcode is set: %s", info)
	}
	// The redactor also masks a passcode it was not told about.
	if got := NewRedactor().Redact("user A pass OTHER vers x"); strings.Contains(got, "OTHER") {
		t.Errorf("login passcode not masked: %s", got)
	}
}

func TestNonHamLoginUsesMinusOne(t *testing.T) {
	fs := &fakeSender{}
	p := New(PluginOptions{Config: goodCfg(), Sender: fs, Passcode: "ignored", Version: "0.1.0"})
	if err := p.Send(context.Background(), "pkt"); err != nil {
		t.Fatal(err)
	}
	if fs.login != "user CW1234 pass -1 vers pws-gateway 0.1.0" {
		t.Errorf("login %q", fs.login)
	}
}

type fixedSource struct {
	obs relay.Observation
	ok  bool
}

func (f fixedSource) Latest(context.Context, string, time.Time) (relay.Observation, bool, error) {
	return f.obs, f.ok, nil
}

func TestRunnerDeliversOnceAndTracksState(t *testing.T) {
	fs := &fakeSender{}
	now := time.Date(2026, 11, 16, 14, 7, 0, 0, time.UTC)
	cfg := goodCfg()
	r := NewRunner(RunnerOptions{
		Config: func() Config { return cfg }, Source: fixedSource{sampleObs(t), true}, Sender: fs,
		Now: func() time.Time { return now }, TimeSynced: func() bool { return true },
	})
	r.Step(context.Background())
	r.Step(context.Background())
	if fs.calls != 1 || !strings.HasPrefix(fs.packets[0], "CW1234>APRS,TCPIP*:@161405z") {
		t.Fatalf("calls %d packets %v", fs.calls, fs.packets)
	}
	st := r.Status(false)
	if st.State != "ok" || st.Health.LastSuccess == nil || !st.Health.LastSuccess.Equal(now) {
		t.Errorf("status %+v", st)
	}
	// A new bucket after the interval is sent too.
	now = now.Add(11 * time.Minute)
	r.o.Source = fixedSource{func() relay.Observation { o := sampleObs(t); o.Start = o.Start.Add(5 * time.Minute); return o }(), true}
	r.Step(context.Background())
	if fs.calls != 2 {
		t.Errorf("calls %d", fs.calls)
	}
}

func TestRunnerErrorAndBackoff(t *testing.T) {
	fs := &fakeSender{err: relay.Errorf(relay.CodeConnect, "dial tcp: refused")}
	now := time.Date(2026, 11, 16, 14, 7, 0, 0, time.UTC)
	r := NewRunner(RunnerOptions{
		Config: goodCfg, Source: fixedSource{sampleObs(t), true}, Sender: fs,
		Now: func() time.Time { return now },
	})
	r.Step(context.Background())
	r.Step(context.Background()) // inside the backoff, no second attempt
	if fs.calls != 1 {
		t.Fatalf("calls %d", fs.calls)
	}
	st := r.Status(false)
	if st.State != "error" || st.Health.LastError == nil || st.Health.LastError.Code != relay.CodeConnect || st.Health.Failures != 1 || st.NextAttempt == nil {
		t.Errorf("status %+v", st)
	}
	now = now.Add(2 * time.Minute)
	fs.err = nil
	r.Step(context.Background())
	if st = r.Status(false); st.State != "ok" || st.Health.Failures != 0 {
		t.Errorf("status after recovery %+v", st)
	}
}

func TestRunnerOwnerFixtureNeverSendsWhenNotDevice(t *testing.T) {
	var fx struct {
		Cases []struct {
			Owner   string `json:"owner"`
			PiSends bool   `json:"pi_sends"`
		} `json:"cases"`
	}
	vector(t, "ownership.json", &fx)
	for _, c := range fx.Cases {
		fs := &fakeSender{}
		cfg := goodCfg()
		cfg.Owner = c.Owner
		r := NewRunner(RunnerOptions{Config: func() Config { return cfg }, Source: fixedSource{sampleObs(t), true}, Sender: fs})
		r.Step(context.Background())
		if (fs.calls > 0) != c.PiSends {
			t.Errorf("owner %q: sends %v, want %v", c.Owner, fs.calls > 0, c.PiSends)
		}
	}
}

func TestRunnerUnsyncedSendsNothing(t *testing.T) {
	fs := &fakeSender{}
	r := NewRunner(RunnerOptions{
		Config: goodCfg, Source: fixedSource{sampleObs(t), true}, Sender: fs,
		TimeSynced: func() bool { return false },
	})
	r.Step(context.Background())
	if fs.calls != 0 {
		t.Fatal("sent while unsynced")
	}
	if st := r.Status(false); st.State != "waiting_for_time" || st.Health.LastError.Code != relay.CodeTimeUnsynced {
		t.Errorf("status %+v", st)
	}
}
