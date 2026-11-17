package netcfg

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/platform"
)

// FallbackOptions tunes the setup hotspot.
type FallbackOptions struct {
	// Every is how often Run evaluates the failback. Default 15 s.
	Every time.Duration
	// Grace is how long no known network may be reachable before the hotspot
	// starts. Default 90 s, enough for a normal Wi-Fi association at boot.
	Grace time.Duration
	// Retry is how long the hotspot stays up before it is dropped for a
	// probe of the known networks. Default 5 minutes.
	Retry time.Duration
	// Probe is how long known networks get after the hotspot drops, before
	// it may come back. Default 45 s.
	Probe time.Duration
	// Identity seeds the SSID and passphrase (board serial or machine id).
	// The passphrase is derived from it, so it can be printed on the device
	// label and needs no storage.
	Identity string
}

func (f FallbackOptions) withDefaults() FallbackOptions {
	if f.Every <= 0 {
		f.Every = 15 * time.Second
	}
	if f.Grace <= 0 {
		f.Grace = 90 * time.Second
	}
	if f.Retry <= 0 {
		f.Retry = 5 * time.Minute
	}
	if f.Probe <= 0 {
		f.Probe = 45 * time.Second
	}
	return f
}

type fallbackState struct {
	noNetSince   time.Time
	hotspotSince time.Time
	probeUntil   time.Time
	hotspotSSID  string
	failedUntil  time.Time
}

// HotspotCredentials derives the setup hotspot SSID and passphrase from the
// device identity: PWS-XXXX with the first four hex characters of its hash,
// and ten lowercase base32 characters as the WPA2 passphrase.
func HotspotCredentials(identity string) platform.HotspotSpec {
	sum := sha256.Sum256([]byte("pws-gateway setup hotspot:" + identity))
	ssid := "PWS-" + strings.ToUpper(hexN(sum[:2]))
	psk := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[2:10]))[:10]
	return platform.HotspotSpec{SSID: ssid, PSK: psk}
}

func hexN(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, v := range b {
		out = append(out, digits[v>>4], digits[v&15])
	}
	return string(out)
}

// knownReachable reports whether any device is connected through a profile
// that is not the setup hotspot.
func knownReachable(devs []platform.Device) bool {
	for _, d := range devs {
		if d.State == "connected" && d.Active != "" && d.Active != HotspotName {
			return true
		}
	}
	return false
}

// FallbackTick runs one evaluation of the AP failback: bring the hotspot up
// when no known network has been reachable for Grace, take it down as soon as
// one is, and drop it every Retry for a Probe window so a network that came
// back is found. It never acts while a network change awaits confirmation.
func (m *Manager) FallbackTick(ctx context.Context) {
	devs, err := m.o.NM.Devices(ctx)
	if err != nil {
		m.log.Warn("netcfg: failback cannot read devices", "err", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.commit != nil {
		return
	}
	f, fo, now := &m.fb, m.o.Fallback, m.now()
	known := knownReachable(devs)

	if f.hotspotSSID != "" { // the hotspot is up
		switch {
		case known:
			m.stopHotspot(ctx)
		case now.Sub(f.hotspotSince) >= fo.Retry:
			m.log.Info("netcfg: dropping the setup hotspot to probe known networks")
			m.stopHotspot(ctx)
			f.probeUntil = now.Add(fo.Probe)
			f.noNetSince = now.Add(-fo.Grace) // only the probe window holds it back
		}
		return
	}
	if known {
		f.noNetSince = time.Time{}
		return
	}
	if f.noNetSince.IsZero() {
		f.noNetSince = now
	}
	if now.Sub(f.noNetSince) < fo.Grace || now.Before(f.probeUntil) || now.Before(f.failedUntil) {
		return
	}
	spec := HotspotCredentials(fo.Identity)
	if err := m.o.NM.StartHotspot(ctx, spec); err != nil {
		m.log.Warn("netcfg: cannot start the setup hotspot", "err", err)
		f.failedUntil = now.Add(fo.Retry)
		return
	}
	m.log.Warn("netcfg: no known network is reachable, setup hotspot is up", "ssid", spec.SSID)
	f.hotspotSSID, f.hotspotSince = spec.SSID, now
}

func (m *Manager) stopHotspot(ctx context.Context) {
	if err := m.o.NM.StopHotspot(ctx); err != nil {
		m.log.Warn("netcfg: stop hotspot", "err", err)
	}
	m.fb.hotspotSSID, m.fb.hotspotSince, m.fb.noNetSince = "", time.Time{}, time.Time{}
}
