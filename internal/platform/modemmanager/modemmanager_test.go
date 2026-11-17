package modemmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

type fakeBus struct {
	modems []dbus.ObjectPath
	props  map[string]map[string]dbus.Variant
	calls  []string
	netT   string
	setupE error
}

func (f *fakeBus) Modems(context.Context) ([]dbus.ObjectPath, error) { return f.modems, nil }
func (f *fakeBus) Props(_ context.Context, _ dbus.ObjectPath, iface string) (map[string]dbus.Variant, error) {
	return f.props[iface], nil
}
func (f *fakeBus) Call(_ context.Context, _ dbus.ObjectPath, iface, method string, _ ...any) ([]any, error) {
	f.calls = append(f.calls, iface+"."+method)
	if method == "Setup" {
		if f.setupE != nil {
			return nil, f.setupE
		}
		f.props[ifaceSignal]["Rate"] = dbus.MakeVariant(uint32(60))
		return nil, nil
	}
	if method == "GetNetworkTime" {
		return []any{f.netT}, nil
	}
	return nil, errors.New("unexpected call")
}

func registeredLTE() *fakeBus {
	v := dbus.MakeVariant
	return &fakeBus{
		modems: []dbus.ObjectPath{"/org/freedesktop/ModemManager1/Modem/0"},
		netT:   "2026-11-17T13:40:00-06:00",
		props: map[string]map[string]dbus.Variant{
			ifaceModem: {
				"Manufacturer": v("QUALCOMM INCORPORATED"), "Model": v("SIMCOM_SIM7600G-H"),
				"State": v(int32(8)), "AccessTechnologies": v(uint32(1 << 14)),
				"SignalQuality": v([]any{uint32(57), true}),
			},
			iface3gpp: {
				"OperatorName": v("AT&T"), "OperatorCode": v("310410"), "RegistrationState": v(uint32(1)),
			},
			ifaceSignal: {
				"Rate": v(uint32(60)),
				"Lte":  v(map[string]dbus.Variant{"rssi": v(-83.0), "rsrp": v(-110.0)}),
			},
		},
	}
}

func TestRegisteredLTEModem(t *testing.T) {
	info, err := (&Reader{Bus: registeredLTE()}).Read(context.Background())
	if err != nil || info == nil {
		t.Fatalf("info=%v err=%v", info, err)
	}
	if info.Carrier != "AT&T" || info.MCCMNC != "310410" || info.Reg != "home" || info.Tech != "lte" || info.State != "registered" {
		t.Errorf("info = %+v", info)
	}
	if info.RSSIdBm == nil || *info.RSSIdBm != -83 || info.SignalPct == nil || *info.SignalPct != 57 {
		t.Errorf("signal = %v / %v", info.RSSIdBm, info.SignalPct)
	}
	if info.NetworkTime == nil || info.NetworkTime.UTC().Hour() != 19 {
		t.Errorf("network time = %v", info.NetworkTime)
	}
}

func TestNoModem(t *testing.T) {
	info, err := (&Reader{Bus: &fakeBus{}}).Read(context.Background())
	if err != nil || info != nil {
		t.Fatalf("want (nil, nil), got %v, %v", info, err)
	}
}

func TestSignalSetupEnabledOnceThenRead(t *testing.T) {
	b := registeredLTE()
	b.props[ifaceSignal]["Rate"] = dbus.MakeVariant(uint32(0))
	r := &Reader{Bus: b}
	info, _ := r.Read(context.Background())
	if info.RSSIdBm != nil {
		t.Error("no dBm figure until the first period has passed")
	}
	if len(b.calls) == 0 || b.calls[0] != ifaceSignal+".Setup" {
		t.Fatalf("Setup not called: %v", b.calls)
	}
	info, _ = r.Read(context.Background())
	if info.RSSIdBm == nil || *info.RSSIdBm != -83 {
		t.Errorf("second read rssi = %v", info.RSSIdBm)
	}
}

func TestSetupRefusedLeavesRSSIEmpty(t *testing.T) {
	b := registeredLTE()
	b.props[ifaceSignal]["Rate"] = dbus.MakeVariant(uint32(0))
	b.setupE = errors.New("not authorized")
	info, err := (&Reader{Bus: b}).Read(context.Background())
	if err != nil || info == nil || info.RSSIdBm != nil || info.Carrier != "AT&T" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
}

func TestVocabulary(t *testing.T) {
	if regName(5) != "roaming" || regName(2) != "searching" || regName(3) != "denied" || techName(1<<15) != "nr" || techName(1<<5) != "umts" || stateName(11) != "connected" {
		t.Error("vocabulary mapping is wrong")
	}
}
