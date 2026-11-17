package i2c

import (
	"fmt"
	"sync"
)

// Fake is an in-memory bus for tests. Devices maps an address to its
// register file; reading past a register file returns zeros.
type Fake struct {
	mu      sync.Mutex
	Devices map[int][]byte
	// Writes records every WriteReg as {addr, reg, data...}.
	Writes [][]byte
	// Fail makes every call return this error.
	Fail error
}

// NewFake returns an empty bus.
func NewFake() *Fake { return &Fake{Devices: map[int][]byte{}} }

// Put installs a device whose registers start as regs (256 bytes).
func (f *Fake) Put(addr int, regs map[byte]byte) {
	mem := make([]byte, 256)
	for r, v := range regs {
		mem[r] = v
	}
	f.Devices[addr] = mem
}

// Probe implements Bus.
func (f *Fake) Probe(addr int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.Devices[addr]
	return ok && f.Fail == nil
}

// ReadReg implements Bus.
func (f *Fake) ReadReg(addr int, reg byte, n int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return nil, f.Fail
	}
	mem, ok := f.Devices[addr]
	if !ok {
		return nil, fmt.Errorf("fake i2c: no device at 0x%02X", addr)
	}
	out := make([]byte, n)
	for i := range out {
		if int(reg)+i < len(mem) {
			out[i] = mem[int(reg)+i]
		}
	}
	return out, nil
}

// WriteReg implements Bus.
func (f *Fake) WriteReg(addr int, reg byte, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return f.Fail
	}
	if _, ok := f.Devices[addr]; !ok {
		return fmt.Errorf("fake i2c: no device at 0x%02X", addr)
	}
	f.Writes = append(f.Writes, append([]byte{byte(addr), reg}, data...))
	return nil
}

// Close implements Bus.
func (f *Fake) Close() error { return nil }

// BMP280Regs returns the register file of a BMP280 with the datasheet's
// worked example calibration and the raw counts that example uses, which
// compensate to 100653.27 Pa at 25.08 C.
func BMP280Regs(chip byte) map[byte]byte {
	r := map[byte]byte{0xD0: chip}
	cal := []byte{
		0x70, 0x6B, 0x43, 0x67, 0x18, 0xFC, // T1 T2 T3
		0x7D, 0x8E, 0x43, 0xD6, 0xD0, 0x0B, 0x27, 0x0B, 0x8C, 0x00, 0xF9, 0xFF, 0x8C, 0x3C, 0xF8, 0xC6, 0x70, 0x17, // P1..P9
	}
	for i, v := range cal {
		r[byte(0x88+i)] = v
	}
	// adc_P 415148 = 0x65A2C, adc_T 519888 = 0x7EED0.
	for i, v := range []byte{0x65, 0x5A, 0xC0, 0x7E, 0xED, 0x00} {
		r[byte(0xF7+i)] = v
	}
	return r
}
