// Package fake holds scriptable fakes of the optional platform interfaces
// (BootSlots, NetManager) for tests in other packages.
package fake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/wxalerts/pws-gateway/internal/platform"
)

// Slots is a BootSlots that behaves like the Raspberry Pi tryboot flow
// without hardware: RebootInto arms a one-shot trial, Boot simulates the
// restart the firmware performs.
type Slots struct {
	mu sync.Mutex

	running, def platform.Slot
	tryboot      bool
	armed        platform.Slot // set by RebootInto, consumed by Boot
	rebootNow    bool

	// Written holds what WriteSlot received, keyed "a/boot" and so on.
	Written map[string][]byte
	// WriteErr, RebootErr and CommitErr make the matching call fail.
	WriteErr, RebootErr, CommitErr error
	// Reboots counts restarts requested by either reboot method.
	Reboots int
	// Calls is the ordered call log, for sequence assertions.
	Calls []string
}

var _ platform.BootSlots = (*Slots)(nil)

// NewSlots returns slots running and defaulting to a.
func NewSlots() *Slots {
	return &Slots{running: platform.SlotA, def: platform.SlotA, Written: map[string][]byte{}}
}

func (s *Slots) log(f string, a ...any) { s.Calls = append(s.Calls, fmt.Sprintf(f, a...)) }

// BootState implements platform.BootSlots.
func (s *Slots) BootState(context.Context) (platform.BootState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return platform.BootState{Running: s.running, Default: s.def, Tryboot: s.tryboot}, nil
}

// WriteSlot implements platform.BootSlots.
func (s *Slots) WriteSlot(_ context.Context, slot platform.Slot, part platform.SlotPart, image io.Reader, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.WriteErr != nil {
		return s.WriteErr
	}
	if slot == s.running {
		return errors.New("fake: refusing to write the running slot")
	}
	var buf bytes.Buffer
	n, err := io.Copy(&buf, image)
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("fake: image is %d bytes, expected %d", n, size)
	}
	s.Written[string(slot)+"/"+string(part)] = buf.Bytes()
	s.log("write %s/%s", slot, part)
	return nil
}

// RebootInto implements platform.BootSlots.
func (s *Slots) RebootInto(_ context.Context, slot platform.Slot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.RebootErr != nil {
		return s.RebootErr
	}
	s.armed, s.rebootNow = slot, true
	s.Reboots++
	s.log("reboot-into %s", slot)
	return nil
}

// RebootDefault implements platform.BootSlots.
func (s *Slots) RebootDefault(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.RebootErr != nil {
		return s.RebootErr
	}
	s.armed, s.rebootNow = "", true
	s.Reboots++
	s.log("reboot-default")
	return nil
}

// Commit implements platform.BootSlots.
func (s *Slots) Commit(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.CommitErr != nil {
		return s.CommitErr
	}
	if !s.tryboot {
		return errors.New("fake: commit outside a trial boot")
	}
	s.def, s.tryboot = s.running, false
	s.log("commit %s", s.running)
	return nil
}

// RebootRequested reports whether a restart is waiting for Boot.
func (s *Slots) RebootRequested() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rebootNow
}

// Boot performs the restart: a trial of the armed slot if RebootInto was
// called, else the default slot. The one-shot flag is consumed either way.
func (s *Slots) Boot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rebootNow = false
	if s.armed != "" && s.armed != s.def {
		s.running, s.tryboot = s.armed, true
	} else {
		s.running, s.tryboot = s.def, false
	}
	s.armed = ""
}

// DropTrial forgets an armed trial, as if the new slot never booted and the
// firmware fell back on its own.
func (s *Slots) DropTrial() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = ""
}
