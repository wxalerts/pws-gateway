package linuxfs

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/chrony"
)

// Adapter implements the parts of platform.Platform that every Linux host
// shares. The raspberrypi and generic adapters embed it and add board
// detection on top. All inputs are injectable so tests never touch the live
// kernel.
type Adapter struct {
	// FS is the proc/sys tree to read.
	FS Host
	// RootFSPath is the mount whose usage is reported as the root
	// filesystem. Defaults to "/".
	RootFSPath string
	// TmpfsPath is the RAM backed mount used for scratch data. Defaults to
	// "/run", which systemd always mounts as tmpfs.
	TmpfsPath string
	// FSUsedPct measures a mount. Defaults to the statfs implementation.
	FSUsedPct func(path string) (float64, error)
	// KernelTime queries the kernel clock discipline state. Defaults to
	// adjtimex.
	KernelTime func() (KernelTime, error)
	// HW holds the modem, GPS, I2C and chrony backends.
	HW *Hardware
	// PrivDir is where privileged requests are dropped for the root
	// helper. Default $STATE_DIRECTORY/priv.
	PrivDir string
}

// NewAdapter returns an Adapter reading the tree under root ("" for the live
// system) with production defaults for everything else.
func NewAdapter(root string) *Adapter {
	a := &Adapter{
		FS:         Host{Root: root},
		RootFSPath: "/",
		TmpfsPath:  "/run",
		FSUsedPct:  FSUsedPct,
		KernelTime: KernelTimeStatus,
	}
	a.HW = newHardware(a)
	return a
}

// Health reads the board independent metrics. The caller fills in Throttled.
func (a *Adapter) Health(ctx context.Context) (platform.Health, error) {
	if err := ctx.Err(); err != nil {
		return platform.Health{}, err
	}
	h := platform.Health{RootFSUsedPct: -1, TmpfsUsedPct: -1}

	if t, err := a.FS.CPUTempC(); err == nil {
		h.CPUTempC = t
		h.CPUTempKnown = true
	}
	mi, err := a.FS.MemInfo()
	if err != nil {
		return h, err
	}
	h.MemFreeMB = mi.AvailableMB
	if h.UptimeS, err = a.FS.Uptime(); err != nil {
		return h, err
	}
	if a.FSUsedPct != nil {
		if pct, err := a.FSUsedPct(a.RootFSPath); err == nil {
			h.RootFSUsedPct = pct
		}
		if pct, err := a.FSUsedPct(a.TmpfsPath); err == nil {
			h.TmpfsUsedPct = pct
		}
	}
	return h, nil
}

// USBDevices lists the bus from sysfs.
func (a *Adapter) USBDevices(ctx context.Context) ([]platform.USBDevice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := a.FS.USBDevices()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, platform.ErrUnsupported
		}
		return nil, err
	}
	out := make([]platform.USBDevice, 0, len(raw))
	for _, d := range raw {
		out = append(out, platform.USBDevice{
			Bus:       d.Bus,
			Dev:       d.Dev,
			Port:      d.Port,
			VendorID:  d.Vendor,
			ProductID: d.Product,
			Vendor:    d.Manufacturer,
			Product:   d.ProductName,
			Serial:    d.Serial,
		})
	}
	return out, nil
}

// USBResetCount is unsupported: the Linux kernel keeps no per-device reset
// counter in sysfs (resets only show up as kernel log lines). The rtl_433
// supervisor counts re-enumerations of its own dongle instead.
func (a *Adapter) USBResetCount(context.Context) (int, error) {
	return 0, platform.ErrUnsupported
}

// TimeSync prefers the explicit daemon markers and falls back to the kernel
// clock status, which both chrony and systemd-timesyncd maintain.
func (a *Adapter) TimeSync(ctx context.Context) (platform.TimeSync, error) {
	if err := ctx.Err(); err != nil {
		return platform.TimeSync{}, err
	}
	var kt KernelTime
	var ktErr error
	if a.KernelTime != nil {
		kt, ktErr = a.KernelTime()
	} else {
		ktErr = platform.ErrUnsupported
	}

	ts := platform.TimeSync{Source: "kernel"}
	if ktErr == nil {
		ts.Synced = kt.Synced
		ts.Offset = kt.Offset
		ts.OffsetKnown = true
	}

	switch {
	case a.FS.TimesyncdSynchronized():
		ts.Source = "systemd-timesyncd"
		ts.Synced = true
	case a.FS.ChronyRunning():
		ts.Source = "chrony"
	}
	if a.FS.ChronyRunning() && a.HW != nil && a.HW.Run != nil {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if cs, err := chrony.Query(qctx, a.HW.Run); err == nil {
			ts.Ref, ts.LastSync = cs.Ref, cs.LastSync
		}
		cancel()
	}
	if ktErr != nil && ts.Source == "kernel" {
		return ts, ktErr
	}
	return ts, nil
}

// Reboot is not wired yet. It stays ErrUnsupported until the signed command
// path exists; see AGENTS.md rule 4.
func (a *Adapter) Reboot(context.Context) error {
	return platform.ErrUnsupported
}

// DefaultRouteInterface reports the interface of the IPv4 default route, read
// from /proc/net/route.
func (a *Adapter) DefaultRouteInterface(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := a.FS.DefaultRouteInterface()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", platform.ErrUnsupported
		}
		return "", err
	}
	return name, nil
}
