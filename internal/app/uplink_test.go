package app

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
)

func newUplinkApp(t *testing.T, mod func(*config.Config), deps func(*Deps)) (*App, string, *bytes.Buffer) {
	t.Helper()
	cs, dir := newConfig(t)
	if mod != nil {
		if _, err := cs.Update(func(c *config.Config) error { mod(c); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	d := Deps{
		DataDir: dir, Config: cs, Platform: mock.New(),
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Now:    func() time.Time { return time.Date(2026, 11, 16, 17, 20, 0, 0, time.UTC) },
	}
	if deps != nil {
		deps(&d)
	}
	a, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.store.Close() })
	return a, dir, &logs
}

func TestUnclaimedDeviceDoesNotStartUplink(t *testing.T) {
	a, _, logs := newUplinkApp(t, nil, nil)
	if a.Uplink() != nil {
		t.Fatal("uplink started without a device id")
	}
	if got := strings.Count(logs.String(), "unclaimed"); got != 1 {
		t.Fatalf("unclaimed logged %d times: %s", got, logs.String())
	}
	if st := a.Status().Uplink(context.Background()); st.Connected || st.DeviceID != "" {
		t.Fatalf("status: %+v", st)
	}
}

func TestMissingCredentialFileLeavesDeviceUnclaimed(t *testing.T) {
	a, _, logs := newUplinkApp(t, func(c *config.Config) { c.Uplink.DeviceID = "d_8f3a" }, nil)
	if a.Uplink() != nil || !strings.Contains(logs.String(), "credential file not found") {
		t.Fatalf("uplink=%v logs=%s", a.Uplink(), logs.String())
	}
}

func TestClaimedDeviceBuildsUplink(t *testing.T) {
	a, dir, logs := newUplinkApp(t, func(c *config.Config) { c.Uplink.DeviceID = "d_8f3a" }, nil)
	_ = a // the credential file is written after the first build, so rebuild
	if err := os.WriteFile(filepath.Join(dir, "uplink.credential"), []byte(`{"username":"d_8f3a","password":"s3cret-pw"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a2, err := New(Deps{DataDir: dir, Config: a.deps.Config, Platform: mock.New(), Logger: slog.New(slog.NewTextHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a2.store.Close() })
	u := a2.Uplink()
	if u == nil {
		t.Fatalf("uplink not built: %s", logs.String())
	}
	if st := a2.Status().Uplink(context.Background()); st.DeviceID != "d_8f3a" || st.Broker == "" || st.Connected {
		t.Fatalf("status: %+v", st)
	}
	if up := a2.Status().Update(context.Background()); up.State != "idle" {
		t.Fatalf("update: %+v", up)
	}
	if strings.Contains(logs.String(), "s3cret-pw") {
		t.Fatal("credential in the logs")
	}
}

func TestDryRunNeedsNoCredentialAndNeverConnects(t *testing.T) {
	a, _, _ := newUplinkApp(t,
		func(c *config.Config) { c.Uplink.DeviceID = "d_8f3a"; c.Uplink.BrokerURL = "mqtt://127.0.0.1:1" },
		func(d *Deps) { d.UplinkDryRun = true })
	u := a.Uplink()
	if u == nil {
		t.Fatal("dry run did not build the uplink")
	}
	if st := u.Status(); !st.DryRun || st.Connected {
		t.Fatalf("status: %+v", st)
	}
}
