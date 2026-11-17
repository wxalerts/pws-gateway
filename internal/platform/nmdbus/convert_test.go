package nmdbus

import (
	"errors"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/wxalerts/pws-gateway/internal/platform"
)

func TestDeviceState(t *testing.T) {
	cases := map[uint32]string{
		0: "unavailable", 10: "unavailable", 20: "unavailable",
		30: "disconnected", 40: "connecting", 70: "connecting", 90: "connecting",
		100: "connected", 110: "disconnected", 120: "disconnected",
	}
	for in, want := range cases {
		if got := deviceState(in); got != want {
			t.Errorf("deviceState(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestDeviceType(t *testing.T) {
	cases := []struct {
		in   uint32
		want platform.ConnType
		ok   bool
	}{{1, platform.ConnEthernet, true}, {2, platform.ConnWifi, true}, {8, platform.ConnCellular, true}, {14, "", false}, {32, "", false}}
	for _, c := range cases {
		got, ok := deviceType(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("deviceType(%d) = %q,%v", c.in, got, ok)
		}
	}
}

func TestConnectivityFromNM(t *testing.T) {
	cases := map[uint32]platform.Connectivity{
		0: platform.ConnectivityUnknown, 1: platform.ConnectivityNone, 2: platform.ConnectivityLimited,
		3: platform.ConnectivityLimited, 4: platform.ConnectivityFull, 9: platform.ConnectivityUnknown,
	}
	for in, want := range cases {
		if got := connectivityFromNM(in); got != want {
			t.Errorf("connectivityFromNM(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAPSecured(t *testing.T) {
	cases := []struct {
		f, w, r uint32
		want    bool
	}{{0, 0, 0, false}, {1, 0, 0, true}, {0, 0x188, 0, true}, {0, 0, 0x188, true}}
	for _, c := range cases {
		if got := apSecured(c.f, c.w, c.r); got != c.want {
			t.Errorf("apSecured(%d,%d,%d) = %v", c.f, c.w, c.r, got)
		}
	}
}

func TestActiveStateDone(t *testing.T) {
	cases := []struct {
		in       uint32
		done, ok bool
	}{{0, false, false}, {1, false, false}, {2, true, true}, {3, true, false}, {4, true, false}}
	for _, c := range cases {
		d, ok := activeStateDone(c.in)
		if d != c.done || ok != c.ok {
			t.Errorf("activeStateDone(%d) = %v,%v", c.in, d, ok)
		}
	}
}

func TestConnTypeMapping(t *testing.T) {
	for _, tc := range []platform.ConnType{platform.ConnEthernet, platform.ConnWifi} {
		nm, err := connTypeToNM(tc)
		if err != nil {
			t.Fatal(err)
		}
		back, ok := nmToConnType(nm)
		if !ok || back != tc {
			t.Errorf("round trip %q -> %q -> %q", tc, nm, back)
		}
	}
	if _, err := connTypeToNM(platform.ConnCellular); !errors.Is(err, errReadOnly) {
		t.Errorf("cellular err = %v", err)
	}
	if _, ok := nmToConnType("vpn"); ok {
		t.Error("vpn should be unsupported")
	}
}

func TestIPv4Section(t *testing.T) {
	sec, err := ipv4Section(platform.IPv4Settings{
		Method: platform.IPv4Manual, Address: "192.168.1.20/24", Gateway: "192.168.1.1",
		DNS: []string{"1.1.1.1"}, DNSSearch: []string{"lan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sec["method"].Value() != "manual" || sec["gateway"].Value() != "192.168.1.1" {
		t.Errorf("sec = %v", sec)
	}
	if got := addrFromData(sec, "address-data"); got != "192.168.1.20/24" {
		t.Errorf("address = %q", got)
	}
	bad := []platform.IPv4Settings{
		{Method: "bogus"},
		{Method: platform.IPv4Manual, Address: "nope"},
		{Method: platform.IPv4Manual, Address: "10.0.0.2/24", Gateway: "x"},
		{Method: platform.IPv4Auto, DNS: []string{"x"}},
	}
	for _, b := range bad {
		if _, err := ipv4Section(b); err == nil {
			t.Errorf("expected error for %+v", b)
		}
	}
	auto, err := ipv4Section(platform.IPv4Settings{})
	if err != nil || auto["method"].Value() != "auto" {
		t.Errorf("default method: %v %v", auto, err)
	}
}

func TestLegacyDNS(t *testing.T) {
	// 8.8.4.4 as a little endian uint32 is 0x04040808.
	sec := map[string]dbus.Variant{"dns": dbus.MakeVariant([]uint32{0x04040808})}
	if got := legacyDNS(sec); !reflect.DeepEqual(got, []string{"8.8.4.4"}) {
		t.Errorf("legacyDNS = %v", got)
	}
}

func TestToSettingsNewWifi(t *testing.T) {
	s, err := toSettings(platform.Connection{
		Name: "home", Type: platform.ConnWifi, Autoconnect: true, Priority: 5, Interface: "wlan0",
		IPv4: platform.IPv4Settings{Method: platform.IPv4Auto},
		Wifi: &platform.WifiSettings{SSID: "HomeNet", Hidden: true, PSK: "hunter2hunter2"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := s["connection"]
	if conn["id"].Value() != "home" || conn["type"].Value() != nmWifi || conn["interface-name"].Value() != "wlan0" {
		t.Errorf("connection = %v", conn)
	}
	if conn["autoconnect-priority"].Value() != int32(5) {
		t.Errorf("priority = %v", conn["autoconnect-priority"])
	}
	if got := string(s[nmWifi]["ssid"].Value().([]byte)); got != "HomeNet" {
		t.Errorf("ssid = %q", got)
	}
	if s["802-11-wireless-security"]["psk"].Value() != "hunter2hunter2" {
		t.Error("psk missing")
	}
}

func TestToSettingsValidation(t *testing.T) {
	cases := map[string]platform.Connection{
		"no name":    {Type: platform.ConnEthernet},
		"cellular":   {Name: "lte", Type: platform.ConnCellular},
		"hotspot":    {Name: "x", Type: platform.ConnWifi, Hotspot: true},
		"hotspot id": {Name: hotspotID, Type: platform.ConnWifi, Wifi: &platform.WifiSettings{SSID: "a"}},
		"no ssid":    {Name: "w", Type: platform.ConnWifi},
		"short psk":  {Name: "w", Type: platform.ConnWifi, Wifi: &platform.WifiSettings{SSID: "a", PSK: "short"}},
		"bad type":   {Name: "w", Type: "foo"},
	}
	for name, c := range cases {
		if _, err := toSettings(c, nil); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestToSettingsUpdateKeepsSecretBlockAndUnknownKeys(t *testing.T) {
	existing := settings{
		"connection":               {"id": dbus.MakeVariant("home"), "type": dbus.MakeVariant(nmWifi), "uuid": dbus.MakeVariant("abc")},
		"ipv4":                     {"method": dbus.MakeVariant("manual"), "address-data": dbus.MakeVariant([]map[string]dbus.Variant{}), "route-metric": dbus.MakeVariant(int64(100))},
		nmWifi:                     {"ssid": dbus.MakeVariant([]byte("HomeNet")), "mode": dbus.MakeVariant("infrastructure")},
		"802-11-wireless-security": {"key-mgmt": dbus.MakeVariant("wpa-psk")},
	}
	s, err := toSettings(platform.Connection{
		Name: "home", Type: platform.ConnWifi, IPv4: platform.IPv4Settings{Method: platform.IPv4Auto},
		Wifi: &platform.WifiSettings{SSID: "HomeNet"},
	}, existing)
	if err != nil {
		t.Fatal(err)
	}
	if s["connection"]["uuid"].Value() != "abc" || s[nmWifi]["mode"].Value() != "infrastructure" || s["ipv4"]["route-metric"].Value() != int64(100) {
		t.Errorf("unknown keys lost: %v", s)
	}
	sec := s["802-11-wireless-security"]
	if sec["key-mgmt"].Value() != "wpa-psk" {
		t.Errorf("security block lost: %v", sec)
	}
	if _, ok := sec["psk"]; ok {
		t.Error("psk must not be sent when none was supplied")
	}
	if _, ok := s["ipv4"]["address-data"]; ok {
		t.Error("stale address-data kept")
	}
	if s["ipv4"]["method"].Value() != "auto" {
		t.Error("method not replaced")
	}
	// Input must not be mutated.
	if existing["ipv4"]["method"].Value() != "manual" {
		t.Error("existing settings mutated")
	}
	if _, err := toSettings(platform.Connection{Name: "home", Type: platform.ConnEthernet}, existing); err == nil {
		t.Error("type change should fail")
	}
}

func TestFromSettings(t *testing.T) {
	s := settings{
		"connection": {"id": dbus.MakeVariant("home"), "type": dbus.MakeVariant(nmWifi), "autoconnect": dbus.MakeVariant(false), "autoconnect-priority": dbus.MakeVariant(int32(3))},
		"ipv4": {
			"method":       dbus.MakeVariant("manual"),
			"address-data": dbus.MakeVariant([]map[string]dbus.Variant{{"address": dbus.MakeVariant("10.0.0.2"), "prefix": dbus.MakeVariant(uint32(24))}}),
			"gateway":      dbus.MakeVariant("10.0.0.1"),
			"dns-data":     dbus.MakeVariant([]string{"9.9.9.9"}),
		},
		nmWifi:                     {"ssid": dbus.MakeVariant([]byte("HomeNet")), "hidden": dbus.MakeVariant(true)},
		"802-11-wireless-security": {"psk": dbus.MakeVariant("leaked")},
	}
	c, ok := fromSettings(s)
	if !ok {
		t.Fatal("not ok")
	}
	want := platform.Connection{
		Name: "home", Type: platform.ConnWifi, Autoconnect: false, Priority: 3,
		IPv4: platform.IPv4Settings{Method: platform.IPv4Manual, Address: "10.0.0.2/24", Gateway: "10.0.0.1", DNS: []string{"9.9.9.9"}},
		Wifi: &platform.WifiSettings{SSID: "HomeNet", Hidden: true},
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got %+v, want %+v", c, want)
	}
	if _, ok := fromSettings(settings{"connection": {"type": dbus.MakeVariant("vpn")}}); ok {
		t.Error("vpn should be skipped")
	}
}

func TestHotspotSettings(t *testing.T) {
	s, err := hotspotSettings(platform.HotspotSpec{SSID: "PWS-Setup", PSK: "setup-pass-1", Interface: "wlan0"})
	if err != nil {
		t.Fatal(err)
	}
	if s["connection"]["id"].Value() != hotspotID || s[nmWifi]["mode"].Value() != "ap" ||
		s["ipv4"]["method"].Value() != "shared" || s["802-11-wireless-security"]["key-mgmt"].Value() != "wpa-psk" {
		t.Errorf("settings = %v", s)
	}
	for _, bad := range []platform.HotspotSpec{{PSK: "longenough"}, {SSID: "a", PSK: "short"}} {
		if _, err := hotspotSettings(bad); err == nil {
			t.Errorf("expected error for %+v", bad)
		}
	}
}

func TestToSettingsSendsNoEmptyArrays(t *testing.T) {
	existing := settings{
		"802-3-ethernet": {"s390-options": variant(map[string]string{}), "auto-negotiate": variant(false)},
		"ipv4":           {"route-data": variant([]map[string]dbus.Variant{}), "method": variant("auto")},
		"proxy":          {},
		"connection":     {"type": variant("802-3-ethernet"), "permissions": variant([]string{})},
	}
	out, err := toSettings(platform.Connection{Name: "wired", Type: platform.ConnEthernet, IPv4: platform.IPv4Settings{Method: platform.IPv4Auto}}, existing)
	if err != nil {
		t.Fatal(err)
	}
	for sec, kv := range out {
		if len(kv) == 0 {
			t.Errorf("empty section %s sent", sec)
		}
		for k, v := range kv {
			if rv := reflect.ValueOf(v.Value()); (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map) && rv.Len() == 0 {
				t.Errorf("%s.%s is an empty array", sec, k)
			}
		}
	}
	if _, ok := out["802-3-ethernet"]["auto-negotiate"]; !ok {
		t.Error("a non-empty unknown key must survive")
	}
}
