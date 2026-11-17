package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	if found.Model != "Acurite-5n1" || found.ID != 4026 || found.Channel != "A" {
		t.Errorf("unexpected station info: %+v", *found)
	}
	if found.BatteryOK == nil || !*found.BatteryOK {
		t.Errorf("5n1 reports battery_ok=1, got %v", found.BatteryOK)
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

// A decoder line without battery_ok must reach the API as unknown, not OK.
func TestStationWithoutBatteryFlagIsUnknown(t *testing.T) {
	cs, dir := newConfig(t)
	// The recorded 5n1 line with battery_ok removed and a different id.
	var lines strings.Builder
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&lines, `{"time" : "2026-11-16T16:52:%02d.393158", "protocol" : 40, "model" : "Acurite-5n1", `+
			`"message_type" : 49, "id" : 77, "channel" : "B", "sequence_num" : %d, "wind_avg_km_h" : 8.450, `+
			`"wind_dir_deg" : 135.000, "rain_in" : 38.810, "mic" : "CHECKSUM", "mod" : "ASK", `+
			`"freq" : 433.934, "rssi" : -0.102, "snr" : 13.873, "noise" : -13.975}`+"\n", 3+i*8, i%3)
	}
	file := filepath.Join(t.TempDir(), "nobattery.jsonl")
	if err := os.WriteFile(file, []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(Deps{
		DataDir: dir, Config: cs, Platform: mock.New(), Logger: quietLogger(), ReplayFile: file,
		Now: func() time.Time { return time.Date(2026, 11, 16, 17, 20, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		sts, err := a.Stations().Stations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(sts) == 1 {
			if sts[0].BatteryOK != nil || sts[0].Channel != "B" {
				t.Fatalf("want nil battery and channel B, got %+v", sts[0])
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("station not discovered")
}

func TestFakeHwclockDisabledWhenRTCPresent(t *testing.T) {
	cs, dir := newConfig(t)
	plat := mock.New()
	a, err := New(Deps{DataDir: dir, Config: cs, Platform: plat, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	a.disableFakeHwclockIfRTC(ctx) // no DS3231: leave fake-hwclock alone
	if plat.FakeHwclockCalls != 0 {
		t.Fatalf("disabled without an RTC: %d", plat.FakeHwclockCalls)
	}

	plat.Set(func(p *mock.Platform) {
		p.RTCValue = &platform.RTCInfo{Time: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	})
	a.disableFakeHwclockIfRTC(ctx)
	a.disableFakeHwclockIfRTC(ctx) // idempotent: already disabled
	if plat.FakeHwclockCalls != 1 || !plat.FakeHwclockOff {
		t.Fatalf("calls=%d off=%v", plat.FakeHwclockCalls, plat.FakeHwclockOff)
	}
}

func TestBMP280FoundOnTheBusBecomesAStationSource(t *testing.T) {
	cs, dir := newConfig(t)
	plat := mock.New()
	plat.I2CValue = platform.I2CStatus{Enabled: true, BusPresent: true, Devices: []platform.I2CDevice{
		{Addr: 0x68, Kind: platform.I2CKindDS3231},
		{Addr: 0x76, Kind: platform.I2CKindBMP280},
		{Addr: 0x77, Kind: platform.I2CKindBME280},
	}}
	plat.BMP280Values = map[int]platform.PressureReading{0x76: {StationHPa: 1006.5}}
	a, err := New(Deps{DataDir: dir, Config: cs, Platform: plat, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	sensors := a.findBarometers(context.Background())
	if len(sensors) != 1 || sensors[0].ID() != "i2c1-0x76" {
		t.Fatalf("sensors = %v", sensors)
	}
	hpa, err := sensors[0].ReadHPa(context.Background())
	if err != nil || hpa != 1006.5 {
		t.Fatalf("hpa=%v err=%v", hpa, err)
	}
}
