package generic

import (
	"context"
	"errors"
	"testing"

	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/linuxfs"
)

func TestGenericReportsOnlyWhatItCanRead(t *testing.T) {
	a := linuxfs.NewAdapter("testdata/x86")
	a.FSUsedPct = func(string) (float64, error) { return 0, errors.New("no statfs in test") }
	a.KernelTime = func() (linuxfs.KernelTime, error) { return linuxfs.KernelTime{Synced: true}, nil }
	p := NewWithAdapter(a)

	b := p.Board()
	if b.Tier != platform.TierUnsupported {
		t.Fatalf("tier = %s", b.Tier)
	}
	if b.Model != "generic Linux host" || b.SoC != "" || b.Serial != "" || b.RAMMB != 15920 {
		t.Fatalf("board = %+v", b)
	}

	if b.MachineID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("machine id = %q", b.MachineID)
	}

	h, err := p.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Throttled.Known {
		t.Error("generic host must not claim a throttle register")
	}
	if h.RootFSUsedPct != -1 || h.TmpfsUsedPct != -1 {
		t.Errorf("unmeasured filesystems must be -1, got %v %v", h.RootFSUsedPct, h.TmpfsUsedPct)
	}
	if !h.CPUTempKnown || h.CPUTempC != 61 || h.UptimeS != 4000 || h.MemFreeMB != 10742 {
		t.Errorf("health = %+v", h)
	}

	devs, err := p.USBDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 0 {
		t.Errorf("root hubs must be skipped, got %+v", devs)
	}

	ts, err := p.TimeSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ts.Source != "kernel" || !ts.Synced {
		t.Errorf("timesync = %+v", ts)
	}
	if err := p.Reboot(context.Background()); !errors.Is(err, platform.ErrUnsupported) {
		t.Errorf("Reboot err = %v", err)
	}
}

func TestMissingSysfsIsUnsupported(t *testing.T) {
	p := New(t.TempDir())
	if _, err := p.USBDevices(context.Background()); !errors.Is(err, platform.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if _, err := p.Health(context.Background()); err == nil {
		t.Fatal("Health without /proc/meminfo must fail rather than report zeros")
	}
}
