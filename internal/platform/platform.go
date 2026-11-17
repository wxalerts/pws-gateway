// Package platform is the board adapter boundary for pwsgw.
//
// Everything that is specific to the hardware the daemon runs on (Raspberry
// Pi firmware flags, SoC temperature, USB enumeration, time sync state, and
// one day reboot) is reached through the Platform interface. Implementations
// live in the subpackages raspberrypi, generic and mock.
//
// Rule for the rest of the code base: nothing outside internal/platform and
// its subpackages may read /sys, /proc, device trees or call vcgencmd. If you
// need a new fact about the host, add it to the Platform interface and
// implement it here, so that tests elsewhere stay hardware free and a new
// board only ever means a new subpackage.
package platform

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrUnsupported is returned by a method the current platform cannot provide.
// Callers must treat it as "unknown", never as zero.
var ErrUnsupported = errors.New("platform: unsupported on this host")

// Tier classifies the board against the supported hardware list. The tier is
// what the rest of the daemon keys on (for example to size caches or pick
// defaults), never the model string.
type Tier string

// Known tiers. Only Pi 3B+, Pi 4 family and Pi 5 family are supported in v1.
const (
	TierPi3         Tier = "pi3"
	TierPi4         Tier = "pi4"
	TierPi5         Tier = "pi5"
	TierUnsupported Tier = "unsupported"
)

// BoardInfo describes the host as detected at startup. Fields the platform
// cannot determine are left empty or zero.
type BoardInfo struct {
	// Model is the human readable board name, for example
	// "Raspberry Pi 4 Model B Rev 1.4".
	Model string `json:"model"`
	// SoC is the system on chip, for example "bcm2711".
	SoC string `json:"soc"`
	// Arch is the Go architecture the daemon was built for (arm64, amd64).
	Arch string `json:"arch"`
	// Serial is the board serial number when the firmware exposes one.
	Serial string `json:"serial,omitempty"`
	// MachineID is the OS machine id (/etc/machine-id), the stable identity
	// of a host that has no board serial. It is never serialized: only the
	// claim flow reads it, and only as input to a hash.
	MachineID string `json:"-"`
	// RAMMB is the total memory in mebibytes.
	RAMMB int `json:"ram_mb"`
	// Tier is the supported hardware tier.
	Tier Tier `json:"tier"`
}

// Throttled is the decoded Raspberry Pi firmware throttle state.
//
// The firmware exposes a 32-bit mask through
// /sys/devices/platform/soc/soc:firmware/get_throttled (the same value that
// `vcgencmd get_throttled` prints). The low nibble reports conditions that are
// active right now; bits 16 to 19 report the same conditions having occurred
// at any time since boot:
//
//	bit 0   under-voltage detected now        (0x1)
//	bit 1   ARM frequency capped now          (0x2)
//	bit 2   currently throttled               (0x4)
//	bit 3   soft temperature limit active now (0x8)
//	bit 16  under-voltage has occurred        (0x10000)
//	bit 17  ARM frequency capping has occurred(0x20000)
//	bit 18  throttling has occurred           (0x40000)
//	bit 19  soft temperature limit occurred   (0x80000)
//
// Under-voltage is the one that matters most for a weather gateway: a weak
// power supply causes SDR dongles to drop off the bus.
type Throttled struct {
	Raw                   uint32 `json:"raw"`
	UnderVoltageNow       bool   `json:"under_voltage_now"`
	UnderVoltageOccurred  bool   `json:"under_voltage_occurred"`
	FreqCappedNow         bool   `json:"freq_capped_now"`
	FreqCappedOccurred    bool   `json:"freq_capped_occurred"`
	ThrottledNow          bool   `json:"throttled_now"`
	ThrottledOccurred     bool   `json:"throttled_occurred"`
	SoftTempLimitNow      bool   `json:"soft_temp_limit_now"`
	SoftTempLimitOccurred bool   `json:"soft_temp_limit_occurred"`
	// Known is false when the host has no firmware throttle register, in
	// which case every flag above is false and must not be trusted.
	Known bool `json:"known"`
}

// Health is a point-in-time snapshot of the host. Values that could not be
// read are reported through the matching *Known flag or a negative sentinel
// rather than a misleading zero.
type Health struct {
	// CPUTempC is the SoC temperature in degrees Celsius. CPUTempKnown is
	// false when no thermal zone could be read.
	CPUTempC     float64 `json:"cpu_temp_c"`
	CPUTempKnown bool    `json:"cpu_temp_known"`
	// Throttled is the firmware throttle state, Known=false when unavailable.
	Throttled Throttled `json:"throttled"`
	// MemFreeMB is MemAvailable from the kernel, in mebibytes.
	MemFreeMB int `json:"mem_free_mb"`
	// UptimeS is seconds since boot.
	UptimeS int64 `json:"uptime_s"`
	// RootFSUsedPct is the used fraction of the root filesystem (0 to 100),
	// or -1 when it could not be measured.
	RootFSUsedPct float64 `json:"rootfs_used_pct"`
	// TmpfsUsedPct is the used fraction of the runtime tmpfs (0 to 100), or
	// -1 when it could not be measured.
	TmpfsUsedPct float64 `json:"tmpfs_used_pct"`
}

// USBDevice is one device on the USB bus (interfaces are not listed).
type USBDevice struct {
	// Bus and Dev are the kernel bus and device numbers (lsusb "Bus 001
	// Device 004").
	Bus int `json:"bus"`
	Dev int `json:"dev"`
	// Port is the sysfs topology name, for example "1-1.2". It is stable
	// across replugs into the same socket, unlike Dev.
	Port      string `json:"port"`
	VendorID  uint16 `json:"vendor_id"`
	ProductID uint16 `json:"product_id"`
	Vendor    string `json:"vendor,omitempty"`
	Product   string `json:"product,omitempty"`
	Serial    string `json:"serial,omitempty"`
}

// TimeSync reports whether the system clock is disciplined by NTP. The daemon
// refuses to stamp observations until Synced is true.
type TimeSync struct {
	Synced bool `json:"synced"`
	// Source names what provided the answer: "systemd-timesyncd", "chrony"
	// or "kernel" (the adjtimex status, which both daemons update).
	Source string `json:"source"`
	// Offset is the estimated clock error. OffsetKnown is false when only a
	// boolean sync state was available.
	Offset      time.Duration `json:"offset"`
	OffsetKnown bool          `json:"offset_known"`
	// Ref is the class of the source chrony has selected: "ntp", "gps",
	// "lte" or "rtc". Empty when unknown (chrony not running or not
	// queryable).
	Ref string `json:"ref,omitempty"`
	// LastSync is when chrony last updated the clock, zero when unknown.
	LastSync time.Time `json:"last_sync"`
}

// Platform is the board adapter. Implementations must be safe for concurrent
// use; the web server and the collector call them from different goroutines.
type Platform interface {
	// Board returns static facts gathered at construction time.
	Board() BoardInfo
	// Health reads the live host metrics.
	Health(ctx context.Context) (Health, error)
	// USBDevices lists the devices on the USB bus.
	USBDevices(ctx context.Context) ([]USBDevice, error)
	// USBResetCount returns how many times USB devices were reset since
	// boot, or ErrUnsupported when the kernel does not expose a counter.
	USBResetCount(ctx context.Context) (int, error)
	// TimeSync reports the NTP state.
	TimeSync(ctx context.Context) (TimeSync, error)
	// DefaultRouteInterface returns the name of the network interface that
	// carries the default IPv4 route (for example "eth0", "wlan0" or "wwan0"),
	// or ErrUnsupported when it cannot be told. Only the name is returned, no
	// address, so telemetry can classify the link without leaking network
	// details.
	DefaultRouteInterface(ctx context.Context) (string, error)
	// Modem returns the cellular modem state, or (nil, nil) when no modem is
	// attached. ErrUnsupported means the host has no ModemManager at all.
	Modem(ctx context.Context) (*ModemInfo, error)
	// GPS returns the GNSS receiver state, or (nil, nil) when gpsd has no
	// receiver. ErrUnsupported means gpsd is not running.
	GPS(ctx context.Context) (*GPSInfo, error)
	// I2C reports the bus state and scans it. A bus that is not enabled or
	// not present returns an empty device list, not an error.
	I2C(ctx context.Context) (I2CStatus, error)
	// RTC reads the DS3231, or (nil, nil) when none answers at 0x68.
	RTC(ctx context.Context) (*RTCInfo, error)
	// BMP280 reads the station pressure of the sensor at addr.
	BMP280(ctx context.Context, addr int) (PressureReading, error)
	// SetI2CEnabled edits the boot configuration to enable or disable the
	// I2C bus. It takes effect after a reboot. It is the boot change that
	// the commit-confirm flow wraps.
	SetI2CEnabled(ctx context.Context, enabled bool) error
	// FakeHwclockDisabled reports whether the fake-hwclock service is
	// disabled, ErrUnsupported when it cannot be told.
	FakeHwclockDisabled(ctx context.Context) (bool, error)
	// DisableFakeHwclock disables fake-hwclock, which an RTC makes
	// redundant. It is idempotent.
	DisableFakeHwclock(ctx context.Context) error
	// Reboot restarts the host. It returns ErrUnsupported until the signed
	// remote command path exists; nothing may call it on its own.
	Reboot(ctx context.Context) error
}

// Probe inspects the host and returns an adapter when it recognizes the
// board. Returning (nil, false) means "not mine".
type Probe func() (Platform, bool)

var (
	registryMu sync.Mutex
	registry   []registered
)

type registered struct {
	name     string
	priority int
	probe    Probe
}

// Register adds a probe. Higher priority probes run first. The subpackages
// call this from init; cmd/pwsgw imports them for the side effect so that the
// platform package itself never depends on an implementation.
func Register(name string, priority int, probe Probe) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, registered{name: name, priority: priority, probe: probe})
	sort.SliceStable(registry, func(i, j int) bool { return registry[i].priority > registry[j].priority })
}

// Detect runs the registered probes in priority order and returns the first
// adapter that claims the host. The generic adapter registers with the lowest
// priority and always claims, so Detect only fails when no implementation
// was linked into the binary.
func Detect() (Platform, error) {
	registryMu.Lock()
	probes := append([]registered(nil), registry...)
	registryMu.Unlock()
	for _, r := range probes {
		if p, ok := r.probe(); ok {
			return p, nil
		}
	}
	return nil, errors.New("platform: no adapter registered; import a platform implementation")
}
