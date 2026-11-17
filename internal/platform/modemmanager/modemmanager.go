// Package modemmanager reads the LTE modem state from ModemManager over the
// system D-Bus. The bus is the Bus interface so tests drive it with canned
// properties; the real connection lives in dbus.go.
package modemmanager

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// D-Bus names from the ModemManager API.
const (
	service      = "org.freedesktop.ModemManager1"
	ifaceModem   = "org.freedesktop.ModemManager1.Modem"
	iface3gpp    = "org.freedesktop.ModemManager1.Modem.Modem3gpp"
	ifaceSignal  = "org.freedesktop.ModemManager1.Modem.Signal"
	ifaceTime    = "org.freedesktop.ModemManager1.Modem.Time"
	signalPeriod = 60 // seconds between extended signal refreshes
)

// ErrNoManager means ModemManager is not on the bus.
var ErrNoManager = errors.New("modemmanager: service not available")

// Bus is the slice of D-Bus the reader needs.
type Bus interface {
	// Modems lists the object paths of known modems.
	Modems(ctx context.Context) ([]dbus.ObjectPath, error)
	// Props returns all properties of one interface, empty when the object
	// does not implement it.
	Props(ctx context.Context, path dbus.ObjectPath, iface string) (map[string]dbus.Variant, error)
	// Call invokes a method and returns its output arguments.
	Call(ctx context.Context, path dbus.ObjectPath, iface, method string, args ...any) ([]any, error)
}

// Info is the decoded modem state.
type Info struct {
	Model       string
	State       string
	Carrier     string
	MCCMNC      string
	Reg         string
	Tech        string
	RSSIdBm     *float64
	SignalPct   *int
	NetworkTime *time.Time
}

// Reader queries one Bus.
type Reader struct {
	Bus Bus
	// EnableSignal is tried once, when the service account is not allowed
	// to call Signal.Setup itself. The daemon points it at the root helper.
	EnableSignal func(ctx context.Context) error

	mu    sync.Mutex
	tried bool
}

// Read returns the first modem, or (nil, nil) when there is none.
func (r *Reader) Read(ctx context.Context) (*Info, error) {
	paths, err := r.Bus.Modems(ctx)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	p := paths[0]
	mp, err := r.Bus.Props(ctx, p, ifaceModem)
	if err != nil {
		return nil, err
	}
	info := &Info{
		Model: strings.TrimSpace(str(mp["Manufacturer"]) + " " + str(mp["Model"])),
		State: stateName(i32(mp["State"], 0)),
		Tech:  techName(u32(mp["AccessTechnologies"])),
	}
	if sq, ok := mp["SignalQuality"]; ok {
		if v, ok := sq.Value().([]any); ok && len(v) >= 1 {
			if pct, ok := v[0].(uint32); ok {
				n := int(pct)
				info.SignalPct = &n
			}
		}
	}
	gp, _ := r.Bus.Props(ctx, p, iface3gpp) //nolint:errcheck // a modem without 3GPP data still reports its state
	info.Carrier = str(gp["OperatorName"])
	info.MCCMNC = digits(str(gp["OperatorCode"]))
	info.Reg = regName(u32(gp["RegistrationState"]))

	info.RSSIdBm = r.rssi(ctx, p, info.Tech)
	info.NetworkTime = r.networkTime(ctx, p)
	return info, nil
}

// rssi asks for the extended signal figures. The Signal interface only
// updates after Setup turns polling on, so enable it when the rate is zero.
// A refusal (polkit, old modem) just means no dBm value.
func (r *Reader) rssi(ctx context.Context, p dbus.ObjectPath, tech string) *float64 {
	sp, err := r.Bus.Props(ctx, p, ifaceSignal)
	if err != nil || len(sp) == 0 {
		return nil
	}
	if u32(sp["Rate"]) == 0 {
		if _, err := r.Bus.Call(ctx, p, ifaceSignal, "Setup", uint32(signalPeriod)); err != nil {
			r.mu.Lock()
			first := !r.tried
			r.tried = true
			r.mu.Unlock()
			if first && r.EnableSignal != nil {
				_ = r.EnableSignal(ctx) //nolint:errcheck // without it there is simply no dBm figure
			}
		}
		return nil // the first sample arrives after one period
	}
	key := map[string]string{"lte": "Lte", "umts": "Umts", "gsm": "Gsm"}[tech]
	if key == "" {
		return nil
	}
	m, ok := sp[key].Value().(map[string]dbus.Variant)
	if !ok {
		return nil
	}
	if v, ok := m["rssi"]; ok {
		if f, ok := v.Value().(float64); ok {
			return &f
		}
	}
	return nil
}

func (r *Reader) networkTime(ctx context.Context, p dbus.ObjectPath) *time.Time {
	out, err := r.Bus.Call(ctx, p, ifaceTime, "GetNetworkTime")
	if err != nil || len(out) == 0 {
		return nil
	}
	s, ok := out[0].(string)
	if !ok || s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-07", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

func str(v dbus.Variant) string {
	if s, ok := v.Value().(string); ok {
		return s
	}
	return ""
}

func u32(v dbus.Variant) uint32 {
	if n, ok := v.Value().(uint32); ok {
		return n
	}
	return 0
}

func i32(v dbus.Variant, def int32) int32 {
	if n, ok := v.Value().(int32); ok {
		return n
	}
	return def
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stateName maps MMModemState.
func stateName(s int32) string {
	names := map[int32]string{
		-1: "failed", 0: "unknown", 1: "initializing", 2: "locked", 3: "disabled",
		4: "disabling", 5: "enabling", 6: "enabled", 7: "searching", 8: "registered",
		9: "disconnecting", 10: "connecting", 11: "connected",
	}
	if n, ok := names[s]; ok {
		return n
	}
	return "unknown"
}

// regName maps MMModem3gppRegistrationState.
func regName(s uint32) string {
	switch s {
	case 0:
		return "idle"
	case 1, 6, 9:
		return "home"
	case 2:
		return "searching"
	case 3:
		return "denied"
	case 5, 7, 10:
		return "roaming"
	case 8:
		return "emergency"
	default:
		return "unknown"
	}
}

// techName picks the best technology out of the MMModemAccessTechnology
// bit mask.
func techName(m uint32) string {
	switch {
	case m&(1<<15) != 0:
		return "nr"
	case m&(1<<14|1<<16|1<<17) != 0:
		return "lte"
	case m&(1<<5|1<<6|1<<7|1<<8|1<<9) != 0:
		return "umts"
	case m&(1<<1|1<<2|1<<3|1<<4) != 0:
		return "gsm"
	default:
		return "unknown"
	}
}
