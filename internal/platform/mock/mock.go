// Package mock is a scriptable platform.Platform for tests in other packages.
// Every method returns the matching field; set Err fields to make a call fail.
// The zero value is a healthy, time synced Pi 4 with no USB devices.
package mock

import (
	"context"
	"sync"

	"github.com/wxalerts/pws-gateway/internal/platform"
)

// Platform is the mock. Fields may be changed between calls; access is
// serialized by an internal mutex so tests can mutate it from any goroutine.
type Platform struct {
	mu sync.Mutex

	BoardInfo     platform.BoardInfo
	HealthValue   platform.Health
	HealthErr     error
	USB           []platform.USBDevice
	USBErr        error
	ResetCount    int
	ResetErr      error
	TimeSyncValue platform.TimeSync
	TimeSyncErr   error
	RebootErr     error
	// RouteIface and RouteErr are the default route interface answer.
	RouteIface string
	RouteErr   error
	// ModemValue, GPSValue and RTCValue are returned as is; nil means the
	// hardware is absent. The Err fields make the call fail.
	ModemValue *platform.ModemInfo
	ModemErr   error
	GPSValue   *platform.GPSInfo
	GPSErr     error
	I2CValue   platform.I2CStatus
	I2CErr     error
	RTCValue   *platform.RTCInfo
	RTCErr     error
	// BMP280Values maps an I2C address to its pressure.
	BMP280Values map[int]platform.PressureReading
	BMP280Err    error
	// I2CSetCalls records every SetI2CEnabled argument in order.
	I2CSetCalls []bool
	I2CSetErr   error
	// FakeHwclockOff is the answer to FakeHwclockDisabled and is set by
	// DisableFakeHwclock. FakeHwclockCalls counts those calls.
	FakeHwclockOff   bool
	FakeHwclockCalls int
	FakeHwclockErr   error
	// RebootCalls counts Reboot invocations.
	RebootCalls int
}

var _ platform.Platform = (*Platform)(nil)

// New returns a mock pre-filled with plausible Pi 4 values.
func New() *Platform {
	return &Platform{
		BoardInfo: platform.BoardInfo{
			Model: "Raspberry Pi 4 Model B Rev 1.4",
			SoC:   "bcm2711",
			Arch:  "arm64",
			// A fixed fake serial so claim tests derive a stable hardware id.
			Serial: "10000000abcdef01",
			RAMMB:  3792,
			Tier:   platform.TierPi4,
		},
		HealthValue: platform.Health{
			CPUTempC:      48.2,
			CPUTempKnown:  true,
			Throttled:     platform.DecodeThrottled(0),
			MemFreeMB:     2900,
			UptimeS:       86400,
			RootFSUsedPct: 31.5,
			TmpfsUsedPct:  2.0,
		},
		TimeSyncValue: platform.TimeSync{Synced: true, Source: "systemd-timesyncd", OffsetKnown: true},
		ResetErr:      platform.ErrUnsupported,
		RebootErr:     platform.ErrUnsupported,
		RouteIface:    "eth0",
	}
}

// Board implements platform.Platform.
func (m *Platform) Board() platform.BoardInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.BoardInfo
}

// Health implements platform.Platform.
func (m *Platform) Health(context.Context) (platform.Health, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.HealthValue, m.HealthErr
}

// USBDevices implements platform.Platform.
func (m *Platform) USBDevices(context.Context) ([]platform.USBDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]platform.USBDevice(nil), m.USB...), m.USBErr
}

// USBResetCount implements platform.Platform.
func (m *Platform) USBResetCount(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ResetCount, m.ResetErr
}

// TimeSync implements platform.Platform.
func (m *Platform) TimeSync(context.Context) (platform.TimeSync, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.TimeSyncValue, m.TimeSyncErr
}

// DefaultRouteInterface implements platform.Platform.
func (m *Platform) DefaultRouteInterface(context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.RouteIface, m.RouteErr
}

// Reboot implements platform.Platform.
func (m *Platform) Reboot(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RebootCalls++
	return m.RebootErr
}

// Modem implements platform.Platform.
func (m *Platform) Modem(context.Context) (*platform.ModemInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ModemValue, m.ModemErr
}

// GPS implements platform.Platform.
func (m *Platform) GPS(context.Context) (*platform.GPSInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.GPSValue, m.GPSErr
}

// I2C implements platform.Platform.
func (m *Platform) I2C(context.Context) (platform.I2CStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.I2CValue, m.I2CErr
}

// RTC implements platform.Platform.
func (m *Platform) RTC(context.Context) (*platform.RTCInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.RTCValue, m.RTCErr
}

// BMP280 implements platform.Platform.
func (m *Platform) BMP280(_ context.Context, addr int) (platform.PressureReading, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.BMP280Err != nil {
		return platform.PressureReading{}, m.BMP280Err
	}
	v, ok := m.BMP280Values[addr]
	if !ok {
		return platform.PressureReading{}, platform.ErrUnsupported
	}
	return v, nil
}

// SetI2CEnabled implements platform.Platform. The call is recorded and, on
// success, the scripted I2C status follows so a later read sees the change.
func (m *Platform) SetI2CEnabled(_ context.Context, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.I2CSetCalls = append(m.I2CSetCalls, enabled)
	if m.I2CSetErr != nil {
		return m.I2CSetErr
	}
	m.I2CValue.Enabled = enabled
	return nil
}

// FakeHwclockDisabled implements platform.Platform.
func (m *Platform) FakeHwclockDisabled(context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.FakeHwclockOff, m.FakeHwclockErr
}

// DisableFakeHwclock implements platform.Platform.
func (m *Platform) DisableFakeHwclock(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FakeHwclockCalls++
	if m.FakeHwclockErr != nil {
		return m.FakeHwclockErr
	}
	m.FakeHwclockOff = true
	return nil
}

// Set applies fn under the lock, for tests that change several fields.
func (m *Platform) Set(fn func(*Platform)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(m)
}
