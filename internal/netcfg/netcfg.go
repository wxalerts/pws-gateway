// Package netcfg changes the host network without ever stranding a remote
// station. A change is applied through the platform's NetManager and then has
// ConfirmWindow (90 seconds) to be confirmed from the browser at the new
// address; if it is not, the previous profiles are restored. If no known
// network is reachable the gateway raises a setup hotspot instead.
//
// The commit-confirm state machine is config.Commit, the same one the signed
// config command and the boot-config changes use, so there is one
// implementation of "apply, confirm or undo".
package netcfg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/atomicfile"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

// ConfirmWindow is how long a change has to be confirmed before it is undone.
const ConfirmWindow = 90 * time.Second

// HotspotName is the NetworkManager profile name of the setup hotspot.
const HotspotName = "pwsgw-setup-hotspot"

const pendingFile = "netcfg-pending.json"

// Errors.
var (
	ErrInvalid    = errors.New("netcfg: invalid change")
	ErrNoPending  = errors.New("netcfg: no change is waiting for confirmation")
	ErrBusy       = errors.New("netcfg: a change is already waiting for confirmation")
	ErrActiveConn = errors.New("netcfg: that connection is in use, switch to another one first")
	ErrProtected  = errors.New("netcfg: that connection is managed by the gateway")
)

// Snapshot is the manageable network configuration: the profiles by name,
// which profile is active on each device, and the host name. PSKs are never in
// it (NetworkManager does not hand them out), which is why a change may not
// delete a profile: restoring a deleted Wi-Fi profile could not restore its
// passphrase.
type Snapshot struct {
	Connections map[string]platform.Connection `json:"connections"`
	Active      map[string]string              `json:"active"`
	Hostname    string                         `json:"hostname"`
}

// Change is what the UI submits: profiles to create or update and an optional
// new host name. Profiles not mentioned are left alone.
type Change struct {
	Connections []platform.Connection `json:"connections"`
	Hostname    string                `json:"hostname,omitempty"`
}

// Pending describes an applied, unconfirmed change.
type Pending struct {
	Deadline time.Time `json:"deadline"`
	Changed  []string  `json:"changed"`
}

// State is the network page's data.
type State struct {
	Devices      []platform.Device     `json:"devices"`
	Connections  []platform.Connection `json:"connections"`
	Connectivity platform.Connectivity `json:"connectivity"`
	Hostname     string                `json:"hostname"`
	Pending      *Pending              `json:"pending,omitempty"`
	Hotspot      *HotspotInfo          `json:"hotspot,omitempty"`
}

// HotspotInfo says the setup hotspot is up. The passphrase is not included.
type HotspotInfo struct {
	SSID string `json:"ssid"`
}

// Options configures a Manager.
type Options struct {
	NM      platform.NetManager
	DataDir string
	Now     func() time.Time
	Logger  *slog.Logger
	// Window overrides ConfirmWindow (tests).
	Window time.Duration
	// Fallback tunes the access point failback. Zero values take defaults.
	Fallback FallbackOptions
}

// Manager applies network changes with commit-confirm and runs the AP
// failback.
type Manager struct {
	o   Options
	log *slog.Logger
	now func() time.Time

	mu      sync.Mutex // guards the fields below; held across NM calls on purpose
	commit  *config.Commit[Snapshot]
	changed []string
	fb      fallbackState
}

// New builds a Manager.
func New(o Options) *Manager {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Window <= 0 {
		o.Window = ConfirmWindow
	}
	o.Fallback = o.Fallback.withDefaults()
	return &Manager{o: o, log: o.Logger, now: o.Now}
}

// ---- snapshot and reconcile ----------------------------------------------

func (m *Manager) snapshot(ctx context.Context) (Snapshot, error) {
	conns, err := m.o.NM.Connections(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	devs, err := m.o.NM.Devices(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	host, err := m.o.NM.Hostname(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{Connections: map[string]platform.Connection{}, Active: map[string]string{}, Hostname: host}
	for i := range conns {
		if c := &conns[i]; !c.Hotspot && c.Type != platform.ConnCellular && c.Name != HotspotName {
			s.Connections[c.Name] = *c
		}
	}
	for _, d := range devs {
		if d.Active != "" && d.Active != HotspotName {
			s.Active[d.Name] = d.Active
		}
	}
	return s, nil
}

// reconcile makes the host match want: save changed profiles, remove
// profiles that exist but are not wanted (only ever profiles a change
// created), activate what a device should run, set the host name. It is the
// apply callback for both directions of the commit.
func (m *Manager) reconcile(want Snapshot) error {
	ctx := context.Background()
	have, err := m.snapshot(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(have.Connections)) {
		if _, keep := want.Connections[name]; !keep {
			errs = append(errs, m.o.NM.Delete(ctx, name))
		}
	}
	activate := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(want.Connections)) {
		if changedConn(have, want, name) {
			errs = append(errs, m.o.NM.Save(ctx, want.Connections[name]))
			activate[name] = true
		}
	}
	for dev, name := range want.Active {
		if have.Active[dev] != name {
			activate[name] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(activate)) {
		if _, ok := want.Connections[name]; ok {
			errs = append(errs, m.o.NM.Activate(ctx, name))
		}
	}
	if want.Hostname != "" && want.Hostname != have.Hostname {
		errs = append(errs, m.o.NM.SetHostname(ctx, want.Hostname))
	}
	return errors.Join(errs...)
}

func changedConn(have, want Snapshot, name string) bool {
	h, ok := have.Connections[name]
	return !ok || !sameConn(h, want.Connections[name]) || want.Connections[name].Wifi != nil && want.Connections[name].Wifi.PSK != ""
}

func sameConn(a, b platform.Connection) bool {
	a, b = stripPSK(a), stripPSK(b)
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

func stripPSK(c platform.Connection) platform.Connection {
	if c.Wifi != nil {
		w := *c.Wifi
		w.PSK = ""
		c.Wifi = &w
	}
	return c
}

// ---- apply, confirm, revert ----------------------------------------------

// Apply validates the change, applies it and starts the confirmation window.
// It returns the deadline. A reconcile failure is rolled back immediately.
func (m *Manager) Apply(ctx context.Context, ch Change) (Pending, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if m.commit != nil {
		return Pending{}, ErrBusy
	}
	if err := validateChange(ch); err != nil {
		return Pending{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	prev, err := m.snapshot(ctx)
	if err != nil {
		return Pending{}, err
	}
	cand := Snapshot{Connections: maps.Clone(prev.Connections), Active: maps.Clone(prev.Active), Hostname: prev.Hostname}
	var changed []string
	for i := range ch.Connections {
		c := ch.Connections[i]
		if old, ok := prev.Connections[c.Name]; ok && old.Type != c.Type {
			return Pending{}, fmt.Errorf("netcfg: %q is a %s profile, not %s", c.Name, old.Type, c.Type)
		}
		cand.Connections[c.Name] = c
		changed = append(changed, c.Name)
		// Activate the changed profile on its interface by recording it.
		if c.Interface != "" {
			cand.Active[c.Interface] = c.Name
		}
	}
	if ch.Hostname != "" {
		cand.Hostname = ch.Hostname
		changed = append(changed, "hostname")
	}
	if err := m.savePending(prev); err != nil {
		return Pending{}, err
	}
	c := config.NewCommit(prev, m.reconcile, m.now)
	if err := c.Stage(cand); err != nil {
		return Pending{}, err
	}
	deadline, err := c.Apply(m.o.Window)
	if err != nil {
		// Apply failed part way: put things back, do not leave a half change.
		if rerr := m.reconcile(prev); rerr != nil {
			m.log.Error("netcfg: restore after failed apply", "err", rerr)
		}
		m.clearPending()
		return Pending{}, err
	}
	m.commit, m.changed = c, changed
	return Pending{Deadline: deadline, Changed: changed}, nil
}

// Confirm keeps the applied change. After the deadline it rolls back instead
// and returns config.ErrDeadlinePassed.
func (m *Manager) Confirm() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.commit == nil {
		return ErrNoPending
	}
	err := m.commit.Confirm()
	m.commit, m.changed = nil, nil
	m.clearPending()
	return err
}

// Revert undoes an unconfirmed change now.
func (m *Manager) Revert() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.commit == nil {
		return ErrNoPending
	}
	err := m.commit.Rollback()
	m.commit, m.changed = nil, nil
	m.clearPending()
	return err
}

// Tick reverts an unconfirmed change whose window has passed. Run calls it
// every second; tests call it after advancing the fake clock.
func (m *Manager) Tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
}

func (m *Manager) expireLocked() {
	if m.commit == nil {
		return
	}
	did, err := m.commit.Expire()
	if err != nil {
		m.log.Error("netcfg: revert after unconfirmed change", "err", err)
	}
	if did {
		m.log.Warn("netcfg: change was not confirmed in time, previous settings restored", "changed", m.changed)
		m.commit, m.changed = nil, nil
		m.clearPending()
	}
}

// Resume runs at start. A change that was applied but never confirmed when the
// daemon stopped is reverted: failing safe beats guessing whether the
// operator got through.
func (m *Manager) Resume(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(m.o.DataDir, pendingFile))
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var prev Snapshot
	if err == nil {
		err = json.Unmarshal(b, &prev)
	}
	if err != nil {
		m.log.Error("netcfg: unreadable pending file, removing it", "err", err)
		m.clearPending()
		return
	}
	m.log.Warn("netcfg: restoring the settings from before an unconfirmed change")
	if err := m.reconcile(prev); err != nil {
		m.log.Error("netcfg: restore failed", "err", err)
		return
	}
	m.clearPending()
	_ = ctx
}

func (m *Manager) savePending(prev Snapshot) error {
	b, err := json.Marshal(prev)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.o.DataDir, 0o750); err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(m.o.DataDir, pendingFile), b, 0o600)
}

func (m *Manager) clearPending() {
	if err := os.Remove(filepath.Join(m.o.DataDir, pendingFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		m.log.Warn("netcfg: remove pending file", "err", err)
	}
}

// Delete removes a profile that is not in use. It is immediate, not
// commit-confirm, because a deleted profile cannot be restored.
func (m *Manager) Delete(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == HotspotName {
		return ErrProtected
	}
	devs, err := m.o.NM.Devices(ctx)
	if err != nil {
		return err
	}
	for _, d := range devs {
		if d.Active == name {
			return ErrActiveConn
		}
	}
	return m.o.NM.Delete(ctx, name)
}

// State reads the live network state for the page.
func (m *Manager) State(ctx context.Context) (State, error) {
	m.mu.Lock()
	m.expireLocked()
	var p *Pending
	if m.commit != nil {
		if d := m.commit.State().Deadline; d != nil {
			p = &Pending{Deadline: *d, Changed: slices.Clone(m.changed)}
		}
	}
	hs := m.fb.hotspotSSID
	m.mu.Unlock()

	var st State
	var err error
	if st.Devices, err = m.o.NM.Devices(ctx); err != nil {
		return st, err
	}
	if st.Connections, err = m.o.NM.Connections(ctx); err != nil {
		return st, err
	}
	if st.Connectivity, err = m.o.NM.Connectivity(ctx); err != nil {
		st.Connectivity = platform.ConnectivityUnknown
	}
	if st.Hostname, err = m.o.NM.Hostname(ctx); err != nil {
		st.Hostname = ""
	}
	st.Pending = p
	if hs != "" {
		st.Hotspot = &HotspotInfo{SSID: hs}
	}
	return st, nil
}

// ScanWifi lists visible Wi-Fi networks.
func (m *Manager) ScanWifi(ctx context.Context) ([]platform.AccessPoint, error) {
	return m.o.NM.ScanWifi(ctx)
}

// Run ticks the confirmation timer every second and the failback every
// FallbackOptions.Every until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	m.Resume(ctx)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var n int
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Tick()
			n++
			if n%int(m.o.Fallback.Every/time.Second) == 0 {
				m.FallbackTick(ctx)
			}
		}
	}
}

// ---- validation -----------------------------------------------------------

var (
	reHostname = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	reDomain   = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	reName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,62}$`)
)

func validateChange(ch Change) error {
	if len(ch.Connections) == 0 && ch.Hostname == "" {
		return errors.New("netcfg: the change is empty")
	}
	if ch.Hostname != "" && !reHostname.MatchString(ch.Hostname) {
		return fmt.Errorf("netcfg: host name %q must be lowercase letters, digits and hyphens", ch.Hostname)
	}
	seen := map[string]bool{}
	for i := range ch.Connections {
		c := &ch.Connections[i]
		if seen[c.Name] {
			return fmt.Errorf("netcfg: %q appears twice", c.Name)
		}
		seen[c.Name] = true
		if err := validateConn(*c); err != nil {
			return err
		}
	}
	return nil
}

func validateConn(c platform.Connection) error {
	switch {
	case !reName.MatchString(c.Name):
		return fmt.Errorf("netcfg: connection name %q is not allowed", c.Name)
	case c.Name == HotspotName || c.Hotspot:
		return ErrProtected
	case c.Type != platform.ConnEthernet && c.Type != platform.ConnWifi:
		return fmt.Errorf("netcfg: %s connections cannot be changed here", c.Type)
	}
	if c.Type == platform.ConnWifi {
		if c.Wifi == nil || c.Wifi.SSID == "" || len(c.Wifi.SSID) > 32 {
			return errors.New("netcfg: a Wi-Fi connection needs an SSID of 1 to 32 bytes")
		}
		if n := len(c.Wifi.PSK); n != 0 && (n < 8 || n > 63) {
			return errors.New("netcfg: a Wi-Fi passphrase must be 8 to 63 characters")
		}
	} else if c.Wifi != nil {
		return errors.New("netcfg: an Ethernet connection has no Wi-Fi settings")
	}
	return validateIPv4(c.IPv4)
}

func validateIPv4(v platform.IPv4Settings) error {
	switch v.Method {
	case platform.IPv4Auto:
		if v.Address != "" || v.Gateway != "" {
			return errors.New("netcfg: an automatic connection takes no address or gateway")
		}
	case platform.IPv4Manual:
		p, err := netip.ParsePrefix(v.Address)
		if err != nil || !p.Addr().Is4() {
			return fmt.Errorf("netcfg: address %q must be IPv4 in CIDR form, like 192.168.1.20/24", v.Address)
		}
		gw, err := netip.ParseAddr(v.Gateway)
		if err != nil || !gw.Is4() {
			return fmt.Errorf("netcfg: gateway %q is not an IPv4 address", v.Gateway)
		}
		if !p.Masked().Contains(gw) {
			return fmt.Errorf("netcfg: gateway %s is outside %s", gw, p.Masked())
		}
		if gw == p.Addr() {
			return errors.New("netcfg: the gateway is the same as the address")
		}
	default:
		return fmt.Errorf("netcfg: IPv4 method %q must be auto or manual", v.Method)
	}
	for _, d := range v.DNS {
		if a, err := netip.ParseAddr(d); err != nil || !a.Is4() {
			return fmt.Errorf("netcfg: DNS server %q is not an IPv4 address", d)
		}
	}
	for _, d := range v.DNSSearch {
		if len(d) > 253 || !reDomain.MatchString(d) {
			return fmt.Errorf("netcfg: search domain %q is not a valid domain name", d)
		}
	}
	return nil
}
