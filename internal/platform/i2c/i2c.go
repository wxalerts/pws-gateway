// Package i2c talks to the sensors the gateway supports on the Pi header: a
// DS3231 real-time clock and a BMP280 pressure sensor. The bus itself is the
// Bus interface so tests run against a fake; the Linux implementation uses
// the i2c-dev character device and lives in bus_linux.go.
//
// Only reads and the BMP280 measurement trigger ever go on the wire. The
// DS3231 is never written, so a scan or a status read cannot disturb the
// clock the kernel driver maintains.
package i2c

import (
	"errors"
	"fmt"
)

// ErrNoBus means the bus device does not exist on this host.
var ErrNoBus = errors.New("i2c: bus device not available")

// Bus is one I2C adapter.
type Bus interface {
	// Probe reports whether a device acknowledges at addr.
	Probe(addr int) bool
	// ReadReg reads n bytes starting at register reg of the device at addr.
	ReadReg(addr int, reg byte, n int) ([]byte, error)
	// WriteReg writes data starting at register reg.
	WriteReg(addr int, reg byte, data []byte) error
	Close() error
}

// Opener opens bus number n (1 on the Pi header).
type Opener func(n int) (Bus, error)

// Addresses of the supported parts.
const (
	AddrDS3231    = 0x68
	AddrBMP280    = 0x76
	AddrBMP280Alt = 0x77
)

// Scan range, the same 7 bit range i2cdetect covers.
const (
	scanFirst = 0x03
	scanLast  = 0x77
)

// Kind names, kept equal to the platform package constants.
const (
	KindDS3231  = "ds3231"
	KindBMP280  = "bmp280"
	KindBME280  = "bme280"
	KindBME680  = "bme680"
	KindUnknown = "unknown"
)

// Chip IDs read from register 0xD0.
const (
	regChipID   = 0xD0
	ChipBMP280  = 0x58
	ChipBME280  = 0x60
	ChipBME680  = 0x61
	chipBMP280E = 0x56 // early BMP280 samples
	chipBMP280F = 0x57
)

// Device is one address that answered.
type Device struct {
	Addr   int
	Kind   string
	Label  string
	ChipID *int
}

// Scan probes every address and identifies what it can. Addresses are
// returned in ascending order.
func Scan(bus Bus) []Device {
	var out []Device
	for addr := scanFirst; addr <= scanLast; addr++ {
		if !bus.Probe(addr) {
			continue
		}
		out = append(out, Identify(bus, addr))
	}
	return out
}

// Identify names the device at addr. Cheap boards are often mislabeled, so
// the pressure sensors are told apart by chip ID, not by address, and a
// device at 0x68 must look like a DS3231 before it is called one (an
// MPU-6050 also lives there).
func Identify(bus Bus, addr int) Device {
	d := Device{Addr: addr, Kind: KindUnknown, Label: "Unknown device"}
	switch addr {
	case AddrDS3231:
		if looksLikeDS3231(bus) {
			d.Kind, d.Label = KindDS3231, "DS3231 real-time clock"
		}
	case AddrBMP280, AddrBMP280Alt:
		b, err := bus.ReadReg(addr, regChipID, 1)
		if err != nil {
			return d
		}
		id := int(b[0])
		d.ChipID = &id
		switch b[0] {
		case ChipBMP280, chipBMP280E, chipBMP280F:
			d.Kind, d.Label = KindBMP280, "BMP280 pressure sensor"
		case ChipBME280:
			d.Kind, d.Label = KindBME280, "BME280 pressure, humidity and temperature sensor"
		case ChipBME680:
			d.Kind, d.Label = KindBME680, "BME680 gas sensor"
		default:
			d.Label = fmt.Sprintf("Unknown sensor (chip ID 0x%02X)", b[0])
		}
	}
	return d
}

func bcd(b byte) (int, bool) {
	hi, lo := int(b>>4), int(b&0x0F)
	if hi > 9 || lo > 9 {
		return 0, false
	}
	return hi*10 + lo, true
}
