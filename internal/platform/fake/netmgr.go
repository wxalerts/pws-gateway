package fake

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/wxalerts/pws-gateway/internal/platform"
)

// HotspotName is the profile name the fake gives the setup hotspot, matching
// what Connections reports with Hotspot set.
const HotspotName = "pwsgw-setup-hotspot"

// Net is an in-memory platform.NetManager. Devices follow the profiles: a
// device is connected to the profile last activated on it.
type Net struct {
	mu sync.Mutex

	conns   []platform.Connection
	devices []platform.Device
	hotspot *platform.HotspotSpec
	host    string

	// Calls is the ordered log of mutating calls, "save x", "activate x".
	Calls []string
	// ActivateErr makes Activate of a named profile fail.
	ActivateErr map[string]error
	// ConnectivityValue is what Connectivity returns.
	ConnectivityValue platform.Connectivity
	// HotspotErr makes StartHotspot fail.
	HotspotErr error
	// Up lists profile names that can reach upstream; activating anything
	// else leaves the device connected but Connectivity none.
	Up map[string]bool
}

var _ platform.NetManager = (*Net)(nil)

// NewNet returns a manager with an eth0 and a wlan0 device, one DHCP
// Ethernet profile active on eth0, and host name "pws".
func NewNet() *Net {
	n := &Net{
		host:              "pws",
		ConnectivityValue: platform.ConnectivityFull,
		ActivateErr:       map[string]error{},
		Up:                map[string]bool{"wired": true},
		devices: []platform.Device{
			{Name: "eth0", Type: platform.ConnEthernet, State: "connected", Active: "wired", Addresses: []string{"192.168.1.20/24"}},
			{Name: "wlan0", Type: platform.ConnWifi, State: "disconnected"},
		},
		conns: []platform.Connection{
			{Name: "wired", Type: platform.ConnEthernet, Interface: "eth0", Autoconnect: true, IPv4: platform.IPv4Settings{Method: platform.IPv4Auto}},
		},
	}
	return n
}

func (n *Net) log(f string, a ...any) { n.Calls = append(n.Calls, fmt.Sprintf(f, a...)) }

// Devices implements platform.NetManager.
func (n *Net) Devices(context.Context) ([]platform.Device, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.devices), nil
}

// SetDevices replaces the device list (tests).
func (n *Net) SetDevices(d []platform.Device) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.devices = d
}

// Connections implements platform.NetManager. PSKs are never returned.
func (n *Net) Connections(context.Context) ([]platform.Connection, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]platform.Connection, 0, len(n.conns))
	for i := range n.conns {
		out = append(out, strip(n.conns[i]))
	}
	return out, nil
}

func strip(c platform.Connection) platform.Connection {
	if c.Wifi != nil {
		w := *c.Wifi
		w.PSK = ""
		c.Wifi = &w
	}
	return c
}

// StoredPSK returns the secret kept for a Wi-Fi profile.
func (n *Net) StoredPSK(name string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := range n.conns {
		if c := &n.conns[i]; c.Name == name && c.Wifi != nil {
			return c.Wifi.PSK
		}
	}
	return ""
}

// Save implements platform.NetManager.
func (n *Net) Save(_ context.Context, c platform.Connection) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.log("save %s", c.Name)
	for i := range n.conns {
		old := &n.conns[i]
		if old.Name != c.Name {
			continue
		}
		if c.Wifi != nil && c.Wifi.PSK == "" && old.Wifi != nil {
			w := *c.Wifi
			w.PSK = old.Wifi.PSK
			c.Wifi = &w
		}
		n.conns[i] = c
		return nil
	}
	n.conns = append(n.conns, c)
	return nil
}

// Delete implements platform.NetManager.
func (n *Net) Delete(_ context.Context, name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := range n.conns {
		if n.conns[i].Name == name {
			n.conns = slices.Delete(n.conns, i, i+1)
			n.log("delete %s", name)
			return nil
		}
	}
	return platform.ErrNotFound
}

// Activate implements platform.NetManager.
func (n *Net) Activate(_ context.Context, name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.log("activate %s", name)
	if err := n.ActivateErr[name]; err != nil {
		return err
	}
	var conn *platform.Connection
	for i := range n.conns {
		if n.conns[i].Name == name {
			conn = &n.conns[i]
		}
	}
	if conn == nil {
		return platform.ErrNotFound
	}
	for i := range n.devices {
		d := &n.devices[i]
		if d.Type != conn.Type || (conn.Interface != "" && conn.Interface != d.Name) {
			continue
		}
		d.State, d.Active = "connected", name
		d.Addresses = nil
		if conn.IPv4.Method == platform.IPv4Manual {
			d.Addresses = []string{conn.IPv4.Address}
		}
		return nil
	}
	return errors.New("fake: no device for " + name)
}

// ActiveOn returns the profile active on a device, "" when none.
func (n *Net) ActiveOn(dev string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, d := range n.devices {
		if d.Name == dev {
			return d.Active
		}
	}
	return ""
}

// Connectivity implements platform.NetManager. It reports none when no
// connected device runs a profile listed in Up.
func (n *Net) Connectivity(context.Context) (platform.Connectivity, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, d := range n.devices {
		if d.State == "connected" && n.Up[d.Active] {
			return n.ConnectivityValue, nil
		}
	}
	return platform.ConnectivityNone, nil
}

// ScanWifi implements platform.NetManager.
func (n *Net) ScanWifi(context.Context) ([]platform.AccessPoint, error) {
	return []platform.AccessPoint{{SSID: "HomeNet", Signal: 80, Secured: true}}, nil
}

// StartHotspot implements platform.NetManager.
func (n *Net) StartHotspot(_ context.Context, spec platform.HotspotSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.HotspotErr != nil {
		return n.HotspotErr
	}
	if n.hotspot == nil {
		n.log("hotspot-start %s", spec.SSID)
	}
	n.hotspot = &spec
	for i := range n.devices {
		if n.devices[i].Type == platform.ConnWifi {
			n.devices[i].State, n.devices[i].Active = "connected", HotspotName
		}
	}
	return nil
}

// StopHotspot implements platform.NetManager.
func (n *Net) StopHotspot(context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.hotspot != nil {
		n.log("hotspot-stop")
	}
	n.hotspot = nil
	for i := range n.devices {
		if n.devices[i].Active == HotspotName {
			n.devices[i].State, n.devices[i].Active = "disconnected", ""
		}
	}
	return nil
}

// Hotspot returns the running hotspot spec, nil when down.
func (n *Net) Hotspot() *platform.HotspotSpec {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.hotspot
}

// Hostname implements platform.NetManager.
func (n *Net) Hostname(context.Context) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.host, nil
}

// SetHostname implements platform.NetManager.
func (n *Net) SetHostname(_ context.Context, name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.host = name
	n.log("hostname %s", name)
	return nil
}
