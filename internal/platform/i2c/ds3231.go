package i2c

import (
	"errors"
	"time"
)

const (
	dsRegTime   = 0x00
	dsRegStatus = 0x0F
	dsLen       = 0x13 // registers 0x00 to 0x12
	dsOSF       = 0x80
)

// DS3231 is a decoded register dump.
type DS3231 struct {
	Time              time.Time
	OscillatorStopped bool
	TempC             float64
}

var errNotDS3231 = errors.New("i2c: device at 0x68 does not look like a DS3231")

func looksLikeDS3231(bus Bus) bool {
	_, err := ReadDS3231(bus)
	return err == nil
}

// ReadDS3231 reads the time, the oscillator stop flag and the temperature.
// It validates the BCD fields and the always-zero status bits, which is how a
// DS3231 is told from other parts that share the address.
func ReadDS3231(bus Bus) (DS3231, error) {
	b, err := bus.ReadReg(AddrDS3231, dsRegTime, dsLen)
	if err != nil {
		return DS3231{}, err
	}
	if len(b) < dsLen {
		return DS3231{}, errNotDS3231
	}
	// Status bits 6 to 4 are read-only zero on the DS3231.
	if b[dsRegStatus]&0x70 != 0 {
		return DS3231{}, errNotDS3231
	}
	sec, ok1 := bcd(b[0] & 0x7F)
	minute, ok2 := bcd(b[1] & 0x7F)
	var hour int
	var ok3 bool
	if b[2]&0x40 != 0 { // 12 hour mode
		hour, ok3 = bcd(b[2] & 0x1F)
		hour %= 12
		if b[2]&0x20 != 0 {
			hour += 12
		}
	} else {
		hour, ok3 = bcd(b[2] & 0x3F)
	}
	date, ok4 := bcd(b[4] & 0x3F)
	mon, ok5 := bcd(b[5] & 0x1F)
	yr, ok6 := bcd(b[6])
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || sec > 59 || minute > 59 || hour > 23 ||
		date < 1 || date > 31 || mon < 1 || mon > 12 {
		return DS3231{}, errNotDS3231
	}
	year := 2000 + yr
	if b[5]&0x80 != 0 {
		year += 100
	}
	t := time.Date(year, time.Month(mon), date, hour, minute, sec, 0, time.UTC)
	temp := float64(int8(b[0x11])) + float64(b[0x12]>>6)*0.25
	return DS3231{Time: t, OscillatorStopped: b[dsRegStatus]&dsOSF != 0, TempC: temp}, nil
}
