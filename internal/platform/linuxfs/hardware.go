package linuxfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/chrony"
	"github.com/wxalerts/pws-gateway/internal/platform/gpsd"
	"github.com/wxalerts/pws-gateway/internal/platform/i2c"
	"github.com/wxalerts/pws-gateway/internal/platform/modemmanager"
	"github.com/wxalerts/pws-gateway/internal/platform/priv"
)

// I2CBusNumber is the Pi header bus.
const I2CBusNumber = 1

// privWait is how long a privileged request may take.
const privWait = 20 * time.Second

// Hardware holds the injectable backends of the modem, GPS, I2C and time
// facilities. NewAdapter fills production defaults; tests replace the fields.
type Hardware struct {
	// ModemReader returns the modem, (nil, nil) for none and
	// platform.ErrUnsupported when ModemManager is absent.
	ModemReader func(ctx context.Context) (*modemmanager.Info, error)
	// GPS queries gpsd.
	GPS *gpsd.Client
	// OpenI2C opens the bus.
	OpenI2C i2c.Opener
	// Run runs chronyc and systemctl.
	Run chrony.Runner
	// Priv performs a privileged action, normally through the root helper.
	Priv func(ctx context.Context, a priv.Action) error

	i2cMu sync.Mutex

	mmMu  sync.Mutex
	mmBus *modemmanager.SystemBus
}

func newHardware(a *Adapter) *Hardware {
	h := &Hardware{
		GPS:     &gpsd.Client{},
		OpenI2C: i2c.OpenDev,
		Run:     chrony.ExecRunner,
	}
	h.ModemReader = h.systemModem
	h.Priv = func(ctx context.Context, act priv.Action) error { return a.privDo(ctx, h, act) }
	return h
}

// systemModem lazily connects to the system bus and reads the first modem.
func (h *Hardware) systemModem(ctx context.Context) (*modemmanager.Info, error) {
	h.mmMu.Lock()
	if h.mmBus == nil {
		b, err := modemmanager.Connect()
		if err != nil {
			h.mmMu.Unlock()
			return nil, platform.ErrUnsupported
		}
		h.mmBus = b
	}
	bus := h.mmBus
	h.mmMu.Unlock()
	info, err := (&modemmanager.Reader{Bus: bus}).Read(ctx)
	if errors.Is(err, modemmanager.ErrNoManager) {
		h.mmMu.Lock()
		_ = h.mmBus.Close()
		h.mmBus = nil
		h.mmMu.Unlock()
		return nil, platform.ErrUnsupported
	}
	return info, err
}

// Modem implements platform.Platform.
func (a *Adapter) Modem(ctx context.Context) (*platform.ModemInfo, error) {
	info, err := a.HW.ModemReader(ctx)
	if err != nil || info == nil {
		return nil, err
	}
	return &platform.ModemInfo{
		Model: info.Model, State: info.State, Carrier: info.Carrier, MCCMNC: info.MCCMNC,
		Reg: info.Reg, Tech: info.Tech, RSSIdBm: info.RSSIdBm, SignalPct: info.SignalPct,
		NetworkTime: info.NetworkTime,
	}, nil
}

// GPS implements platform.Platform.
func (a *Adapter) GPS(ctx context.Context) (*platform.GPSInfo, error) {
	info, err := a.HW.GPS.Query(ctx)
	if errors.Is(err, gpsd.ErrNoDaemon) {
		return nil, platform.ErrUnsupported
	}
	if err != nil || info == nil {
		return nil, err
	}
	return &platform.GPSInfo{Fix: info.Fix, Sats: info.Sats, Lat: info.Lat, Lon: info.Lon, AltM: info.AltM}, nil
}

// openBus opens the header bus. ok is false when the bus does not exist,
// which is the normal state before I2C is enabled.
func (a *Adapter) openBus() (i2c.Bus, bool, error) {
	b, err := a.HW.OpenI2C(I2CBusNumber)
	if errors.Is(err, i2c.ErrNoBus) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// I2C implements platform.Platform.
func (a *Adapter) I2C(ctx context.Context) (platform.I2CStatus, error) {
	if err := ctx.Err(); err != nil {
		return platform.I2CStatus{}, err
	}
	st := platform.I2CStatus{Devices: []platform.I2CDevice{}}
	if cfg, ok := a.FS.BootConfig(); ok {
		st.Enabled = priv.I2CEnabled(cfg)
	}
	a.HW.i2cMu.Lock()
	defer a.HW.i2cMu.Unlock()
	bus, ok, err := a.openBus()
	if err != nil {
		return st, err
	}
	if !ok {
		return st, nil
	}
	defer bus.Close()
	st.BusPresent = true
	for _, d := range i2c.Scan(bus) {
		st.Devices = append(st.Devices, platform.I2CDevice{
			Addr: d.Addr, AddrHex: fmt.Sprintf("0x%02x", d.Addr), Kind: d.Kind, Label: d.Label,
			ChipID: d.ChipID, KernelDriver: a.FS.I2CDriverBound(I2CBusNumber, d.Addr),
		})
	}
	return st, nil
}

// RTC implements platform.Platform.
func (a *Adapter) RTC(ctx context.Context) (*platform.RTCInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.HW.i2cMu.Lock()
	defer a.HW.i2cMu.Unlock()
	bus, ok, err := a.openBus()
	if err != nil || !ok {
		return nil, err
	}
	defer bus.Close()
	if !bus.Probe(i2c.AddrDS3231) {
		return nil, nil
	}
	d, err := i2c.ReadDS3231(bus)
	if err != nil {
		return nil, nil //nolint:nilerr // a device at 0x68 that is not a DS3231 means no RTC
	}
	t := d.TempC
	return &platform.RTCInfo{Time: d.Time, OscillatorStopped: d.OscillatorStopped, TempC: &t}, nil
}

// BMP280 implements platform.Platform.
func (a *Adapter) BMP280(ctx context.Context, addr int) (platform.PressureReading, error) {
	if err := ctx.Err(); err != nil {
		return platform.PressureReading{}, err
	}
	a.HW.i2cMu.Lock()
	defer a.HW.i2cMu.Unlock()
	bus, ok, err := a.openBus()
	if err != nil {
		return platform.PressureReading{}, err
	}
	if !ok {
		return platform.PressureReading{}, platform.ErrUnsupported
	}
	defer bus.Close()
	hpa, err := i2c.ReadBMP280(bus, addr)
	return platform.PressureReading{StationHPa: hpa}, err
}

// SetI2CEnabled implements platform.Platform.
func (a *Adapter) SetI2CEnabled(ctx context.Context, enabled bool) error {
	act := priv.ActI2COff
	if enabled {
		act = priv.ActI2COn
	}
	return a.HW.Priv(ctx, act)
}

// FakeHwclockDisabled implements platform.Platform.
func (a *Adapter) FakeHwclockDisabled(ctx context.Context) (bool, error) {
	out, err := a.HW.Run(ctx, "systemctl", "is-enabled", "fake-hwclock.service")
	state := strings.TrimSpace(string(out))
	switch state {
	case "disabled", "masked", "masked-runtime", "not-found":
		return true, nil
	case "":
		if err != nil {
			return false, platform.ErrUnsupported
		}
	}
	return false, nil
}

// DisableFakeHwclock implements platform.Platform.
func (a *Adapter) DisableFakeHwclock(ctx context.Context) error {
	return a.HW.Priv(ctx, priv.ActFakeHwclockDisable)
}

// privDo runs a privileged action. Root (a development run or the bench)
// applies it directly; the service account asks the root helper unit.
func (a *Adapter) privDo(ctx context.Context, h *Hardware, act priv.Action) error {
	dir := a.PrivDir
	if dir == "" {
		base := os.Getenv("STATE_DIRECTORY")
		if base == "" {
			base = "/var/lib/pwsgw"
		}
		dir = base + "/priv"
	}
	if os.Geteuid() == 0 {
		return applyDirect(ctx, h, act)
	}
	return priv.Submit(ctx, dir, act, privWait)
}

func applyDirect(ctx context.Context, h *Hardware, act priv.Action) error {
	ex := priv.DefaultExecutor(priv.Runner(h.Run))
	switch act {
	case priv.ActI2COn:
		return priv.Executor.SetI2C(ex, true)
	case priv.ActI2COff:
		return priv.Executor.SetI2C(ex, false)
	default:
		return priv.Executor.DisableFakeHwclock(ex, ctx)
	}
}
