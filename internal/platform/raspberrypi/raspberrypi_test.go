package raspberrypi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/linuxfs"
)

func fixture(t *testing.T, name string) *linuxfs.Adapter {
	t.Helper()
	a := linuxfs.NewAdapter("testdata/" + name)
	a.FSUsedPct = func(path string) (float64, error) {
		switch path {
		case "/":
			return 42.5, nil
		case "/run":
			return 1.25, nil
		}
		return 0, errors.New("unexpected mount " + path)
	}
	a.KernelTime = func() (linuxfs.KernelTime, error) {
		return linuxfs.KernelTime{Synced: true, Offset: 1500 * time.Microsecond}, nil
	}
	return a
}

func TestDetectModels(t *testing.T) {
	cases := []struct {
		dir    string
		model  string
		soc    string
		tier   platform.Tier
		ramMB  int
		serial string
	}{
		{"pi3bplus", "Raspberry Pi 3 Model B Plus Rev 1.3", "bcm2837", platform.TierPi3, 923, "00000000a1b2c3d4"},
		{"pi4", "Raspberry Pi 4 Model B Rev 1.4", "bcm2711", platform.TierPi4, 3793, "100000005e6f7a8b"},
		{"pi5", "Raspberry Pi 5 Model B Rev 1.0", "bcm2712", platform.TierPi5, 8000, "2c0f0000deadbeef"},
	}
	for _, c := range cases {
		t.Run(c.dir, func(t *testing.T) {
			p, err := NewWithAdapter(fixture(t, c.dir))
			if err != nil {
				t.Fatal(err)
			}
			b := p.Board()
			if b.Model != c.model || b.SoC != c.soc || b.Tier != c.tier || b.RAMMB != c.ramMB || b.Serial != c.serial {
				t.Fatalf("board = %+v", b)
			}
			if b.Arch == "" {
				t.Fatal("arch missing")
			}
		})
	}
}

func TestNewRejectsNonPi(t *testing.T) {
	if _, err := New("../generic/testdata/x86"); !errors.Is(err, ErrNotRaspberryPi) {
		t.Fatalf("err = %v, want ErrNotRaspberryPi", err)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]platform.Tier{
		"Raspberry Pi 3 Model B Plus Rev 1.3":    platform.TierPi3,
		"Raspberry Pi 3 Model B Rev 1.2":         platform.TierUnsupported,
		"Raspberry Pi 3 Model A Plus Rev 1.0":    platform.TierUnsupported,
		"Raspberry Pi 4 Model B Rev 1.5":         platform.TierPi4,
		"Raspberry Pi 400 Rev 1.0":               platform.TierPi4,
		"Raspberry Pi Compute Module 4 Rev 1.1":  platform.TierPi4,
		"Raspberry Pi 5 Model B Rev 1.0":         platform.TierPi5,
		"Raspberry Pi 500 Rev 1.0":               platform.TierPi5,
		"Raspberry Pi Compute Module 5 Rev 1.0":  platform.TierPi5,
		"Raspberry Pi Zero 2 W Rev 1.0":          platform.TierUnsupported,
		"Raspberry Pi 2 Model B Rev 1.1":         platform.TierUnsupported,
		"Raspberry Pi Model B Plus Rev 1.2":      platform.TierUnsupported,
		"Raspberry Pi Compute Module 3 Plus Rev": platform.TierUnsupported,
	}
	for model, want := range cases {
		if got := Classify(model); got != want {
			t.Errorf("Classify(%q) = %s, want %s", model, got, want)
		}
	}
}

func TestHealthDecodesUnderVoltage(t *testing.T) {
	p, err := NewWithAdapter(fixture(t, "pi3bplus"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := p.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !h.CPUTempKnown || h.CPUTempC != 52.616 {
		t.Errorf("cpu temp = %v known=%v", h.CPUTempC, h.CPUTempKnown)
	}
	if h.MemFreeMB != 597 || h.UptimeS != 12345 {
		t.Errorf("mem=%d uptime=%d", h.MemFreeMB, h.UptimeS)
	}
	if h.RootFSUsedPct != 42.5 || h.TmpfsUsedPct != 1.25 {
		t.Errorf("fs usage = %v %v", h.RootFSUsedPct, h.TmpfsUsedPct)
	}
	th := h.Throttled
	if !th.Known || th.Raw != 0x50005 {
		t.Fatalf("throttled = %+v", th)
	}
	if !th.UnderVoltageNow || !th.UnderVoltageOccurred || !th.ThrottledNow || !th.ThrottledOccurred {
		t.Errorf("expected under-voltage and throttled flags: %+v", th)
	}
	if th.FreqCappedNow || th.SoftTempLimitNow || th.SoftTempLimitOccurred {
		t.Errorf("unexpected flags: %+v", th)
	}
}

func TestHealthPi5FirmwarePathGlob(t *testing.T) {
	p, err := NewWithAdapter(fixture(t, "pi5"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := p.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !h.Throttled.Known || !h.Throttled.SoftTempLimitOccurred || h.Throttled.UnderVoltageNow {
		t.Fatalf("throttled = %+v", h.Throttled)
	}
}

func TestUSBDevicesFindsRTLSDR(t *testing.T) {
	p, err := NewWithAdapter(fixture(t, "pi3bplus"))
	if err != nil {
		t.Fatal(err)
	}
	devs, err := p.USBDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Root hub and the interface node are skipped: hub, NIC and dongle remain.
	if len(devs) != 3 {
		t.Fatalf("got %d devices: %+v", len(devs), devs)
	}
	var sdr []platform.USBDevice
	for _, d := range devs {
		if d.IsRTLSDR() {
			sdr = append(sdr, d)
		}
	}
	if len(sdr) != 1 {
		t.Fatalf("expected one RTL-SDR, got %+v", sdr)
	}
	d := sdr[0]
	if d.Port != "1-1.3" || d.Bus != 1 || d.Dev != 4 || d.Serial != "00000001" || d.Product != "RTL2838UHIDIR" || d.Vendor != "Realtek" {
		t.Fatalf("dongle = %+v", d)
	}
	if _, err := p.USBResetCount(context.Background()); !errors.Is(err, platform.ErrUnsupported) {
		t.Fatalf("USBResetCount err = %v, want ErrUnsupported", err)
	}
}

func TestTimeSyncSources(t *testing.T) {
	ctx := context.Background()

	p3, _ := NewWithAdapter(fixture(t, "pi3bplus"))
	ts, err := p3.TimeSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Source != "systemd-timesyncd" || !ts.Synced || !ts.OffsetKnown || ts.Offset != 1500*time.Microsecond {
		t.Errorf("pi3 timesync = %+v", ts)
	}

	a4 := fixture(t, "pi4")
	a4.KernelTime = func() (linuxfs.KernelTime, error) { return linuxfs.KernelTime{Synced: false}, nil }
	p4, _ := NewWithAdapter(a4)
	ts, err = p4.TimeSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Source != "chrony" || ts.Synced {
		t.Errorf("pi4 timesync = %+v", ts)
	}

	a5 := fixture(t, "pi5")
	p5, _ := NewWithAdapter(a5)
	ts, err = p5.TimeSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Source != "kernel" || !ts.Synced {
		t.Errorf("pi5 timesync = %+v", ts)
	}

	a5.KernelTime = func() (linuxfs.KernelTime, error) { return linuxfs.KernelTime{}, errors.New("no adjtimex") }
	if _, err := p5.TimeSync(ctx); err == nil {
		t.Error("expected error when no source is available")
	}
}

func TestRebootUnsupported(t *testing.T) {
	p, _ := NewWithAdapter(fixture(t, "pi4"))
	if err := p.Reboot(context.Background()); !errors.Is(err, platform.ErrUnsupported) {
		t.Fatalf("Reboot err = %v", err)
	}
}
