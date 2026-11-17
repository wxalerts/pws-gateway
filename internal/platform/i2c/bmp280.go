package i2c

import (
	"errors"
	"fmt"
	"time"
)

// BMP280 register map, datasheet BST-BMP280-DS001 section 4.2.
const (
	bmpRegCalib   = 0x88
	bmpCalibLen   = 24
	bmpRegCtrl    = 0xF4
	bmpRegData    = 0xF7
	bmpRegStatus  = 0xF3
	bmpRegReset   = 0xE0
	bmpResetMagic = 0xB6
	// ctrl_meas: temperature x1, pressure x4, forced mode. Forced mode takes
	// one measurement and returns to sleep, which keeps the die cool and the
	// reading free of self heating.
	bmpCtrlForced = 0x2D
	bmpMeasuring  = 0x08
)

// Calibration holds the factory trimming values.
type Calibration struct {
	T1         uint16
	T2, T3     int16
	P1         uint16
	P2, P3, P4 int16
	P5, P6, P7 int16
	P8, P9     int16
}

// ParseCalibration decodes the 24 bytes at 0x88.
func ParseCalibration(b []byte) (Calibration, error) {
	if len(b) < bmpCalibLen {
		return Calibration{}, errors.New("i2c: short BMP280 calibration block")
	}
	u := func(i int) uint16 { return uint16(b[i]) | uint16(b[i+1])<<8 }
	s := func(i int) int16 { return int16(u(i)) } //nolint:gosec // two's complement reinterpretation is the register format
	return Calibration{
		T1: u(0), T2: s(2), T3: s(4),
		P1: u(6), P2: s(8), P3: s(10), P4: s(12), P5: s(14), P6: s(16), P7: s(18), P8: s(20), P9: s(22),
	}, nil
}

// Compensate converts raw ADC counts to degrees Celsius and pascals using the
// datasheet's 64 bit integer algorithm (section 3.11.3). Temperature is only
// needed internally; the gateway never reports it because the Pi's own heat
// biases it.
func (c Calibration) Compensate(adcT, adcP int32) (tempC float64, pressurePa float64, err error) {
	v1 := ((int32(adcT) >> 3) - (int32(c.T1) << 1)) * int32(c.T2) >> 11
	v2 := (((int32(adcT)>>4 - int32(c.T1)) * (int32(adcT)>>4 - int32(c.T1))) >> 12) * int32(c.T3) >> 14
	tFine := v1 + v2
	tempC = float64((tFine*5+128)>>8) / 100

	var1 := int64(tFine) - 128000
	var2 := var1 * var1 * int64(c.P6)
	var2 += (var1 * int64(c.P5)) << 17
	var2 += int64(c.P4) << 35
	var1 = (var1*var1*int64(c.P3))>>8 + (var1*int64(c.P2))<<12
	var1 = ((int64(1) << 47) + var1) * int64(c.P1) >> 33
	if var1 == 0 {
		return tempC, 0, errors.New("i2c: BMP280 calibration is invalid")
	}
	p := int64(1048576) - int64(adcP)
	p = ((p<<31 - var2) * 3125) / var1
	var1 = int64(c.P9) * (p >> 13) * (p >> 13) >> 25
	var2 = int64(c.P8) * p >> 19
	p = (p+var1+var2)>>8 + int64(c.P7)<<4
	return tempC, float64(p) / 256, nil
}

// MeasureTimeout bounds the wait for a forced measurement; the datasheet
// gives under 30 ms for these oversampling settings.
const MeasureTimeout = 250 * time.Millisecond

// Sleep is replaced by tests.
var Sleep = time.Sleep

// ReadBMP280 triggers one forced measurement and returns the station
// pressure in hPa. It also accepts a BME280, whose pressure path is the same.
func ReadBMP280(bus Bus, addr int) (float64, error) {
	id, err := bus.ReadReg(addr, regChipID, 1)
	if err != nil {
		return 0, err
	}
	switch id[0] {
	case ChipBMP280, chipBMP280E, chipBMP280F, ChipBME280:
	default:
		return 0, fmt.Errorf("i2c: chip ID 0x%02X at 0x%02X is not a BMP280", id[0], addr)
	}
	raw, err := bus.ReadReg(addr, bmpRegCalib, bmpCalibLen)
	if err != nil {
		return 0, err
	}
	cal, err := ParseCalibration(raw)
	if err != nil {
		return 0, err
	}
	if err := bus.WriteReg(addr, bmpRegCtrl, []byte{bmpCtrlForced}); err != nil {
		return 0, err
	}
	deadline := time.Now().Add(MeasureTimeout)
	for {
		Sleep(10 * time.Millisecond)
		st, err := bus.ReadReg(addr, bmpRegStatus, 1)
		if err != nil {
			return 0, err
		}
		if st[0]&bmpMeasuring == 0 {
			break
		}
		if time.Now().After(deadline) {
			return 0, errors.New("i2c: BMP280 measurement timed out")
		}
	}
	d, err := bus.ReadReg(addr, bmpRegData, 6)
	if err != nil {
		return 0, err
	}
	adcP := int32(d[0])<<12 | int32(d[1])<<4 | int32(d[2])>>4
	adcT := int32(d[3])<<12 | int32(d[4])<<4 | int32(d[5])>>4
	if adcP == 0x80000 { // reset value: the measurement was skipped
		return 0, errors.New("i2c: BMP280 returned no pressure sample")
	}
	_, pa, err := cal.Compensate(adcT, adcP)
	if err != nil {
		return 0, err
	}
	return pa / 100, nil
}
