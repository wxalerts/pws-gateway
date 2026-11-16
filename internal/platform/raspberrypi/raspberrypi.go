// Package raspberrypi is the platform adapter for Raspberry Pi boards.
//
// Detection reads the device tree model string. The adapter adds the firmware
// throttle register (under-voltage is the most common cause of SDR dropouts)
// on top of the shared Linux readers. All paths are relative to Root so tests
// run against the fixtures in testdata.
package raspberrypi

import (
	"context"
	"errors"
	"regexp"
	"runtime"
	"strings"

	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/linuxfs"
)

// ErrNotRaspberryPi is returned by New when the device tree does not name a
// Raspberry Pi.
var ErrNotRaspberryPi = errors.New("raspberrypi: device tree model is not a Raspberry Pi")

// Platform is the Raspberry Pi adapter.
type Platform struct {
	*linuxfs.Adapter
	board platform.BoardInfo
}

var _ platform.Platform = (*Platform)(nil)

func init() {
	platform.Register("raspberrypi", 100, func() (platform.Platform, bool) {
		p, err := New("")
		if err != nil {
			return nil, false
		}
		return p, true
	})
}

// New builds the adapter for the tree under root ("" for the live host).
func New(root string) (*Platform, error) {
	return NewWithAdapter(linuxfs.NewAdapter(root))
}

// NewWithAdapter builds the adapter around a pre-configured linuxfs.Adapter,
// which lets tests inject fake statfs and adjtimex readers.
func NewWithAdapter(a *linuxfs.Adapter) (*Platform, error) {
	model, ok := a.FS.DeviceTreeModel()
	if !ok || !strings.HasPrefix(model, "Raspberry Pi") {
		return nil, ErrNotRaspberryPi
	}
	p := &Platform{Adapter: a}
	p.board = platform.BoardInfo{
		Model:  model,
		SoC:    soc(a.FS.DeviceTreeCompatible()),
		Arch:   runtime.GOARCH,
		Serial: a.FS.DeviceTreeSerial(),
		Tier:   Classify(model),
	}
	if mi, err := a.FS.MemInfo(); err == nil {
		p.board.RAMMB = mi.TotalMB
	}
	return p, nil
}

// Board returns the facts gathered by New.
func (p *Platform) Board() platform.BoardInfo { return p.board }

// Health adds the firmware throttle flags to the common Linux metrics.
func (p *Platform) Health(ctx context.Context) (platform.Health, error) {
	h, err := p.Adapter.Health(ctx)
	if err != nil {
		return h, err
	}
	if raw, ok := p.FS.ThrottledRaw(); ok {
		if th, perr := platform.ParseThrottled(raw); perr == nil {
			h.Throttled = th
		}
	}
	return h, nil
}

var (
	rePi3BPlus = regexp.MustCompile(`^Raspberry Pi 3 Model B Plus`)
	rePi4      = regexp.MustCompile(`^Raspberry Pi (4 Model B|400|Compute Module 4)`)
	rePi5      = regexp.MustCompile(`^Raspberry Pi (5|500|Compute Module 5)`)
)

// Classify maps a device tree model string to a support tier. Only the boards
// in docs/hardware.md are supported: the 3B+ (the floor), the Pi 4 family and
// the Pi 5 family. Older or smaller boards (Pi 3B, 3A+, Zero 2 W) run the
// adapter fine but report "unsupported" so the UI can warn.
func Classify(model string) platform.Tier {
	switch {
	case rePi5.MatchString(model):
		return platform.TierPi5
	case rePi4.MatchString(model):
		return platform.TierPi4
	case rePi3BPlus.MatchString(model):
		return platform.TierPi3
	default:
		return platform.TierUnsupported
	}
}

// soc picks the Broadcom chip out of the compatible list ("brcm,bcm2711").
func soc(compatible []string) string {
	for _, c := range compatible {
		if after, ok := strings.CutPrefix(c, "brcm,"); ok {
			return after
		}
	}
	return ""
}
