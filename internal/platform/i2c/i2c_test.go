package i2c

import (
	"math"
	"testing"
	"time"
)

func init() { Sleep = func(time.Duration) {} }

func ds3231Regs(osf bool) map[byte]byte {
	r := map[byte]byte{0x00: 0x30, 0x01: 0x45, 0x02: 0x13, 0x03: 0x02, 0x04: 0x17, 0x05: 0x11, 0x06: 0x26, 0x11: 0x19, 0x12: 0x40}
	if osf {
		r[0x0F] = 0x80
	}
	return r
}

func TestScanIdentifiesDS3231AndDistinguishesBMPFromBME(t *testing.T) {
	for _, tc := range []struct {
		chip byte
		kind string
	}{{ChipBMP280, KindBMP280}, {ChipBME280, KindBME280}, {ChipBME680, KindBME680}} {
		f := NewFake()
		f.Put(0x68, ds3231Regs(false))
		f.Put(0x76, BMP280Regs(tc.chip))
		devs := Scan(f)
		if len(devs) != 2 || devs[0].Addr != 0x68 || devs[0].Kind != KindDS3231 || devs[1].Addr != 0x76 || devs[1].Kind != tc.kind {
			t.Fatalf("chip %#x: scan = %+v", tc.chip, devs)
		}
		if devs[1].ChipID == nil || *devs[1].ChipID != int(tc.chip) {
			t.Errorf("chip id not carried: %+v", devs[1])
		}
	}
}

func TestDeviceAt0x68ThatIsNotADS3231(t *testing.T) {
	f := NewFake()
	f.Put(0x68, map[byte]byte{0x00: 0xFF, 0x0F: 0x70}) // an MPU-6050 style register file
	if d := Identify(f, 0x68); d.Kind != KindUnknown {
		t.Fatalf("got %+v", d)
	}
}

func TestReadDS3231(t *testing.T) {
	f := NewFake()
	f.Put(0x68, ds3231Regs(true))
	d, err := ReadDS3231(f)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 17, 13, 45, 30, 0, time.UTC); !d.Time.Equal(want) {
		t.Errorf("time = %v, want %v", d.Time, want)
	}
	if !d.OscillatorStopped {
		t.Error("OSF should be set")
	}
	if d.TempC != 25.25 {
		t.Errorf("temp = %v", d.TempC)
	}
	f.Put(0x68, ds3231Regs(false))
	if d, _ := ReadDS3231(f); d.OscillatorStopped {
		t.Error("OSF should be clear")
	}
}

func TestCompensateMatchesDatasheetExample(t *testing.T) {
	cal := Calibration{T1: 27504, T2: 26435, T3: -1000, P1: 36477, P2: -10685, P3: 3024, P4: 2855, P5: 140, P6: -7, P7: 15500, P8: -14600, P9: 6000}
	tc, pa, err := cal.Compensate(519888, 415148)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(tc-25.08) > 0.01 || math.Abs(pa-100653.27) > 0.1 {
		t.Fatalf("t=%v p=%v", tc, pa)
	}
}

func TestReadBMP280OverFakeBus(t *testing.T) {
	f := NewFake()
	f.Put(0x76, BMP280Regs(ChipBMP280))
	hpa, err := ReadBMP280(f, 0x76)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(hpa-1006.5327) > 0.01 {
		t.Fatalf("hpa = %v", hpa)
	}
	// Only the measurement trigger is written; temperature is never exposed.
	if len(f.Writes) != 1 || f.Writes[0][1] != bmpRegCtrl {
		t.Fatalf("writes = %v", f.Writes)
	}
	f.Put(0x77, BMP280Regs(ChipBME680))
	if _, err := ReadBMP280(f, 0x77); err == nil {
		t.Fatal("a BME680 must be refused")
	}
}
