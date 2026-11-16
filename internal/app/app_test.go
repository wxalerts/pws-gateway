package app

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/model"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
	"github.com/wxalerts/pws-gateway/internal/web"
)

const (
	capture = "../../testdata/real/acurite-433-20261116.jsonl"
	keyAcu  = "Acurite-5n1:4026:A"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newConfig(t *testing.T) (*config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	cs := config.NewStore(dir)
	if _, err := cs.Load(); err != nil {
		t.Fatal(err)
	}
	return cs, dir
}

func TestReplayFeedsAdapters(t *testing.T) {
	cs, dir := newConfig(t)
	a, err := New(Deps{
		DataDir: dir, Config: cs, Platform: mock.New(), Logger: quietLogger(),
		ReplayFile: capture,
		// Recorded times are mid afternoon UTC; the clock sits just after the
		// 25 minute capture so no station is considered lost.
		Now: func() time.Time { return time.Date(2026, 11, 16, 17, 20, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	var found *web.StationInfo
	deadline := time.Now().Add(8 * time.Second)
	for found == nil && time.Now().Before(deadline) {
		sts, err := a.Stations().Stations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range sts {
			if sts[i].Key == keyAcu {
				found = &sts[i]
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if found == nil {
		t.Fatalf("station %s not discovered", keyAcu)
	}
	if found.Model != "Acurite-5n1" || found.ID != 4026 || found.Channel == nil || *found.Channel != 1 {
		t.Errorf("unexpected station info: %+v", *found)
	}
	if found.NSightings == 0 || len(found.Fields) == 0 {
		t.Errorf("sightings and fields not filled: %+v", *found)
	}

	rs, err := a.Readings().Readings(ctx, keyAcu, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) == 0 || len(rs[0].F) == 0 || rs[0].TS.IsZero() {
		t.Fatalf("want readings with fields, got %+v", rs)
	}

	if got := a.Status().RadioStatus(ctx, "anything"); got != "idle" {
		t.Errorf("radio status without supervisor = %q, want idle", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestChannelNumber(t *testing.T) {
	for in, want := range map[string]int{"A": 1, "b": 2, "3": 3} {
		if got := channelNumber(in); got == nil || *got != want {
			t.Errorf("channelNumber(%q) = %v, want %d", in, got, want)
		}
	}
	if channelNumber("") != nil {
		t.Error("empty channel should be nil")
	}
}

func TestDefaultRadioChosenForSingleDongle(t *testing.T) {
	cs, dir := newConfig(t)
	plat := mock.New()
	plat.USB = []platform.USBDevice{{VendorID: 0x0bda, ProductID: 0x2838, Serial: "00000001", Port: "1-1"}}
	a, err := New(Deps{DataDir: dir, Config: cs, Platform: plat, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.store.Close() })
	a.chooseDefaultRadio(context.Background())
	got := cs.Get().Radios
	if len(got) != 1 || got[0].Serial != "00000001" || got[0].Role != config.RoleRTL433 || got[0].BandMHz != 433.92 {
		t.Fatalf("radios = %+v", got)
	}
}

func TestNoDefaultRadioForTwoDongles(t *testing.T) {
	cs, dir := newConfig(t)
	plat := mock.New()
	plat.USB = []platform.USBDevice{
		{VendorID: 0x0bda, ProductID: 0x2838, Serial: "1"},
		{VendorID: 0x0bda, ProductID: 0x2838, Serial: "2"},
	}
	a, err := New(Deps{DataDir: dir, Config: cs, Platform: plat, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.store.Close() })
	a.chooseDefaultRadio(context.Background())
	if n := len(cs.Get().Radios); n != 0 {
		t.Fatalf("radios = %d, want none", n)
	}
}

// fakeProc blocks reading until Kill, like an idle rtl_433.
type fakeProc struct {
	outR, errR *io.PipeReader
	outW, errW *io.PipeWriter
}

func newFakeProc() *fakeProc {
	p := &fakeProc{}
	p.outR, p.outW = io.Pipe()
	p.errR, p.errW = io.Pipe()
	return p
}
func (p *fakeProc) Stdout() io.Reader { return p.outR }
func (p *fakeProc) Stderr() io.Reader { return p.errR }
func (p *fakeProc) Wait() error       { return nil }
func (p *fakeProc) PID() int          { return 42 }
func (p *fakeProc) Kill() error {
	_ = p.outW.Close()
	_ = p.errW.Close()
	return nil
}

func TestConfigChangeRestartsSupervisor(t *testing.T) {
	cs, dir := newConfig(t)
	if _, err := cs.Update(func(c *config.Config) error {
		c.Radios = []config.Radio{{Serial: "S1", Role: config.RoleRTL433, BandMHz: 433.92}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	argvs := make(chan []string, 8)
	a, err := New(Deps{
		DataDir: dir, Config: cs, Platform: mock.New(), Logger: quietLogger(),
		Start: func(_ context.Context, _ string, args []string) (rtl433.Process, error) {
			argvs <- args
			return newFakeProc(), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	next := func() []string {
		t.Helper()
		select {
		case v := <-argvs:
			return v
		case <-time.After(5 * time.Second):
			t.Fatal("rtl_433 was not started")
			return nil
		}
	}
	if args := next(); !contains(args, "433920000") {
		t.Fatalf("first args = %v", args)
	}
	if _, err := cs.Update(func(c *config.Config) error { c.Radios[0].BandMHz = 915; return nil }); err != nil {
		t.Fatal(err)
	}
	if args := next(); !contains(args, "915000000") {
		t.Fatalf("restart args = %v", args)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func TestSelectionFollowsLiveConfig(t *testing.T) {
	cs, dir := newConfig(t)
	a, err := New(Deps{DataDir: dir, Config: cs, Platform: mock.New(), Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	st := model.Station{Key: "Acurite-609TXC:224:", Model: "Acurite-609TXC", Fields: []string{"battery_ok", "humidity_pct", "temp_c"}}
	if sel, _ := a.selection(st); sel {
		t.Fatal("an unconfigured station must not be stored")
	}
	if _, err := cs.Update(func(c *config.Config) error {
		c.Stations[st.Key] = config.Station{Selected: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sel, fields := a.selection(st)
	if !sel || !slices.Equal(fields, []string{"temp_c", "humidity_pct"}) {
		t.Fatalf("selected = %v fields = %v", sel, fields)
	}
	if _, err := cs.Update(func(c *config.Config) error {
		c.Stations[st.Key] = config.Station{Selected: true, Fields: map[string]bool{"battery_ok": true, "humidity_pct": false}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, fields := a.selection(st); !slices.Equal(fields, []string{"temp_c", "battery_ok"}) {
		t.Fatalf("fields = %v", fields)
	}
}
