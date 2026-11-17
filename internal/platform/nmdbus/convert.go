package nmdbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/godbus/dbus/v5"

	"github.com/wxalerts/pws-gateway/internal/platform"
)

// hotspotID is the connection id of the setup access point. It is the marker
// Connections uses to set Hotspot, so a user profile never collides with it.
const hotspotID = "pwsgw-setup-hotspot"

// settings is the NetworkManager connection dictionary.
type settings = map[string]map[string]dbus.Variant

// NetworkManager connection type strings.
const (
	nmEthernet = "802-3-ethernet"
	nmWifi     = "802-11-wireless"
	nmGSM      = "gsm"
)

// Errors for requests the adapter refuses.
var (
	errReadOnly = errors.New("nmdbus: cellular profiles are read only")
	errHotspot  = errors.New("nmdbus: the setup hotspot profile is not user editable")
)

func connTypeToNM(t platform.ConnType) (string, error) {
	switch t {
	case platform.ConnEthernet:
		return nmEthernet, nil
	case platform.ConnWifi:
		return nmWifi, nil
	case platform.ConnCellular:
		return "", errReadOnly
	}
	return "", fmt.Errorf("nmdbus: unknown connection type %q", t)
}

func nmToConnType(s string) (platform.ConnType, bool) {
	switch s {
	case nmEthernet:
		return platform.ConnEthernet, true
	case nmWifi:
		return platform.ConnWifi, true
	case nmGSM:
		return platform.ConnCellular, true
	}
	return "", false
}

// deviceType maps the NMDeviceType enum. Loopback, bridges, veth and the like
// are not interesting to the UI, so they report false.
func deviceType(t uint32) (platform.ConnType, bool) {
	switch t {
	case 1:
		return platform.ConnEthernet, true
	case 2:
		return platform.ConnWifi, true
	case 8:
		return platform.ConnCellular, true
	}
	return "", false
}

// deviceState maps the NMDeviceState enum to the platform strings.
func deviceState(s uint32) string {
	switch {
	case s == 100:
		return "connected"
	case s >= 40 && s <= 90:
		return "connecting"
	case s == 30, s == 110, s == 120:
		return "disconnected"
	}
	return "unavailable"
}

// connectivityFromNM maps the NMConnectivityState enum. A captive portal
// counts as limited because the uplink cannot reach the broker.
func connectivityFromNM(s uint32) platform.Connectivity {
	switch s {
	case 1:
		return platform.ConnectivityNone
	case 2, 3:
		return platform.ConnectivityLimited
	case 4:
		return platform.ConnectivityFull
	}
	return platform.ConnectivityUnknown
}

// AP security flag bits.
const (
	apFlagPrivacy = 0x1 // NM_802_11_AP_FLAGS_PRIVACY
)

// apSecured says whether a network needs a passphrase: the privacy bit, or
// any WPA or RSN capability flags.
func apSecured(flags, wpa, rsn uint32) bool {
	return flags&apFlagPrivacy != 0 || wpa != 0 || rsn != 0
}

func str(sec map[string]dbus.Variant, key string) string {
	if v, ok := sec[key]; ok {
		if s, ok := v.Value().(string); ok {
			return s
		}
	}
	return ""
}

func boolean(sec map[string]dbus.Variant, key string, def bool) bool {
	if v, ok := sec[key]; ok {
		if b, ok := v.Value().(bool); ok {
			return b
		}
	}
	return def
}

func strList(sec map[string]dbus.Variant, key string) []string {
	if v, ok := sec[key]; ok {
		if l, ok := v.Value().([]string); ok {
			return l
		}
	}
	return nil
}

// legacyDNS decodes the old "dns" array of uint32 addresses. NetworkManager
// stores them in network byte order, which on the little endian hosts this
// runs on means the first octet is the low byte.
func legacyDNS(sec map[string]dbus.Variant) []string {
	v, ok := sec["dns"]
	if !ok {
		return nil
	}
	l, ok := v.Value().([]uint32)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(l))
	for _, u := range l {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], u)
		out = append(out, net.IP(b[:]).String())
	}
	return out
}

// addrFromData reads the first address-data entry as CIDR.
func addrFromData(sec map[string]dbus.Variant, key string) string {
	v, ok := sec[key]
	if !ok {
		return ""
	}
	list, ok := v.Value().([]map[string]dbus.Variant)
	if !ok || len(list) == 0 {
		return ""
	}
	return cidrFromEntry(list[0])
}

func cidrFromEntry(e map[string]dbus.Variant) string {
	a, ok := e["address"].Value().(string)
	if !ok || a == "" {
		return ""
	}
	p, ok := e["prefix"].Value().(uint32)
	if !ok {
		return a
	}
	return fmt.Sprintf("%s/%d", a, p)
}

// fromSettings converts an NM dictionary to a platform.Connection. It reports
// false for profile types the platform does not model (VPN, bridge, loopback).
// The PSK is never read: GetSettings does not return it and this never asks.
func fromSettings(s settings) (platform.Connection, bool) {
	conn := s["connection"]
	t, ok := nmToConnType(str(conn, "type"))
	if !ok {
		return platform.Connection{}, false
	}
	c := platform.Connection{
		Name:        str(conn, "id"),
		Type:        t,
		Interface:   str(conn, "interface-name"),
		Autoconnect: boolean(conn, "autoconnect", true),
	}
	if v, ok := conn["autoconnect-priority"]; ok {
		if p, ok := v.Value().(int32); ok {
			c.Priority = int(p)
		}
	}
	c.Hotspot = c.Name == hotspotID

	ip4 := s["ipv4"]
	c.IPv4.Method = platform.IPv4Method(str(ip4, "method"))
	c.IPv4.Address = addrFromData(ip4, "address-data")
	c.IPv4.Gateway = str(ip4, "gateway")
	c.IPv4.DNS = strList(ip4, "dns-data")
	if len(c.IPv4.DNS) == 0 {
		c.IPv4.DNS = legacyDNS(ip4)
	}
	c.IPv4.DNSSearch = strList(ip4, "dns-search")

	if t == platform.ConnWifi {
		w := s[nmWifi]
		ws := &platform.WifiSettings{Hidden: boolean(w, "hidden", false)}
		if v, ok := w["ssid"]; ok {
			if b, ok := v.Value().([]byte); ok {
				ws.SSID = string(b)
			}
		}
		c.Wifi = ws
	}
	return c, true
}

func variant(v any) dbus.Variant { return dbus.MakeVariant(v) }

// ipv4Section builds the ipv4 dictionary from the platform settings.
func ipv4Section(in platform.IPv4Settings) (map[string]dbus.Variant, error) {
	method := in.Method
	if method == "" {
		method = platform.IPv4Auto
	}
	switch method {
	case platform.IPv4Auto, platform.IPv4Manual, platform.IPv4Shared:
	default:
		return nil, fmt.Errorf("nmdbus: unknown ipv4 method %q", method)
	}
	sec := map[string]dbus.Variant{"method": variant(string(method))}
	if method == platform.IPv4Manual {
		p, err := netip.ParsePrefix(in.Address)
		if err != nil || !p.Addr().Is4() {
			return nil, fmt.Errorf("nmdbus: manual ipv4 needs an IPv4 CIDR address, got %q", in.Address)
		}
		sec["address-data"] = variant([]map[string]dbus.Variant{{
			"address": variant(p.Addr().String()),
			"prefix":  variant(uint32(p.Bits())), //nolint:gosec // 0..32
		}})
		if in.Gateway != "" {
			g, err := netip.ParseAddr(in.Gateway)
			if err != nil || !g.Is4() {
				return nil, fmt.Errorf("nmdbus: bad ipv4 gateway %q", in.Gateway)
			}
			sec["gateway"] = variant(g.String())
		}
	}
	if len(in.DNS) > 0 && method != platform.IPv4Shared {
		for _, d := range in.DNS {
			if a, err := netip.ParseAddr(d); err != nil || !a.Is4() {
				return nil, fmt.Errorf("nmdbus: bad ipv4 dns server %q", d)
			}
		}
		sec["dns-data"] = variant(append([]string(nil), in.DNS...))
	}
	if len(in.DNSSearch) > 0 && method != platform.IPv4Shared {
		sec["dns-search"] = variant(append([]string(nil), in.DNSSearch...))
	}
	return sec, nil
}

// managedIPv4Keys are the ipv4 keys this adapter owns and rewrites on update.
var managedIPv4Keys = []string{
	"method", "address-data", "addresses", "gateway", "dns", "dns-data", "dns-search",
}

// toSettings builds the dictionary to send. With existing nil it is a new
// profile. With existing set it is that profile's settings with the managed
// keys replaced, so keys the adapter does not know about survive the update
// (NetworkManager's Update replaces everything it is not given).
//
// The security block is only written when a PSK is supplied. An update with
// an empty PSK leaves the existing block as NetworkManager returned it, which
// keeps the stored secret.
func toSettings(c platform.Connection, existing settings) (settings, error) {
	if c.Hotspot || c.Name == hotspotID {
		return nil, errHotspot
	}
	if c.Name == "" {
		return nil, errors.New("nmdbus: connection name is required")
	}
	nmType, err := connTypeToNM(c.Type)
	if err != nil {
		return nil, err
	}
	out := settings{}
	for sec, kv := range existing {
		cp := make(map[string]dbus.Variant, len(kv))
		for k, v := range kv {
			cp[k] = v
		}
		out[sec] = cp
	}
	if existing != nil {
		if old := str(existing["connection"], "type"); old != nmType {
			return nil, fmt.Errorf("nmdbus: connection %q is %s, its type cannot change", c.Name, old)
		}
	}

	conn := section(out, "connection")
	conn["id"] = variant(c.Name)
	conn["type"] = variant(nmType)
	conn["autoconnect"] = variant(c.Autoconnect)
	conn["autoconnect-priority"] = variant(int32(c.Priority)) //nolint:gosec // small user value
	if c.Interface != "" {
		conn["interface-name"] = variant(c.Interface)
	} else {
		delete(conn, "interface-name")
	}

	ip4, err := ipv4Section(c.IPv4)
	if err != nil {
		return nil, err
	}
	cur := section(out, "ipv4")
	for _, k := range managedIPv4Keys {
		delete(cur, k)
	}
	for k, v := range ip4 {
		cur[k] = v
	}

	if c.Type == platform.ConnWifi {
		if c.Wifi == nil || c.Wifi.SSID == "" {
			return nil, errors.New("nmdbus: wifi profile needs an SSID")
		}
		w := section(out, nmWifi)
		w["ssid"] = variant([]byte(c.Wifi.SSID))
		w["hidden"] = variant(c.Wifi.Hidden)
		if c.Wifi.PSK != "" {
			if n := len(c.Wifi.PSK); n < 8 || n > 63 {
				return nil, errors.New("nmdbus: wifi passphrase must be 8 to 63 characters")
			}
			sec := section(out, "802-11-wireless-security")
			sec["key-mgmt"] = variant("wpa-psk")
			sec["psk"] = variant(c.Wifi.PSK)
			w["security"] = variant("802-11-wireless-security")
		}
	}
	return out, nil
}

func section(s settings, name string) map[string]dbus.Variant {
	m, ok := s[name]
	if !ok {
		m = map[string]dbus.Variant{}
		s[name] = m
	}
	return m
}

// hotspotSettings builds the profile for the setup access point: WPA2 PSK,
// shared IPv4 so the host serves DHCP, never autoconnected.
func hotspotSettings(spec platform.HotspotSpec) (settings, error) {
	if spec.SSID == "" || len(spec.SSID) > 32 {
		return nil, errors.New("nmdbus: hotspot SSID must be 1 to 32 bytes")
	}
	if n := len(spec.PSK); n < 8 || n > 63 {
		return nil, errors.New("nmdbus: hotspot passphrase must be 8 to 63 characters")
	}
	conn := map[string]dbus.Variant{
		"id":          variant(hotspotID),
		"type":        variant(nmWifi),
		"autoconnect": variant(false),
	}
	if spec.Interface != "" {
		conn["interface-name"] = variant(spec.Interface)
	}
	return settings{
		"connection": conn,
		nmWifi: {
			"ssid":     variant([]byte(spec.SSID)),
			"mode":     variant("ap"),
			"band":     variant("bg"),
			"security": variant("802-11-wireless-security"),
		},
		"802-11-wireless-security": {
			"key-mgmt": variant("wpa-psk"),
			"psk":      variant(spec.PSK),
		},
		"ipv4": {"method": variant("shared")},
		"ipv6": {"method": variant("ignore")},
	}, nil
}

// activeStateDone maps NMActiveConnectionState. done is true once the attempt
// has finished; ok says whether it succeeded.
func activeStateDone(s uint32) (done, ok bool) {
	switch s {
	case 2:
		return true, true
	case 3, 4:
		// deactivating or deactivated: the attempt failed or was torn down.
		return true, false
	}
	return false, false
}
