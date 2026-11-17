//go:build linux

package i2c

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// i2cSlaveForce selects the target address even when a kernel driver owns it
// (the DS3231 bound by the i2c-rtc overlay shows as UU). Only reads go to
// such an address, so forcing is safe.
const i2cSlaveForce = 0x0706

type devBus struct {
	mu sync.Mutex
	f  *os.File
}

// OpenDev opens /dev/i2c-N.
func OpenDev(n int) (Bus, error) {
	f, err := os.OpenFile(fmt.Sprintf("/dev/i2c-%d", n), os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoBus
		}
		return nil, err
	}
	return &devBus{f: f}, nil
}

func (b *devBus) setAddr(addr int) error {
	return unix.IoctlSetInt(int(b.f.Fd()), i2cSlaveForce, addr)
}

func (b *devBus) Probe(addr int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.setAddr(addr) != nil {
		return false
	}
	var one [1]byte
	_, err := b.f.Read(one[:])
	return err == nil
}

func (b *devBus) ReadReg(addr int, reg byte, n int) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.setAddr(addr); err != nil {
		return nil, err
	}
	if _, err := b.f.Write([]byte{reg}); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	if _, err := b.f.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (b *devBus) WriteReg(addr int, reg byte, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.setAddr(addr); err != nil {
		return err
	}
	_, err := b.f.Write(append([]byte{reg}, data...))
	return err
}

func (b *devBus) Close() error { return b.f.Close() }
