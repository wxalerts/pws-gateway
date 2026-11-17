package platform

import (
	"context"
	"io"
)

// Slot names one of the two A/B system slots. A slot is a pair of partitions:
// a boot partition (firmware config, kernel, cmdline) and a root partition.
type Slot string

// The two slots.
const (
	SlotA Slot = "a"
	SlotB Slot = "b"
)

// Other returns the opposite slot.
func (s Slot) Other() Slot {
	if s == SlotA {
		return SlotB
	}
	return SlotA
}

// SlotPart selects the partition of a slot to write.
type SlotPart string

// Slot partitions.
const (
	PartBoot SlotPart = "boot"
	PartRoot SlotPart = "root"
)

// BootState is what the firmware and the running system say about this boot.
type BootState struct {
	// Running is the slot this system booted from.
	Running Slot `json:"running"`
	// Default is the slot a normal (non tryboot) boot selects.
	Default Slot `json:"default"`
	// Tryboot is true when this boot was a one-shot trial of a slot that is
	// not the default. Until Commit is called the next ordinary reboot
	// returns to Default, which is the rollback.
	Tryboot bool `json:"tryboot"`
}

// BootSlots is the A/B switch of the platform. On a Raspberry Pi it is
// implemented with the firmware tryboot flag (autoboot.txt, tryboot_a_b=1),
// which every model supports. It is a separate interface from Platform so
// boards that cannot do A/B simply do not implement it, and so Platform keeps
// its shape.
//
// The update flow is: WriteSlot both partitions of the inactive slot,
// RebootInto that slot once, and after the health check passes call Commit.
// If the health check fails, or the system hangs and the watchdog resets it,
// the firmware boots the default slot again with no further action.
type BootSlots interface {
	// BootState reports the running and default slots and whether this boot
	// was a trial.
	BootState(ctx context.Context) (BootState, error)
	// WriteSlot streams a raw partition image into one partition of a slot.
	// It must refuse the running slot and an image larger than the
	// partition, and must flush to the device before returning.
	WriteSlot(ctx context.Context, slot Slot, part SlotPart, image io.Reader, size int64) error
	// RebootInto arms a one-shot trial of slot and restarts the host. It
	// returns only if the restart could not be started.
	RebootInto(ctx context.Context, slot Slot) error
	// RebootDefault restarts the host into the default slot, abandoning a
	// trial that is under way. It returns only if the restart failed.
	RebootDefault(ctx context.Context) error
	// Commit makes the running slot the default. It is only valid during a
	// trial boot.
	Commit(ctx context.Context) error
}
