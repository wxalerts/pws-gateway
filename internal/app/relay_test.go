package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
)

func TestRelaySourceAndPasscodeFile(t *testing.T) {
	cs, dir := newConfig(t)
	now := time.Date(2026, 11, 16, 14, 7, 0, 0, time.UTC)
	a, err := New(Deps{DataDir: dir, Config: cs, Platform: mock.New(), Logger: quietLogger(), ReplayFile: capture, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "Acurite-5n1:1234:A"
	start := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	err = a.store.WriteRollups(ctx, []model.Bucket{{Start: start, StationKey: key, N: 7, Fields: map[string]model.Aggregate{
		"temp_c":  {Kind: model.KindGauge, N: 7, Avg: 18.2, Min: 17.9, Max: 18.6},
		"rain_mm": {Kind: model.KindCounter, N: 7, Sum: 1.016},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	obs, ok, err := relaySource{a: a}.Latest(ctx, key, now)
	if err != nil || !ok {
		t.Fatalf("latest: %v %v", ok, err)
	}
	if !obs.Start.Equal(start) || obs.IntervalS != 300 || obs.Station.N != 7 || obs.Rain == nil || obs.Rain.Last24hMM != 1.016 || obs.Rain.Last1hMM != 1.016 {
		t.Fatalf("obs %+v rain %+v", obs, obs.Rain)
	}
	if _, ok, _ := (relaySource{a: a}).Latest(ctx, "Nope:1:1", now); ok {
		t.Error("unknown station must have no data")
	}

	r := a.Relay()
	if err := r.SetCWOPPasscode("abc\ndef"); err == nil {
		t.Error("multi line passcode accepted")
	}
	if err := r.SetCWOPPasscode("VALIDATION-1"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "cwop.credential"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("credential file %v %v", st, err)
	}
	if !r.CWOP(ctx).PasscodeSet {
		t.Error("passcode_set false")
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if strings.Contains(string(cfg), "VALIDATION-1") {
		t.Error("passcode in config.json")
	}
	if err := r.ClearCWOPPasscode(); err != nil || r.CWOP(ctx).PasscodeSet {
		t.Errorf("clear: %v", err)
	}
}
