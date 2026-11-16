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
			RAMMB: 3792,
			Tier:  platform.TierPi4,
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

// Reboot implements platform.Platform.
func (m *Platform) Reboot(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RebootCalls++
	return m.RebootErr
}

// Set applies fn under the lock, for tests that change several fields.
func (m *Platform) Set(fn func(*Platform)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(m)
}
