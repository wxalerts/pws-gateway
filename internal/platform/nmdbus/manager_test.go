package nmdbus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/wxalerts/pws-gateway/internal/platform"
)

type callRec struct {
	path   dbus.ObjectPath
	method string
	args   []any
}

// fakeBus is an object tree: properties keyed by path and name, and method
// handlers keyed by path and full method name.
type fakeBus struct {
	props    map[string]any
	handlers map[string]func(args []any) ([]any, error)
	calls    []callRec
}

func newFake() *fakeBus {
	return &fakeBus{props: map[string]any{}, handlers: map[string]func([]any) ([]any, error){}}
}

func (f *fakeBus) setProp(path dbus.ObjectPath, name string, v any) {
	f.props[string(path)+" "+name] = v
}
func (f *fakeBus) handle(path dbus.ObjectPath, method string, fn func([]any) ([]any, error)) {
	f.handlers[string(path)+" "+method] = fn
}

func (f *fakeBus) call(_ context.Context, _ string, path dbus.ObjectPath, method string, args ...any) ([]any, error) {
	f.calls = append(f.calls, callRec{path, method, args})
	h, ok := f.handlers[string(path)+" "+method]
	if !ok {
		return nil, fmt.Errorf("fake: no handler for %s %s", path, method)
	}
	return h(args)
}

func (f *fakeBus) prop(_ context.Context, _ string, path dbus.ObjectPath, _, name string) (any, error) {
	v, ok := f.props[string(path)+" "+name]
	if !ok {
		return nil, fmt.Errorf("fake: no property %s %s", path, name)
	}
	return v, nil
}

func (f *fakeBus) called(method string) []callRec {
	var out []callRec
	for _, c := range f.calls {
		if strings.HasSuffix(c.method, method) {
			out = append(out, c)
		}
	}
	return out
}

const (
	pHome    = dbus.ObjectPath("/s/1")
	pHotspot = dbus.ObjectPath("/s/2")
)

func homeSettings() settings {
	return settings{
		"connection": {"id": dbus.MakeVariant("home"), "type": dbus.MakeVariant(nmWifi), "autoconnect": dbus.MakeVariant(true)},
		"ipv4":       {"method": dbus.MakeVariant("auto")},
		nmWifi:       {"ssid": dbus.MakeVariant([]byte("HomeNet"))},
		"802-11-wireless-security": {
			"key-mgmt": dbus.MakeVariant("wpa-psk"),
			"psk":      dbus.MakeVariant("should-never-surface"),
		},
	}
}

func hotspotStored() settings {
	s, _ := hotspotSettings(platform.HotspotSpec{SSID: "PWS-Setup", PSK: "setup-pass-1"})
	return s
}

// storeFake wires ListConnections and GetSettings over the given profiles.
func storeFake(profiles map[dbus.ObjectPath]settings, order []dbus.ObjectPath) *fakeBus {
	f := newFake()
	f.handle(settingsPath, settingsIf+".ListConnections", func([]any) ([]any, error) {
		return []any{order}, nil
	})
	for p, s := range profiles {
		s := s
		f.handle(p, connIf+".GetSettings", func([]any) ([]any, error) { return []any{s}, nil })
	}
	return f
}

func newMgr(t *testing.T, f *fakeBus) *Manager {
	t.Helper()
	m, err := New(Options{bus: f, PollInterval: time.Millisecond, ScanWait: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSaveAddsWhenMissing(t *testing.T) {
	f := storeFake(map[dbus.ObjectPath]settings{pHome: homeSettings()}, []dbus.ObjectPath{pHome})
	f.handle(settingsPath, settingsIf+".AddConnection", func([]any) ([]any, error) { return []any{dbus.ObjectPath("/s/9")}, nil })
	m := newMgr(t, f)
	err := m.Save(context.Background(), platform.Connection{Name: "lan", Type: platform.ConnEthernet, Autoconnect: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.called(".AddConnection")); n != 1 {
		t.Fatalf("AddConnection calls = %d", n)
	}
	if len(f.called(".Update")) != 0 || len(f.called("ActivateConnection")) != 0 {
		t.Error("Save must not update or activate on add")
	}
	sent := f.called(".AddConnection")[0].args[0].(settings)
	if sent["connection"]["id"].Value() != "lan" {
		t.Errorf("sent = %v", sent)
	}
}

func TestSaveUpdatesExisting(t *testing.T) {
	f := storeFake(map[dbus.ObjectPath]settings{pHome: homeSettings()}, []dbus.ObjectPath{pHome})
	f.handle(pHome, connIf+".Update", func([]any) ([]any, error) { return nil, nil })
	m := newMgr(t, f)
	err := m.Save(context.Background(), platform.Connection{
		Name: "home", Type: platform.ConnWifi, Autoconnect: true,
		IPv4: platform.IPv4Settings{Method: platform.IPv4Auto}, Wifi: &platform.WifiSettings{SSID: "HomeNet"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.called(".AddConnection")) != 0 {
		t.Error("must not add when the profile exists")
	}
	ups := f.called(".Update")
	if len(ups) != 1 {
		t.Fatalf("Update calls = %d", len(ups))
	}
	sent := ups[0].args[0].(settings)
	if _, ok := sent["802-11-wireless-security"]; !ok {
		t.Error("existing security block dropped")
	}
}

func TestSaveRejectsCellular(t *testing.T) {
	f := storeFake(nil, nil)
	m := newMgr(t, f)
	err := m.Save(context.Background(), platform.Connection{Name: "lte", Type: platform.ConnCellular})
	if !errors.Is(err, errReadOnly) {
		t.Errorf("err = %v", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	f := storeFake(map[dbus.ObjectPath]settings{pHome: homeSettings()}, []dbus.ObjectPath{pHome})
	m := newMgr(t, f)
	if err := m.Delete(context.Background(), "nope"); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	f.handle(pHome, connIf+".Delete", func([]any) ([]any, error) { return nil, nil })
	if err := m.Delete(context.Background(), "home"); err != nil {
		t.Fatal(err)
	}
	if len(f.called(".Delete")) != 1 {
		t.Error("Delete not called")
	}
}

func TestConnectionsHotspotFlagAndNoPSKLeak(t *testing.T) {
	f := storeFake(map[dbus.ObjectPath]settings{pHome: homeSettings(), pHotspot: hotspotStored()}, []dbus.ObjectPath{pHome, pHotspot})
	m := newMgr(t, f)
	got, err := m.Connections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Hotspot || !got[1].Hotspot {
		t.Errorf("hotspot flags = %v %v", got[0].Hotspot, got[1].Hotspot)
	}
	for _, c := range got {
		if c.Wifi != nil && c.Wifi.PSK != "" {
			t.Errorf("PSK leaked on %s", c.Name)
		}
		if strings.Contains(fmt.Sprintf("%+v", c), "should-never-surface") || strings.Contains(fmt.Sprintf("%+v", c), "setup-pass-1") {
			t.Errorf("secret in %+v", c)
		}
	}
}

func TestDevicesMapping(t *testing.T) {
	f := newFake()
	eth, wlan, lo := dbus.ObjectPath("/d/1"), dbus.ObjectPath("/d/2"), dbus.ObjectPath("/d/3")
	f.handle(nmPath, nmIface+".GetDevices", func([]any) ([]any, error) { return []any{[]dbus.ObjectPath{eth, wlan, lo}}, nil })
	f.setProp(eth, "DeviceType", uint32(1))
	f.setProp(eth, "Interface", "eth0")
	f.setProp(eth, "State", uint32(100))
	f.setProp(eth, "ActiveConnection", dbus.ObjectPath("/a/1"))
	f.setProp(eth, "HwAddress", "AA:BB:CC:DD:EE:FF")
	f.setProp(eth, "Ip4Config", dbus.ObjectPath("/ip/1"))
	f.setProp("/a/1", "Id", "Wired connection 1")
	f.setProp("/ip/1", "AddressData", []map[string]dbus.Variant{{"address": dbus.MakeVariant("192.168.1.5"), "prefix": dbus.MakeVariant(uint32(24))}})
	f.setProp(wlan, "DeviceType", uint32(2))
	f.setProp(wlan, "Interface", "wlan0")
	f.setProp(wlan, "State", uint32(30))
	f.setProp(wlan, "ActiveConnection", noObject)
	f.setProp(wlan, "Ip4Config", noObject)
	f.setProp(lo, "DeviceType", uint32(32))
	f.setProp(lo, "Interface", "lo")

	got, err := newMgr(t, f).Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("devices = %+v", got)
	}
	e := got[0]
	if e.Name != "eth0" || e.Type != platform.ConnEthernet || e.State != "connected" || e.Active != "Wired connection 1" ||
		e.MAC != "AA:BB:CC:DD:EE:FF" || len(e.Addresses) != 1 || e.Addresses[0] != "192.168.1.5/24" {
		t.Errorf("eth = %+v", e)
	}
	w := got[1]
	if w.Name != "wlan0" || w.Type != platform.ConnWifi || w.State != "disconnected" || w.Active != "" || len(w.Addresses) != 0 {
		t.Errorf("wlan = %+v", w)
	}
}

func TestActivateWaits(t *testing.T) {
	f := storeFake(map[dbus.ObjectPath]settings{pHome: homeSettings()}, []dbus.ObjectPath{pHome})
	wlan := dbus.ObjectPath("/d/2")
	f.handle(nmPath, nmIface+".GetDevices", func([]any) ([]any, error) { return []any{[]dbus.ObjectPath{wlan}}, nil })
	f.setProp(wlan, "DeviceType", uint32(2))
	f.setProp(wlan, "Interface", "wlan0")
	active := dbus.ObjectPath("/a/7")
	f.handle(nmPath, nmIface+".ActivateConnection", func(args []any) ([]any, error) {
		if args[1] != wlan {
			t.Errorf("device arg = %v", args[1])
		}
		return []any{active}, nil
	})
	polls := 0
	// State goes activating, activating, activated.
	m := newMgr(t, f)
	fb := &stateSeq{fakeBus: f, path: active, seq: []uint32{1, 1, 2}, n: &polls}
	m.bus = fb
	if err := m.Activate(context.Background(), "home"); err != nil {
		t.Fatal(err)
	}
	if polls != 3 {
		t.Errorf("polls = %d", polls)
	}

	fb2 := &stateSeq{fakeBus: f, path: active, seq: []uint32{1, 4}, n: new(int)}
	m.bus = fb2
	if err := m.Activate(context.Background(), "home"); err == nil {
		t.Error("expected failure on deactivated")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	m.bus = &stateSeq{fakeBus: f, path: active, seq: []uint32{1}, n: new(int), loop: true}
	if err := m.Activate(ctx, "home"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	if err := m.Activate(context.Background(), "missing"); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("missing err = %v", err)
	}
}

// stateSeq overrides the active connection State property with a sequence.
type stateSeq struct {
	*fakeBus
	path dbus.ObjectPath
	seq  []uint32
	n    *int
	loop bool
}

func (s *stateSeq) prop(ctx context.Context, d string, p dbus.ObjectPath, i, name string) (any, error) {
	if p == s.path && name == "State" {
		idx := *s.n
		*s.n++
		if idx >= len(s.seq) {
			if !s.loop {
				return nil, errors.New("gone")
			}
			idx = len(s.seq) - 1
		}
		return s.seq[idx], nil
	}
	return s.fakeBus.prop(ctx, d, p, i, name)
}

func TestConnectivity(t *testing.T) {
	f := newFake()
	f.handle(nmPath, nmIface+".CheckConnectivity", func([]any) ([]any, error) { return []any{uint32(4)}, nil })
	got, err := newMgr(t, f).Connectivity(context.Background())
	if err != nil || got != platform.ConnectivityFull {
		t.Errorf("got %q %v", got, err)
	}
}

func TestScanWifi(t *testing.T) {
	f := newFake()
	wlan := dbus.ObjectPath("/d/2")
	f.handle(nmPath, nmIface+".GetDevices", func([]any) ([]any, error) { return []any{[]dbus.ObjectPath{wlan}}, nil })
	f.setProp(wlan, "DeviceType", uint32(2))
	f.setProp(wlan, "Interface", "wlan0")
	f.handle(wlan, wirelessIf+".RequestScan", func([]any) ([]any, error) { return nil, errors.New("too soon") })
	aps := []dbus.ObjectPath{"/ap/1", "/ap/2", "/ap/3", "/ap/4"}
	f.handle(wlan, wirelessIf+".GetAllAccessPoints", func([]any) ([]any, error) { return []any{aps}, nil })
	add := func(p dbus.ObjectPath, ssid string, strength byte, flags, wpa, rsn uint32) {
		f.setProp(p, "Ssid", []byte(ssid))
		f.setProp(p, "Strength", strength)
		f.setProp(p, "Flags", flags)
		f.setProp(p, "WpaFlags", wpa)
		f.setProp(p, "RsnFlags", rsn)
	}
	add("/ap/1", "Home", 60, 1, 0, 0x188)
	add("/ap/2", "Home", 80, 1, 0, 0x188)
	add("/ap/3", "Cafe", 40, 0, 0, 0)
	add("/ap/4", "", 90, 0, 0, 0)
	got, err := newMgr(t, f).ScanWifi(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SSID != "Home" || got[0].Signal != 80 || !got[0].Secured || got[1].SSID != "Cafe" || got[1].Secured {
		t.Errorf("got %+v", got)
	}
}

func TestHotspotLifecycle(t *testing.T) {
	profiles := map[dbus.ObjectPath]settings{pHome: homeSettings()}
	order := []dbus.ObjectPath{pHome}
	f := storeFake(profiles, order)
	wlan := dbus.ObjectPath("/d/2")
	f.handle(nmPath, nmIface+".GetDevices", func([]any) ([]any, error) { return []any{[]dbus.ObjectPath{wlan}}, nil })
	f.setProp(wlan, "DeviceType", uint32(2))
	f.setProp(wlan, "Interface", "wlan0")
	f.handle(nmPath, nmIface+".AddAndActivateConnection", func([]any) ([]any, error) {
		return []any{dbus.ObjectPath("/s/2"), dbus.ObjectPath("/a/2")}, nil
	})
	m := newMgr(t, f)
	spec := platform.HotspotSpec{SSID: "PWS-Setup", PSK: "setup-pass-1"}
	if err := m.StartHotspot(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if len(f.called("AddAndActivateConnection")) != 1 {
		t.Fatal("hotspot not added")
	}

	// Now it exists and is active: a second start is a no-op.
	f2 := storeFake(map[dbus.ObjectPath]settings{pHome: homeSettings(), pHotspot: hotspotStored()}, []dbus.ObjectPath{pHome, pHotspot})
	f2.handle(nmPath, nmIface+".GetDevices", func([]any) ([]any, error) { return []any{[]dbus.ObjectPath{wlan}}, nil })
	f2.setProp(wlan, "DeviceType", uint32(2))
	f2.setProp(wlan, "Interface", "wlan0")
	f2.setProp(nmPath, "ActiveConnections", []dbus.ObjectPath{"/a/2"})
	f2.setProp("/a/2", "Id", hotspotID)
	f2.handle(nmPath, nmIface+".DeactivateConnection", func([]any) ([]any, error) { return nil, nil })
	f2.handle(pHotspot, connIf+".Delete", func([]any) ([]any, error) { return nil, nil })
	m2 := newMgr(t, f2)
	if err := m2.StartHotspot(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if len(f2.called("AddAndActivateConnection")) != 0 {
		t.Error("idempotent start re-added")
	}
	if err := m2.StopHotspot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f2.called("DeactivateConnection")) != 1 || len(f2.called(".Delete")) != 1 {
		t.Errorf("stop calls = %+v", f2.calls)
	}
}

func TestStopHotspotNothingToDo(t *testing.T) {
	f := storeFake(map[dbus.ObjectPath]settings{pHome: homeSettings()}, []dbus.ObjectPath{pHome})
	f.setProp(nmPath, "ActiveConnections", []dbus.ObjectPath{})
	if err := newMgr(t, f).StopHotspot(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHostname(t *testing.T) {
	f := newFake()
	f.setProp(hostnamePath, "StaticHostname", "")
	f.setProp(hostnamePath, "Hostname", "pws-1234")
	m := newMgr(t, f)
	if n, err := m.Hostname(context.Background()); err != nil || n != "pws-1234" {
		t.Errorf("hostname = %q %v", n, err)
	}
	f.setProp(hostnamePath, "StaticHostname", "station")
	if n, _ := m.Hostname(context.Background()); n != "station" {
		t.Errorf("static = %q", n)
	}
	f.handle(hostnamePath, hostnameIf+".SetStaticHostname", func([]any) ([]any, error) { return nil, nil })
	f.handle(hostnamePath, hostnameIf+".SetHostname", func([]any) ([]any, error) { return nil, nil })
	if err := m.SetHostname(context.Background(), "newname"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.calls[0].args[0] != "newname" || f.calls[0].args[1] != false {
		t.Errorf("calls = %+v", f.calls)
	}
	if err := m.SetHostname(context.Background(), ""); err == nil {
		t.Error("empty name accepted")
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Options{PollInterval: -1}); err == nil {
		t.Error("negative option accepted")
	}
	if _, err := New(Options{}); err != nil {
		t.Errorf("New with defaults: %v", err)
	}
}
