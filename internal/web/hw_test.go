package web

import (
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/bootchange"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
)

func chip(v int) *int { return &v }

func TestModemPageData(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login()
	var out map[string]any
	e.do("GET", "/api/modem", "", c).json(&out)
	if out["modem"] != nil || out["gps"] != nil {
		t.Fatalf("no hardware: %v", out)
	}
	rssi := -83.0
	e.plat.Set(func(p *mock.Platform) {
		p.ModemValue = &platform.ModemInfo{Carrier: "AT&T", MCCMNC: "310410", Reg: "home", Tech: "lte", RSSIdBm: &rssi, State: "registered"}
		p.GPSValue = &platform.GPSInfo{Fix: 3, Sats: 9}
		p.RouteIface = "wwan0"
	})
	e.do("GET", "/api/modem", "", c).json(&out)
	m, _ := out["modem"].(map[string]any)
	g, _ := out["gps"].(map[string]any)
	if m["carrier"] != "AT&T" || m["rssi_dbm"].(float64) != -83 || g["fix"].(float64) != 3 || out["link"] != "lte" {
		t.Fatalf("out = %v", out)
	}
}

func TestI2CScanPage(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login()
	e.plat.Set(func(p *mock.Platform) {
		p.I2CValue = platform.I2CStatus{Enabled: true, BusPresent: true, Devices: []platform.I2CDevice{
			{Addr: 0x68, AddrHex: "0x68", Kind: platform.I2CKindDS3231, Label: "DS3231 real-time clock", KernelDriver: true},
			{Addr: 0x76, AddrHex: "0x76", Kind: platform.I2CKindBMP280, Label: "BMP280 pressure sensor", ChipID: chip(0x58)},
		}}
	})
	var out struct {
		Enabled bool `json:"enabled"`
		Reboot  bool `json:"reboot_required"`
		Devices []struct {
			Kind string `json:"kind"`
		} `json:"devices"`
	}
	e.do("GET", "/api/i2c", "", c).json(&out)
	if !out.Enabled || out.Reboot || len(out.Devices) != 2 || out.Devices[0].Kind != "ds3231" || out.Devices[1].Kind != "bmp280" {
		t.Fatalf("out = %+v", out)
	}
}

func TestI2CEnableUsesCommitConfirm(t *testing.T) {
	e := newEnv(t, nil)
	m, err := bootchange.New(e.plat, t.TempDir(), false, time.Minute, func() time.Time { return *e.clock })
	if err != nil {
		t.Fatal(err)
	}
	e.srv.opts.Boot = m
	c := e.login()

	r := e.do("POST", "/api/i2c/enable", `{"enabled":true}`, c)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"confirm_deadline"`) {
		t.Fatalf("enable: %d %s", r.Code, r.Body)
	}
	if len(e.plat.I2CSetCalls) != 1 || !e.plat.I2CSetCalls[0] {
		t.Fatalf("boot config calls = %v", e.plat.I2CSetCalls)
	}
	var out struct {
		Change struct {
			Phase     string `json:"phase"`
			Candidate *bool  `json:"candidate"`
		} `json:"change"`
		Reboot bool `json:"reboot_required"`
	}
	e.do("GET", "/api/i2c", "", c).json(&out)
	if out.Change.Phase != "applied" || out.Change.Candidate == nil || !*out.Change.Candidate || !out.Reboot {
		t.Fatalf("out = %+v", out)
	}
	if r := e.do("POST", "/api/i2c/enable", `{"enabled":false}`, c); r.Code != 409 {
		t.Fatalf("second change while pending: %d", r.Code)
	}

	// Not confirmed in time: the next request rolls the boot config back.
	*e.clock = e.clock.Add(2 * time.Minute)
	e.do("GET", "/api/i2c", "", c).json(&out)
	if out.Change.Phase != "idle" || len(e.plat.I2CSetCalls) != 2 || e.plat.I2CSetCalls[1] {
		t.Fatalf("not rolled back: %+v calls=%v", out, e.plat.I2CSetCalls)
	}

	// Enable again and confirm.
	e.do("POST", "/api/i2c/enable", `{"enabled":true}`, c)
	if r := e.do("POST", "/api/i2c/confirm", "", c); r.Code != 200 {
		t.Fatalf("confirm: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/api/i2c/confirm", "", c); r.Code != 409 {
		t.Fatalf("second confirm: %d", r.Code)
	}
	if r := e.do("POST", "/api/i2c/rollback", "", c); r.Code != 200 {
		t.Fatalf("rollback: %d", r.Code)
	}
}

func TestTimePageShowsOscillatorStop(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login()
	e.plat.Set(func(p *mock.Platform) {
		p.TimeSyncValue = platform.TimeSync{Synced: true, Ref: "gps", OffsetKnown: true, Offset: 3 * time.Millisecond}
		p.RTCValue = &platform.RTCInfo{Time: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), OscillatorStopped: true}
		p.FakeHwclockOff = true
	})
	var out map[string]any
	e.do("GET", "/api/time", "", c).json(&out)
	rtc, _ := out["rtc"].(map[string]any)
	if out["source"] != "gps" || out["synced"] != true || out["offset_ms"].(float64) != 3 ||
		rtc["oscillator_stopped"] != true || out["fake_hwclock_disabled"] != true {
		t.Fatalf("out = %v", out)
	}
	e.plat.Set(func(p *mock.Platform) { p.RTCValue = nil })
	e.do("GET", "/api/time", "", c).json(&out)
	if out["rtc"] != nil || out["fake_hwclock_disabled"] != nil {
		t.Fatalf("no rtc: %v", out)
	}
}
