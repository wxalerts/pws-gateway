// Package nmdbus implements platform.NetManager on NetworkManager over the
// system D-Bus, and the host name through systemd-hostnamed. It is pure Go so
// the daemon still cross compiles with CGO disabled.
package nmdbus

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/wxalerts/pws-gateway/internal/platform"
)

var _ platform.NetManager = (*Manager)(nil)

// Options tunes a Manager. The zero value is right for production.
type Options struct {
	// PollInterval is how often Activate checks the active connection.
	// Default 500ms.
	PollInterval time.Duration
	// ScanWait is how long ScanWifi waits after asking for a scan before it
	// reads the results. Default 3s.
	ScanWait time.Duration

	// bus replaces the system bus in tests.
	bus bus
}

// Manager talks to NetworkManager. It is safe for concurrent use.
type Manager struct {
	bus  bus
	poll time.Duration
	scan time.Duration

	// mu serializes the mutating calls so find-then-add in Save cannot race.
	mu sync.Mutex
}

// New returns a Manager. The system bus is opened on first use, not here.
func New(opts Options) (*Manager, error) {
	if opts.PollInterval < 0 || opts.ScanWait < 0 {
		return nil, errors.New("nmdbus: negative duration option")
	}
	m := &Manager{bus: opts.bus, poll: opts.PollInterval, scan: opts.ScanWait}
	if m.bus == nil {
		m.bus = &systemBus{}
	}
	if m.poll == 0 {
		m.poll = 500 * time.Millisecond
	}
	if m.scan == 0 {
		m.scan = 3 * time.Second
	}
	return m, nil
}

func (m *Manager) nmCall(ctx context.Context, path dbus.ObjectPath, method string, args ...any) ([]any, error) {
	return m.bus.call(ctx, nmDest, path, method, args...)
}

func (m *Manager) nmProp(ctx context.Context, path dbus.ObjectPath, iface, name string) (any, error) {
	return m.bus.prop(ctx, nmDest, path, iface, name)
}

// profile is one stored connection with its settings.
type profile struct {
	path dbus.ObjectPath
	set  settings
}

func (m *Manager) profiles(ctx context.Context) ([]profile, error) {
	paths, err := reply0[[]dbus.ObjectPath](m.nmCall(ctx, settingsPath, settingsIf+".ListConnections"))
	if err != nil {
		return nil, fmt.Errorf("nmdbus: list connections: %w", err)
	}
	out := make([]profile, 0, len(paths))
	for _, p := range paths {
		s, err := reply0[settings](m.nmCall(ctx, p, connIf+".GetSettings"))
		if err != nil {
			return nil, fmt.Errorf("nmdbus: get settings %s: %w", p, err)
		}
		out = append(out, profile{path: p, set: s})
	}
	return out, nil
}

func (m *Manager) findProfile(ctx context.Context, name string) (profile, error) {
	ps, err := m.profiles(ctx)
	if err != nil {
		return profile{}, err
	}
	for _, p := range ps {
		if str(p.set["connection"], "id") == name {
			return p, nil
		}
	}
	return profile{}, fmt.Errorf("connection %q: %w", name, platform.ErrNotFound)
}

// Connections lists the saved profiles without any secret.
func (m *Manager) Connections(ctx context.Context) ([]platform.Connection, error) {
	ps, err := m.profiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]platform.Connection, 0, len(ps))
	for _, p := range ps {
		if c, ok := fromSettings(p.set); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// Save creates or updates the profile with the same name. It never activates.
func (m *Manager) Save(ctx context.Context, c platform.Connection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, err := m.findProfile(ctx, c.Name)
	switch {
	case err == nil:
		s, err := toSettings(c, existing.set)
		if err != nil {
			return err
		}
		if _, err := m.nmCall(ctx, existing.path, connIf+".Update", s); err != nil {
			return fmt.Errorf("nmdbus: update %q: %w", c.Name, err)
		}
		return nil
	case errors.Is(err, platform.ErrNotFound):
		s, err := toSettings(c, nil)
		if err != nil {
			return err
		}
		if _, err := m.nmCall(ctx, settingsPath, settingsIf+".AddConnection", s); err != nil {
			return fmt.Errorf("nmdbus: add %q: %w", c.Name, err)
		}
		return nil
	default:
		return err
	}
}

// Delete removes the profile named name.
func (m *Manager) Delete(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.findProfile(ctx, name)
	if err != nil {
		return err
	}
	if _, err := m.nmCall(ctx, p.path, connIf+".Delete"); err != nil {
		return fmt.Errorf("nmdbus: delete %q: %w", name, err)
	}
	return nil
}

// device is one NetworkManager device with the properties the manager uses.
type device struct {
	path dbus.ObjectPath
	name string
	typ  platform.ConnType
	nm   uint32
}

func (m *Manager) deviceList(ctx context.Context) ([]dbus.ObjectPath, error) {
	paths, err := reply0[[]dbus.ObjectPath](m.nmCall(ctx, nmPath, nmIface+".GetDevices"))
	if err != nil {
		return nil, fmt.Errorf("nmdbus: get devices: %w", err)
	}
	return paths, nil
}

func (m *Manager) basicDevices(ctx context.Context) ([]device, error) {
	paths, err := m.deviceList(ctx)
	if err != nil {
		return nil, err
	}
	var out []device
	for _, p := range paths {
		nmType, err := as[uint32](m.nmProp(ctx, p, deviceIf, "DeviceType"))
		if err != nil {
			return nil, fmt.Errorf("nmdbus: device type %s: %w", p, err)
		}
		t, ok := deviceType(nmType)
		if !ok {
			continue
		}
		name, err := as[string](m.nmProp(ctx, p, deviceIf, "Interface"))
		if err != nil {
			return nil, fmt.Errorf("nmdbus: device name %s: %w", p, err)
		}
		out = append(out, device{path: p, name: name, typ: t, nm: nmType})
	}
	return out, nil
}

// Devices lists the interfaces NetworkManager manages or knows about.
func (m *Manager) Devices(ctx context.Context) ([]platform.Device, error) {
	devs, err := m.basicDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]platform.Device, 0, len(devs))
	for _, d := range devs {
		pd := platform.Device{Name: d.name, Type: d.typ}
		state, err := as[uint32](m.nmProp(ctx, d.path, deviceIf, "State"))
		if err != nil {
			return nil, fmt.Errorf("nmdbus: device state %s: %w", d.name, err)
		}
		pd.State = deviceState(state)
		// A device with no active connection or address is normal, so these
		// lookups are best effort.
		if ac, err := as[dbus.ObjectPath](m.nmProp(ctx, d.path, deviceIf, "ActiveConnection")); err == nil && ac != noObject && ac != "" {
			if id, err := as[string](m.nmProp(ctx, ac, activeIf, "Id")); err == nil {
				pd.Active = id
			}
		}
		if mac, err := as[string](m.nmProp(ctx, d.path, deviceIf, "HwAddress")); err == nil {
			pd.MAC = mac
		}
		pd.Addresses = m.deviceAddresses(ctx, d.path)
		out = append(out, pd)
	}
	return out, nil
}

func (m *Manager) deviceAddresses(ctx context.Context, dev dbus.ObjectPath) []string {
	cfg, err := as[dbus.ObjectPath](m.nmProp(ctx, dev, deviceIf, "Ip4Config"))
	if err != nil || cfg == noObject || cfg == "" {
		return nil
	}
	list, err := as[[]map[string]dbus.Variant](m.nmProp(ctx, cfg, ip4If, "AddressData"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range list {
		if a := cidrFromEntry(e); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// pickDevice chooses the device a profile should run on. An empty result
// object path lets NetworkManager choose.
func (m *Manager) pickDevice(ctx context.Context, c platform.Connection, ifaceHint string) dbus.ObjectPath {
	devs, err := m.basicDevices(ctx)
	if err != nil {
		return noObject
	}
	want := ifaceHint
	if want == "" {
		want = c.Interface
	}
	for _, d := range devs {
		if d.typ == c.Type && (want == "" || d.name == want) {
			return d.path
		}
	}
	return noObject
}

// Activate brings the profile up and waits for the attempt to finish.
func (m *Manager) Activate(ctx context.Context, name string) error {
	p, err := m.findProfile(ctx, name)
	if err != nil {
		return err
	}
	c, ok := fromSettings(p.set)
	if !ok {
		return fmt.Errorf("nmdbus: connection %q has an unsupported type", name)
	}
	dev := m.pickDevice(ctx, c, "")
	active, err := reply0[dbus.ObjectPath](m.nmCall(ctx, nmPath, nmIface+".ActivateConnection", p.path, dev, noObject))
	if err != nil {
		return fmt.Errorf("nmdbus: activate %q: %w", name, err)
	}
	return m.waitActive(ctx, active, name)
}

// waitActive polls the active connection until it is activated or has
// failed, or ctx ends. Polling keeps this free of signal plumbing and easy to
// fake.
func (m *Manager) waitActive(ctx context.Context, active dbus.ObjectPath, name string) error {
	t := time.NewTicker(m.poll)
	defer t.Stop()
	for {
		st, err := as[uint32](m.nmProp(ctx, active, activeIf, "State"))
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// The object vanishes when activation fails and is cleaned up.
			return fmt.Errorf("nmdbus: activation of %q did not complete: %w", name, err)
		}
		if done, ok := activeStateDone(st); done {
			if ok {
				return nil
			}
			return fmt.Errorf("nmdbus: activation of %q failed", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// Connectivity runs a connectivity check.
func (m *Manager) Connectivity(ctx context.Context) (platform.Connectivity, error) {
	s, err := reply0[uint32](m.nmCall(ctx, nmPath, nmIface+".CheckConnectivity"))
	if err != nil {
		return platform.ConnectivityUnknown, fmt.Errorf("nmdbus: check connectivity: %w", err)
	}
	return connectivityFromNM(s), nil
}

func (m *Manager) wifiDevice(ctx context.Context, iface string) (device, error) {
	devs, err := m.basicDevices(ctx)
	if err != nil {
		return device{}, err
	}
	for _, d := range devs {
		if d.typ == platform.ConnWifi && (iface == "" || d.name == iface) {
			return d, nil
		}
	}
	return device{}, fmt.Errorf("wifi device %q: %w", iface, platform.ErrNotFound)
}

// ScanWifi asks every Wi-Fi device to scan and returns the networks seen,
// strongest first, one entry per SSID.
func (m *Manager) ScanWifi(ctx context.Context) ([]platform.AccessPoint, error) {
	devs, err := m.basicDevices(ctx)
	if err != nil {
		return nil, err
	}
	var wifi []device
	for _, d := range devs {
		if d.typ == platform.ConnWifi {
			wifi = append(wifi, d)
		}
	}
	if len(wifi) == 0 {
		return nil, fmt.Errorf("wifi device: %w", platform.ErrNotFound)
	}
	for _, d := range wifi {
		m.requestScan(ctx, d.path)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(m.scan):
	}
	best := map[string]platform.AccessPoint{}
	for _, d := range wifi {
		aps, err := reply0[[]dbus.ObjectPath](m.nmCall(ctx, d.path, wirelessIf+".GetAllAccessPoints"))
		if err != nil {
			return nil, fmt.Errorf("nmdbus: access points on %s: %w", d.name, err)
		}
		for _, p := range aps {
			ap, ok := m.readAP(ctx, p)
			if !ok {
				continue
			}
			if old, seen := best[ap.SSID]; !seen || ap.Signal > old.Signal {
				best[ap.SSID] = ap
			}
		}
	}
	out := make([]platform.AccessPoint, 0, len(best))
	for _, ap := range best {
		out = append(out, ap)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Signal != out[j].Signal {
			return out[i].Signal > out[j].Signal
		}
		return out[i].SSID < out[j].SSID
	})
	return out, nil
}

// requestScan asks one device to scan. NetworkManager rejects a scan that
// follows another too closely, and the cached list is still worth returning,
// so a failure here is deliberately not reported.
func (m *Manager) requestScan(ctx context.Context, dev dbus.ObjectPath) {
	if _, err := m.nmCall(ctx, dev, wirelessIf+".RequestScan", map[string]dbus.Variant{}); err != nil {
		return
	}
}

// u32OrZero reads an access point flag property, treating a missing one as no
// flags set.
func (m *Manager) u32OrZero(ctx context.Context, p dbus.ObjectPath, name string) uint32 {
	v, err := as[uint32](m.nmProp(ctx, p, apIf, name))
	if err != nil {
		return 0
	}
	return v
}

// readAP reads one access point. Hidden networks have an empty SSID and are
// skipped, as are points that vanished between listing and reading.
func (m *Manager) readAP(ctx context.Context, p dbus.ObjectPath) (platform.AccessPoint, bool) {
	ssid, err := as[[]byte](m.nmProp(ctx, p, apIf, "Ssid"))
	if err != nil || len(ssid) == 0 {
		return platform.AccessPoint{}, false
	}
	strength, err := as[byte](m.nmProp(ctx, p, apIf, "Strength"))
	if err != nil {
		return platform.AccessPoint{}, false
	}
	flags := m.u32OrZero(ctx, p, "Flags")
	wpa := m.u32OrZero(ctx, p, "WpaFlags")
	rsn := m.u32OrZero(ctx, p, "RsnFlags")
	return platform.AccessPoint{
		SSID:    string(ssid),
		Signal:  int(strength),
		Secured: apSecured(flags, wpa, rsn),
	}, true
}

// activeByID returns the active connection path whose id is id, if any.
func (m *Manager) activeByID(ctx context.Context, id string) (dbus.ObjectPath, bool, error) {
	paths, err := as[[]dbus.ObjectPath](m.nmProp(ctx, nmPath, nmIface, "ActiveConnections"))
	if err != nil {
		return "", false, fmt.Errorf("nmdbus: active connections: %w", err)
	}
	for _, p := range paths {
		got, err := as[string](m.nmProp(ctx, p, activeIf, "Id"))
		if err == nil && got == id {
			return p, true, nil
		}
	}
	return "", false, nil
}

// StartHotspot brings up the setup access point. If it is already running
// with the same SSID nothing changes; otherwise the old profile is replaced.
func (m *Manager) StartHotspot(ctx context.Context, spec platform.HotspotSpec) error {
	set, err := hotspotSettings(spec)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dev, err := m.wifiDevice(ctx, spec.Interface)
	if err != nil {
		return err
	}
	old, err := m.findProfile(ctx, hotspotID)
	switch {
	case err == nil:
		if c, ok := fromSettings(old.set); ok && c.Wifi != nil && c.Wifi.SSID == spec.SSID {
			if _, up, err := m.activeByID(ctx, hotspotID); err == nil && up {
				return nil
			}
		}
		if err := m.stopLocked(ctx); err != nil {
			return err
		}
	case errors.Is(err, platform.ErrNotFound):
	default:
		return err
	}
	if _, err := m.nmCall(ctx, nmPath, nmIface+".AddAndActivateConnection", set, dev.path, noObject); err != nil {
		return fmt.Errorf("nmdbus: start hotspot: %w", err)
	}
	return nil
}

// StopHotspot deactivates and deletes the setup access point. It is a no-op
// when none exists.
func (m *Manager) StopHotspot(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(ctx)
}

func (m *Manager) stopLocked(ctx context.Context) error {
	active, up, err := m.activeByID(ctx, hotspotID)
	if err != nil {
		return err
	}
	if up {
		if _, err := m.nmCall(ctx, nmPath, nmIface+".DeactivateConnection", active); err != nil {
			return fmt.Errorf("nmdbus: deactivate hotspot: %w", err)
		}
	}
	p, err := m.findProfile(ctx, hotspotID)
	if errors.Is(err, platform.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := m.nmCall(ctx, p.path, connIf+".Delete"); err != nil {
		return fmt.Errorf("nmdbus: delete hotspot profile: %w", err)
	}
	return nil
}

// Hostname returns the static host name, falling back to the transient one.
func (m *Manager) Hostname(ctx context.Context) (string, error) {
	n, err := as[string](m.bus.prop(ctx, hostnameDest, hostnamePath, hostnameIf, "StaticHostname"))
	if err != nil {
		return "", fmt.Errorf("nmdbus: read static hostname: %w", err)
	}
	if n != "" {
		return n, nil
	}
	n, err = as[string](m.bus.prop(ctx, hostnameDest, hostnamePath, hostnameIf, "Hostname"))
	if err != nil {
		return "", fmt.Errorf("nmdbus: read hostname: %w", err)
	}
	return n, nil
}

// SetHostname sets both the static and the running host name. interactive is
// false so polkit does not wait for a prompt nobody can answer.
func (m *Manager) SetHostname(ctx context.Context, name string) error {
	if name == "" || len(name) > 63 {
		return errors.New("nmdbus: host name must be 1 to 63 characters")
	}
	for _, method := range []string{"SetStaticHostname", "SetHostname"} {
		if _, err := m.bus.call(ctx, hostnameDest, hostnamePath, hostnameIf+"."+method, name, false); err != nil {
			return fmt.Errorf("nmdbus: %s: %w", method, err)
		}
	}
	return nil
}
