package app

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"

	"github.com/wxalerts/pws-gateway/internal/claim/claimtest"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
)

// wsBroker starts a WebSocket MQTT broker that accepts one login.
func wsBroker(t *testing.T, user, pass string) (*mqtt.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	srv := mqtt.New(&mqtt.Options{Logger: quietLogger()})
	ledger := &auth.Ledger{Auth: auth.AuthRules{{Username: auth.RString(user), Password: auth.RString(pass), Allow: true}}}
	if err := srv.AddHook(new(auth.Hook), &auth.Options{Ledger: ledger}); err != nil {
		t.Fatal(err)
	}
	if err := srv.AddListener(listeners.NewWebsocket(listeners.Config{ID: "ws", Address: addr})); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv, addr
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestClaimStartsUplinkWithoutRestartAndUnlinkStopsIt(t *testing.T) {
	const device, password = "d_abcdefghij", "claimed-mqtt-password"
	broker, addr := wsBroker(t, device, password)
	api := claimtest.New(t)
	api.Interval = 1
	api.BrokerURL = "ws://" + addr + "/mqtt"
	api.Pending()
	api.Approve(device, password)

	cs, dir := newConfig(t)
	a, err := New(Deps{
		DataDir: dir, Config: cs, Platform: mock.New(), Logger: quietLogger(),
		ReplayFile: capture, ClaimAPI: api.URL, ClaimTimeUnit: 5 * time.Millisecond,
		Now: func() time.Time { return time.Date(2026, 11, 16, 17, 20, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Uplink() != nil {
		t.Fatal("uplink must not exist before the claim")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	src := a.Claim()
	if _, err := src.ClaimStart(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "uplink connected after the claim", func() bool {
		st := a.Status().Uplink(ctx)
		return st.Connected && st.DeviceID == device
	})
	if _, ok := broker.Clients.Get(device); !ok {
		t.Fatal("the broker has no client for the claimed device")
	}
	if _, err := os.Stat(filepath.Join(dir, "uplink.credential")); err != nil {
		t.Fatal(err)
	}

	if err := src.ClaimUnlink(); err != nil {
		t.Fatal(err)
	}
	if a.Uplink() != nil {
		t.Fatal("uplink still present after unlink")
	}
	if _, err := os.Stat(filepath.Join(dir, "uplink.credential")); !os.IsNotExist(err) {
		t.Fatalf("credential file after unlink: %v", err)
	}
	eventually(t, "broker client gone", func() bool {
		_, ok := broker.Clients.Get(device)
		return !ok
	})
	if st := src.Claim(); st.State != "unclaimed" {
		t.Fatalf("claim after unlink = %+v", st)
	}
}
