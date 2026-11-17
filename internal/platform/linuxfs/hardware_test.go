package linuxfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/i2c"
	"github.com/wxalerts/pws-gateway/internal/platform/modemmanager"
	"github.com/wxalerts/pws-gateway/internal/platform/priv"
)

func fakeAdapter(t *testing.T, bus *i2c.Fake, bootCfg string) *Adapter {
	t.Helper()
	root := t.TempDir()
	if bootCfg != "" {
		p := filepath.Join(root, "boot", "firmware", "config.txt")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(bootCfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := NewAdapter(root)
	a.HW.OpenI2C = func(int) (i2c.Bus, error) {
		if bus == nil {
			return nil, i2c.ErrNoBus
		}
		return bus, nil
	}
	return a
}

func TestI2CScanReportsDS3231AndBMP280(t *testing.T) {
	bus := i2c.NewFake()
	bus.Put(0x68, map[byte]byte{0x00: 0x30, 0x01: 0x45, 0x02: 0x13, 0x04: 0x17, 0x05: 0x11, 0x06: 0x26})
	bus.Put(0x76, i2c.BMP280Regs(i2c.ChipBMP280))
	a := fakeAdapter(t, bus, "dtparam=i2c_arm=on\n")
	st, err := a.I2C(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || !st.BusPresent || len(st.Devices) != 2 {
		t.Fatalf("status = %+v", st)
	}
	if st.Devices[0].Kind != platform.I2CKindDS3231 || st.Devices[0].AddrHex != "0x68" ||
		st.Devices[1].Kind != platform.I2CKindBMP280 || *st.Devices[1].ChipID != 0x58 {
		t.Errorf("devices = %+v", st.Devices)
	}
}

func TestI2CBusMissingIsNotAnError(t *testing.T) {
	a := fakeAdapter(t, nil, "#dtparam=i2c_arm=on\n")
	st, err := a.I2C(context.Background())
	if err != nil || st.Enabled || st.BusPresent || len(st.Devices) != 0 {
		t.Fatalf("st=%+v err=%v", st, err)
	}
	if rtc, err := a.RTC(context.Background()); rtc != nil || err != nil {
		t.Fatalf("rtc=%v err=%v", rtc, err)
	}
}

func TestRTCReportsOscillatorStop(t *testing.T) {
	bus := i2c.NewFake()
	bus.Put(0x68, map[byte]byte{0x00: 0x30, 0x01: 0x45, 0x02: 0x13, 0x04: 0x17, 0x05: 0x11, 0x06: 0x26, 0x0F: 0x80})
	a := fakeAdapter(t, bus, "")
	rtc, err := a.RTC(context.Background())
	if err != nil || rtc == nil || !rtc.OscillatorStopped || rtc.Time.Year() != 2026 {
		t.Fatalf("rtc=%+v err=%v", rtc, err)
	}
}

func TestSetI2CGoesThroughPrivHook(t *testing.T) {
	a := fakeAdapter(t, nil, "")
	var got []priv.Action
	a.HW.Priv = func(_ context.Context, act priv.Action) error { got = append(got, act); return nil }
	if err := a.SetI2CEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	_ = a.SetI2CEnabled(context.Background(), false)
	_ = a.DisableFakeHwclock(context.Background())
	if len(got) != 3 || got[0] != priv.ActI2COn || got[1] != priv.ActI2COff || got[2] != priv.ActFakeHwclockDisable {
		t.Fatalf("actions = %v", got)
	}
}

func TestFakeHwclockState(t *testing.T) {
	a := fakeAdapter(t, nil, "")
	for out, want := range map[string]bool{"disabled\n": true, "enabled\n": false, "masked\n": true} {
		a.HW.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte(out), nil }
		if got, err := a.FakeHwclockDisabled(context.Background()); err != nil || got != want {
			t.Errorf("%q: got %v, %v", out, got, err)
		}
	}
	a.HW.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no systemctl") }
	if _, err := a.FakeHwclockDisabled(context.Background()); !errors.Is(err, platform.ErrUnsupported) {
		t.Errorf("err = %v", err)
	}
}

func TestModemAndGPSMapping(t *testing.T) {
	a := fakeAdapter(t, nil, "")
	rssi := -83.0
	a.HW.ModemReader = func(context.Context) (*modemmanager.Info, error) {
		return &modemmanager.Info{Carrier: "AT&T", MCCMNC: "310410", Reg: "home", Tech: "lte", RSSIdBm: &rssi}, nil
	}
	m, err := a.Modem(context.Background())
	if err != nil || m == nil || m.Carrier != "AT&T" || *m.RSSIdBm != -83 {
		t.Fatalf("modem = %+v err=%v", m, err)
	}
	a.HW.ModemReader = func(context.Context) (*modemmanager.Info, error) { return nil, nil }
	if m, err := a.Modem(context.Background()); m != nil || err != nil {
		t.Fatalf("no modem: %v %v", m, err)
	}
}

func TestTimeSyncCarriesChronyRef(t *testing.T) {
	a := fakeAdapter(t, nil, "")
	if err := os.MkdirAll(filepath.Join(a.FS.Root, "run", "chrony"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(a.FS.Root, "run", "chrony", "chronyd.pid"), []byte("1"), 0o644)
	a.KernelTime = func() (KernelTime, error) { return KernelTime{Synced: true}, nil }
	a.HW.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[1] == "sources" {
			return []byte("#,*,GPS,0,4,377,3,0.0001,0.0001,0.0001\n"), nil
		}
		return []byte("47505300,GPS,1,1763409408.5,0.0,0.0,0.0\n"), nil
	}
	ts, err := a.TimeSync(context.Background())
	if err != nil || ts.Ref != "gps" || ts.LastSync.Unix() != 1763409408 || !ts.Synced {
		t.Fatalf("ts=%+v err=%v", ts, err)
	}
	_ = time.Now
}

func TestChronyQueriedEvenWhenPidFileIsHidden(t *testing.T) {
	// The service account cannot see /run/chrony, so the pid file is no proof
	// chrony is absent. The chronyc binary being installed is enough to ask.
	a := fakeAdapter(t, nil, "")
	if err := os.MkdirAll(filepath.Join(a.FS.Root, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(a.FS.Root, "usr", "bin", "chronyc"), nil, 0o755)
	a.KernelTime = func() (KernelTime, error) { return KernelTime{Synced: true}, nil }
	a.HW.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[1] == "sources" {
			return []byte("#,*,LTE,10,6,377,2,0.1,0.1,0.5\n"), nil
		}
		return nil, errors.New("no tracking")
	}
	ts, err := a.TimeSync(context.Background())
	if err != nil || ts.Ref != "lte" {
		t.Fatalf("ts=%+v err=%v", ts, err)
	}
}
