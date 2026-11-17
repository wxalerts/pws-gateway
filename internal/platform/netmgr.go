package platform

import (
	"context"
	"errors"
)

// ErrNotFound is returned by NetManager when a named connection or device
// does not exist.
var ErrNotFound = errors.New("platform: not found")

// ConnType is the kind of a network connection profile.
type ConnType string

// Connection types.
const (
	ConnEthernet ConnType = "ethernet"
	ConnWifi     ConnType = "wifi"
	ConnCellular ConnType = "cellular"
)

// IPv4Method says how an interface gets its address.
type IPv4Method string

// IPv4 methods.
const (
	IPv4Auto   IPv4Method = "auto"
	IPv4Manual IPv4Method = "manual"
	// IPv4Shared is the hotspot mode: the host hands out addresses.
	IPv4Shared IPv4Method = "shared"
)

// IPv4Settings is the IPv4 part of a connection.
type IPv4Settings struct {
	Method IPv4Method `json:"method"`
	// Address is CIDR, for example "192.168.1.20/24", for the manual method.
	Address   string   `json:"address,omitempty"`
	Gateway   string   `json:"gateway,omitempty"`
	DNS       []string `json:"dns,omitempty"`
	DNSSearch []string `json:"dns_search,omitempty"`
}

// WifiSettings is the Wi-Fi part of a connection.
type WifiSettings struct {
	SSID   string `json:"ssid"`
	Hidden bool   `json:"hidden,omitempty"`
	// PSK is write only: reads never return it.
	PSK string `json:"psk,omitempty"`
}

// Connection is a saved network profile, the unit commit-confirm snapshots
// and restores. Name is the stable handle (NetworkManager connection id).
type Connection struct {
	Name string   `json:"name"`
	Type ConnType `json:"type"`
	// Interface restricts the profile to one interface. Empty means any.
	Interface   string        `json:"interface,omitempty"`
	Autoconnect bool          `json:"autoconnect"`
	IPv4        IPv4Settings  `json:"ipv4"`
	Wifi        *WifiSettings `json:"wifi,omitempty"`
	// Priority orders autoconnect between profiles, higher first.
	Priority int `json:"priority,omitempty"`
	// Hotspot marks the setup access point profile. The failback logic owns
	// it; it is never part of a user change.
	Hotspot bool `json:"hotspot,omitempty"`
}

// Device is one network interface as the manager sees it.
type Device struct {
	Name string   `json:"name"`
	Type ConnType `json:"type"`
	// State is "connected", "connecting", "disconnected" or "unavailable".
	State string `json:"state"`
	// Active is the name of the connection in use, empty when none.
	Active string `json:"active,omitempty"`
	// Addresses are the CIDR addresses currently configured.
	Addresses []string `json:"addresses,omitempty"`
	MAC       string   `json:"mac,omitempty"`
}

// Connectivity is the manager's view of upstream reachability.
type Connectivity string

// Connectivity values.
const (
	ConnectivityNone    Connectivity = "none"
	ConnectivityLimited Connectivity = "limited"
	ConnectivityFull    Connectivity = "full"
	ConnectivityUnknown Connectivity = "unknown"
)

// AccessPoint is one Wi-Fi network seen in a scan.
type AccessPoint struct {
	SSID   string `json:"ssid"`
	Signal int    `json:"signal"`
	// Secured is true when the network needs a passphrase.
	Secured bool `json:"secured"`
}

// HotspotSpec describes the setup access point.
type HotspotSpec struct {
	SSID string `json:"ssid"`
	PSK  string `json:"psk"`
	// Interface is the Wi-Fi interface to use, empty for the first one.
	Interface string `json:"interface,omitempty"`
}

// NetManager is the host network manager (NetworkManager over D-Bus on a Pi).
// It is its own interface so Platform does not grow. Implementations must be
// safe for concurrent use.
type NetManager interface {
	// Devices lists the interfaces.
	Devices(ctx context.Context) ([]Device, error)
	// Connections lists the saved profiles. PSKs are never returned.
	Connections(ctx context.Context) ([]Connection, error)
	// Save creates the profile or updates the one with the same Name, and
	// does not activate it. An empty PSK on update keeps the stored one.
	Save(ctx context.Context, c Connection) error
	// Delete removes the profile named name. ErrNotFound when absent.
	Delete(ctx context.Context, name string) error
	// Activate brings the profile up and returns once the attempt has
	// finished (success or not).
	Activate(ctx context.Context, name string) error
	// Connectivity runs a connectivity check.
	Connectivity(ctx context.Context) (Connectivity, error)
	// ScanWifi returns the visible networks.
	ScanWifi(ctx context.Context) ([]AccessPoint, error)
	// StartHotspot brings up the setup access point and StopHotspot takes
	// it down. StartHotspot is idempotent.
	StartHotspot(ctx context.Context, spec HotspotSpec) error
	StopHotspot(ctx context.Context) error
	// Hostname and SetHostname read and set the static host name.
	Hostname(ctx context.Context) (string, error)
	SetHostname(ctx context.Context, name string) error
}
