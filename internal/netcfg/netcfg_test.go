package netcfg_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/netcfg"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/fake"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func setup(t *testing.T) (*netcfg.Manager, *fake.Net, *clock, string) {
	t.Helper()
	nm := fake.NewNet()
	clk := &clock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	dir := t.TempDir()
	m := netcfg.New(netcfg.Options{NM: nm, DataDir: dir, Now: clk.Now, Fallback: netcfg.FallbackOptions{Identity: "serial-1"}})
	return m, nm, clk, dir
}

func staticEth() platform.Connection {
	return platform.Connection{
		Name: "wired", Type: platform.ConnEthernet, Interface: "eth0", Autoconnect: true,
		IPv4: platform.IPv4Settings{
			Method: platform.IPv4Manual, Address: "192.168.1.99/24", Gateway: "192.168.1.1",
			DNS: []string{"1.1.1.1"}, DNSSearch: []string{"lab.example"},
		},
	}
}

func wiredNow(nm *fake.Net) platform.Connection {
	cs, _ := nm.Connections(context.Background())
	for i := range cs {
		if cs[i].Name == "wired" {
			return cs[i]
		}
	}
	return platform.Connection{}
}

// A change that is not confirmed within 90 seconds is reverted, driven by a
// fake NetworkManager and a fake clock.
func TestUnconfirmedChangeRevertsAfter90Seconds(t *testing.T) {
	m, nm, clk, _ := setup(t)
	ctx := context.Background()
	p, err := m.Apply(ctx, netcfg.Change{Connections: []platform.Connection{staticEth()}})
	if err != nil {
		t.Fatal(err)
	}
	if want := clk.Now().Add(90 * time.Second); !p.Deadline.Equal(want) {
		t.Fatalf("deadline %v, want %v", p.Deadline, want)
	}
	if got := wiredNow(nm); got.IPv4.Method != platform.IPv4Manual || nm.ActiveOn("eth0") != "wired" {
		t.Fatalf("change not applied: %+v", got)
	}
	if st, _ := m.State(ctx); st.Pending == nil {
		t.Fatal("state must show the pending change")
	}

	clk.Advance(89 * time.Second)
	m.Tick()
	if wiredNow(nm).IPv4.Method != platform.IPv4Manual {
		t.Fatal("reverted early")
	}
	clk.Advance(2 * time.Second)
	m.Tick()
	got := wiredNow(nm)
	if got.IPv4.Method != platform.IPv4Auto || got.IPv4.Address != "" || len(got.IPv4.DNSSearch) != 0 {
		t.Fatalf("not reverted: %+v", got)
	}
	if st, _ := m.State(ctx); st.Pending != nil {
		t.Fatal("pending state survived the revert")
	}
	// The device is re-activated with the old profile.
	if nm.Calls[len(nm.Calls)-1] != "activate wired" {
		t.Fatalf("calls = %v", nm.Calls)
	}
	if err := m.Confirm(); !errors.Is(err, netcfg.ErrNoPending) {
		t.Fatalf("confirm after revert: %v", err)
	}
}

func TestConfirmKeepsChange(t *testing.T) {
	m, nm, clk, dir := setup(t)
	ctx := context.Background()
	if _, err := m.Apply(ctx, netcfg.Change{Connections: []platform.Connection{staticEth()}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "netcfg-pending.json")); err != nil {
		t.Fatal("pending file not written while a change is unconfirmed")
	}
	clk.Advance(30 * time.Second)
	if err := m.Confirm(); err != nil {
		t.Fatal(err)
	}
	clk.Advance(5 * time.Minute)
	m.Tick()
	if wiredNow(nm).IPv4.Method != platform.IPv4Manual {
		t.Fatal("confirmed change was reverted")
	}
	if _, err := os.Stat(filepath.Join(dir, "netcfg-pending.json")); err == nil {
		t.Fatal("pending file left behind after confirm")
	}
}

func TestConfirmAfterDeadlineRollsBack(t *testing.T) {
	m, nm, clk, _ := setup(t)
	if _, err := m.Apply(context.Background(), netcfg.Change{Connections: []platform.Connection{staticEth()}}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(91 * time.Second)
	if err := m.Confirm(); !errors.Is(err, config.ErrDeadlinePassed) {
		t.Fatalf("err = %v", err)
	}
	if wiredNow(nm).IPv4.Method != platform.IPv4Auto {
		t.Fatal("late confirm kept the change")
	}
}

func TestRevertNowAndSecondApplyBlocked(t *testing.T) {
	m, nm, _, _ := setup(t)
	ctx := context.Background()
	if _, err := m.Apply(ctx, netcfg.Change{Connections: []platform.Connection{staticEth()}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply(ctx, netcfg.Change{Hostname: "other"}); !errors.Is(err, netcfg.ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if err := m.Revert(); err != nil {
		t.Fatal(err)
	}
	if wiredNow(nm).IPv4.Method != platform.IPv4Auto {
		t.Fatal("revert did not restore")
	}
}

// A new Wi-Fi profile is created for the change, removed again on revert, and
// the host name goes back too.
func TestRevertRemovesCreatedProfileAndHostname(t *testing.T) {
	m, nm, clk, _ := setup(t)
	ctx := context.Background()
	wifi := platform.Connection{
		Name: "Home", Type: platform.ConnWifi, Autoconnect: true,
		Wifi: &platform.WifiSettings{SSID: "HomeNet", PSK: "supersecret"}, IPv4: platform.IPv4Settings{Method: platform.IPv4Auto},
	}
	if _, err := m.Apply(ctx, netcfg.Change{Connections: []platform.Connection{wifi}, Hostname: "garage"}); err != nil {
		t.Fatal(err)
	}
	if nm.ActiveOn("wlan0") != "Home" || nm.StoredPSK("Home") != "supersecret" {
		t.Fatal("wifi profile not applied")
	}
	if h, _ := nm.Hostname(ctx); h != "garage" {
		t.Fatalf("hostname %q", h)
	}
	clk.Advance(2 * time.Minute)
	m.Tick()
	cs, _ := nm.Connections(ctx)
	if len(cs) != 1 || cs[0].Name != "wired" {
		t.Fatalf("created profile left behind: %+v", cs)
	}
	if h, _ := nm.Hostname(ctx); h != "pws" {
		t.Fatalf("hostname not restored: %q", h)
	}
}

// If the daemon dies with a change unconfirmed, the next start reverts it.
func TestResumeRevertsAfterRestart(t *testing.T) {
	m, nm, _, dir := setup(t)
	if _, err := m.Apply(context.Background(), netcfg.Change{Connections: []platform.Connection{staticEth()}}); err != nil {
		t.Fatal(err)
	}
	m2 := netcfg.New(netcfg.Options{NM: nm, DataDir: dir})
	m2.Resume(context.Background())
	if wiredNow(nm).IPv4.Method != platform.IPv4Auto {
		t.Fatal("not reverted on resume")
	}
	if _, err := os.Stat(filepath.Join(dir, "netcfg-pending.json")); err == nil {
		t.Fatal("pending file left behind")
	}
}

func TestFailedApplyRollsBackAtOnce(t *testing.T) {
	m, nm, _, _ := setup(t)
	nm.ActivateErr["wired"] = errors.New("device busy")
	_, err := m.Apply(context.Background(), netcfg.Change{Connections: []platform.Connection{staticEth()}})
	if err == nil {
		t.Fatal("apply should fail")
	}
	delete(nm.ActivateErr, "wired")
	if wiredNow(nm).IPv4.Method != platform.IPv4Auto {
		t.Fatalf("half applied change left: %+v", wiredNow(nm))
	}
	if st, _ := m.State(context.Background()); st.Pending != nil {
		t.Fatal("no change should be pending")
	}
}

func TestValidation(t *testing.T) {
	m, _, _, _ := setup(t)
	bad := map[string]func(c *platform.Connection){
		"address not CIDR":    func(c *platform.Connection) { c.IPv4.Address = "192.168.1.99" },
		"gateway off subnet":  func(c *platform.Connection) { c.IPv4.Gateway = "10.0.0.1" },
		"gateway equals addr": func(c *platform.Connection) { c.IPv4.Gateway = "192.168.1.99" },
		"ipv6 dns":            func(c *platform.Connection) { c.IPv4.DNS = []string{"::1"} },
		"bad search domain":   func(c *platform.Connection) { c.IPv4.DNSSearch = []string{"bad domain"} },
		"unknown method":      func(c *platform.Connection) { c.IPv4.Method = "wild" },
		"hotspot name":        func(c *platform.Connection) { c.Name = netcfg.HotspotName },
		"cellular":            func(c *platform.Connection) { c.Type = platform.ConnCellular },
		"ethernet with wifi":  func(c *platform.Connection) { c.Wifi = &platform.WifiSettings{SSID: "x"} },
		"type change":         func(c *platform.Connection) { c.Type = platform.ConnWifi; c.Wifi = &platform.WifiSettings{SSID: "x"} },
		"short psk (as a wifi)": func(c *platform.Connection) {
			c.Type = platform.ConnWifi
			c.Wifi = &platform.WifiSettings{SSID: "x", PSK: "short"}
		},
	}
	for name, mut := range bad {
		t.Run(name, func(t *testing.T) {
			c := staticEth()
			mut(&c)
			if _, err := m.Apply(context.Background(), netcfg.Change{Connections: []platform.Connection{c}}); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, err := m.Apply(context.Background(), netcfg.Change{Hostname: "Bad Host"}); err == nil {
		t.Fatal("bad host name accepted")
	}
	if _, err := m.Apply(context.Background(), netcfg.Change{}); err == nil {
		t.Fatal("empty change accepted")
	}
}

func TestDeleteRefusesActiveAndHotspot(t *testing.T) {
	m, nm, _, _ := setup(t)
	ctx := context.Background()
	if err := m.Delete(ctx, "wired"); !errors.Is(err, netcfg.ErrActiveConn) {
		t.Fatalf("err = %v", err)
	}
	if err := m.Delete(ctx, netcfg.HotspotName); !errors.Is(err, netcfg.ErrProtected) {
		t.Fatalf("err = %v", err)
	}
	_ = nm.Save(ctx, platform.Connection{Name: "spare", Type: platform.ConnEthernet, IPv4: platform.IPv4Settings{Method: platform.IPv4Auto}})
	if err := m.Delete(ctx, "spare"); err != nil {
		t.Fatal(err)
	}
}

// With no known network reachable the gateway raises the setup hotspot after
// the grace period, and takes it down when a network comes back.
func TestHotspotFailback(t *testing.T) {
	m, nm, clk, _ := setup(t)
	ctx := context.Background()
	nm.SetDevices([]platform.Device{
		{Name: "eth0", Type: platform.ConnEthernet, State: "unavailable"},
		{Name: "wlan0", Type: platform.ConnWifi, State: "disconnected"},
	})
	m.FallbackTick(ctx) // starts the clock
	clk.Advance(60 * time.Second)
	m.FallbackTick(ctx)
	if nm.Hotspot() != nil {
		t.Fatal("hotspot came up before the grace period")
	}
	clk.Advance(31 * time.Second)
	m.FallbackTick(ctx)
	hs := nm.Hotspot()
	if hs == nil {
		t.Fatal("no hotspot after the grace period")
	}
	want := netcfg.HotspotCredentials("serial-1")
	if hs.SSID != want.SSID || hs.PSK != want.PSK || len(hs.PSK) != 10 {
		t.Fatalf("spec %+v want %+v", hs, want)
	}
	if st, _ := m.State(ctx); st.Hotspot == nil || st.Hotspot.SSID != want.SSID {
		t.Fatalf("state hotspot = %+v", st.Hotspot)
	}
	// Cable plugged in: a known network is reachable, the hotspot goes away.
	nm.SetDevices([]platform.Device{
		{Name: "eth0", Type: platform.ConnEthernet, State: "connected", Active: "wired"},
		{Name: "wlan0", Type: platform.ConnWifi, State: "connected", Active: fake.HotspotName},
	})
	m.FallbackTick(ctx)
	if nm.Hotspot() != nil {
		t.Fatal("hotspot stayed up with a network available")
	}
}

func TestHotspotDropsToProbeThenReturns(t *testing.T) {
	m, nm, clk, _ := setup(t)
	ctx := context.Background()
	nm.SetDevices([]platform.Device{{Name: "wlan0", Type: platform.ConnWifi, State: "disconnected"}})
	m.FallbackTick(ctx)
	clk.Advance(2 * time.Minute)
	m.FallbackTick(ctx)
	if nm.Hotspot() == nil {
		t.Fatal("no hotspot")
	}
	clk.Advance(5 * time.Minute)
	m.FallbackTick(ctx)
	if nm.Hotspot() != nil {
		t.Fatal("hotspot not dropped for the probe")
	}
	clk.Advance(20 * time.Second)
	m.FallbackTick(ctx)
	if nm.Hotspot() != nil {
		t.Fatal("hotspot returned during the probe window")
	}
	clk.Advance(30 * time.Second)
	m.FallbackTick(ctx)
	if nm.Hotspot() == nil {
		t.Fatal("hotspot did not return after a failed probe")
	}
}

func TestNoHotspotWhileChangeIsPending(t *testing.T) {
	m, nm, clk, _ := setup(t)
	ctx := context.Background()
	if _, err := m.Apply(ctx, netcfg.Change{Connections: []platform.Connection{staticEth()}}); err != nil {
		t.Fatal(err)
	}
	nm.SetDevices([]platform.Device{{Name: "wlan0", Type: platform.ConnWifi, State: "disconnected"}})
	m.FallbackTick(ctx)
	clk.Advance(80 * time.Second)
	m.FallbackTick(ctx)
	if nm.Hotspot() != nil {
		t.Fatal("hotspot raised in the middle of a confirmation window")
	}
}

func TestHotspotCredentialsAreStableAndShaped(t *testing.T) {
	a, b := netcfg.HotspotCredentials("x"), netcfg.HotspotCredentials("x")
	if a != b || netcfg.HotspotCredentials("y") == a {
		t.Fatal("credentials must be a pure function of the identity")
	}
	if len(a.SSID) != 8 || a.SSID[:4] != "PWS-" || len(a.PSK) != 10 || slices.Contains([]byte(a.PSK), '=') {
		t.Fatalf("%+v", a)
	}
}
