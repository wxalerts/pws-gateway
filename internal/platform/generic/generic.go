// Package generic is the fallback platform adapter for any Linux host that is
// not a recognised Raspberry Pi: a developer laptop, a CI runner, a different
// single board computer. It reports Tier "unsupported" and only the facts it
// can actually read; there is no firmware throttle register and USB listing
// works wherever sysfs exists.
package generic

import (
	"runtime"

	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/linuxfs"
)

// Platform is the generic Linux adapter.
type Platform struct {
	*linuxfs.Adapter
	board platform.BoardInfo
}

var _ platform.Platform = (*Platform)(nil)

func init() {
	// Lowest priority and always claims, so Detect never returns an error
	// when this package is linked in.
	platform.Register("generic", 0, func() (platform.Platform, bool) {
		return New(""), true
	})
}

// New builds the adapter for the tree under root ("" for the live host).
func New(root string) *Platform {
	return NewWithAdapter(linuxfs.NewAdapter(root))
}

// NewWithAdapter builds the adapter around a pre-configured linuxfs.Adapter.
func NewWithAdapter(a *linuxfs.Adapter) *Platform {
	p := &Platform{Adapter: a}
	p.board = platform.BoardInfo{
		Arch: runtime.GOARCH,
		Tier: platform.TierUnsupported,
	}
	if model, ok := a.FS.DeviceTreeModel(); ok {
		p.board.Model = model
	} else {
		p.board.Model = "generic Linux host"
	}
	p.board.Serial = a.FS.DeviceTreeSerial()
	if mi, err := a.FS.MemInfo(); err == nil {
		p.board.RAMMB = mi.TotalMB
	}
	return p
}

// Board returns the facts gathered by New.
func (p *Platform) Board() platform.BoardInfo { return p.board }
