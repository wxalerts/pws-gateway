// Package rpiboot implements platform.BootSlots on Raspberry Pi firmware with
// the tryboot flag, which every Pi model supports (Raspberry Pi docs,
// "Fail-safe OS updates (tryboot)").
//
// Disk layout the image pipeline produces:
//
//	p1  autoboot   small FAT partition holding only autoboot.txt
//	p2  bootfs-a   FAT: config.txt, kernel, initramfs, cmdline.txt (root=rootfs-a)
//	p3  bootfs-b   FAT: the same for slot B (root=rootfs-b)
//	p5  rootfs-a   ext4
//	p6  rootfs-b   ext4
//	p7  data       ext4, /var/lib/pwsgw, shared by both slots
//
// autoboot.txt names the default boot partition and, in the [tryboot] section,
// the trial one:
//
//	[all]
//	tryboot_a_b=1
//	boot_partition=2
//	[tryboot]
//	boot_partition=3
//
// tryboot_a_b=1 makes the firmware take config.txt, kernel and cmdline from
// the selected partition rather than only config.txt, so each slot boots its
// own root. A reboot with the argument "0 tryboot" boots the [tryboot]
// partition exactly once; any later reboot (power cut, watchdog, an ordinary
// restart) returns to the [all] partition. That is the rollback: nothing has
// to run for it to happen. Commit swaps the two sections so the trial slot
// becomes the default.
package rpiboot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/wxalerts/pws-gateway/internal/platform"
)

// Partition labels written by the image pipeline.
const (
	LabelBootA = "bootfs-a"
	LabelBootB = "bootfs-b"
	LabelRootA = "rootfs-a"
	LabelRootB = "rootfs-b"
)

// Block is an open block device.
type Block interface {
	io.ReadWriteSeeker
	// Size is the device size in bytes.
	Size() (int64, error)
	Sync() error
	Close() error
}

// Host is everything the adapter needs from the machine, so tests run without
// a Pi. OSHost is the real implementation.
type Host interface {
	ReadFile(name string) ([]byte, error)
	// WriteFile replaces name atomically and durably.
	WriteFile(name string, data []byte) error
	// ResolveLabel returns the device node (for example /dev/mmcblk0p2) of
	// the partition with a filesystem label.
	ResolveLabel(label string) (string, error)
	// ResolveRoot turns a root= value from the kernel command line (a device
	// path, PARTUUID=, UUID= or LABEL=) into a device node.
	ResolveRoot(spec string) (string, error)
	OpenBlock(dev string) (Block, error)
	// Run executes a program and waits for it.
	Run(ctx context.Context, name string, args ...string) error
}

// Config locates the firmware files.
type Config struct {
	// AutobootFile is the path of autoboot.txt on the mounted autoboot
	// partition, for example /boot/autoboot/autoboot.txt.
	AutobootFile string
	// Cmdline and TrybootProp default to /proc/cmdline and
	// /proc/device-tree/chosen/bootloader/tryboot.
	Cmdline, TrybootProp string
}

// Slots is the BootSlots implementation.
type Slots struct {
	cfg  Config
	host Host
}

var _ platform.BootSlots = (*Slots)(nil)

// New returns the adapter. host is OSHost{} on a real Pi.
func New(cfg Config, host Host) *Slots {
	if cfg.Cmdline == "" {
		cfg.Cmdline = "/proc/cmdline"
	}
	if cfg.TrybootProp == "" {
		cfg.TrybootProp = "/proc/device-tree/chosen/bootloader/tryboot"
	}
	return &Slots{cfg: cfg, host: host}
}

func bootLabel(s platform.Slot) string {
	if s == platform.SlotA {
		return LabelBootA
	}
	return LabelBootB
}

func rootLabel(s platform.Slot) string {
	if s == platform.SlotA {
		return LabelRootA
	}
	return LabelRootB
}

var rePartNum = regexp.MustCompile(`(\d+)$`)

// partNumber is the partition number of a device node: mmcblk0p2 and sda2
// both give 2.
func partNumber(dev string) (int, error) {
	m := rePartNum.FindString(dev)
	if m == "" {
		return 0, fmt.Errorf("rpiboot: cannot read a partition number from %q", dev)
	}
	return strconv.Atoi(m)
}

func (s *Slots) bootPart(slot platform.Slot) (int, error) {
	dev, err := s.host.ResolveLabel(bootLabel(slot))
	if err != nil {
		return 0, err
	}
	return partNumber(dev)
}

// Autoboot is the parsed autoboot.txt.
type Autoboot struct {
	// Default is boot_partition under [all]. Trial is boot_partition under
	// [tryboot], zero when absent.
	Default, Trial int
}

// ParseAutoboot reads the two boot_partition values.
func ParseAutoboot(data []byte) (Autoboot, error) {
	var a Autoboot
	section := "all"
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = strings.Trim(line, "[]")
		default:
			k, v, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(k) != "boot_partition" {
				continue
			}
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return a, fmt.Errorf("rpiboot: bad boot_partition %q", v)
			}
			switch section {
			case "all":
				a.Default = n
			case "tryboot":
				a.Trial = n
			}
		}
	}
	if a.Default == 0 {
		return a, errors.New("rpiboot: autoboot.txt has no boot_partition under [all]")
	}
	return a, nil
}

// Render produces autoboot.txt for a default and a trial partition.
func (a Autoboot) Render() []byte {
	return []byte(fmt.Sprintf("[all]\ntryboot_a_b=1\nboot_partition=%d\n[tryboot]\nboot_partition=%d\n", a.Default, a.Trial))
}

func (s *Slots) readAutoboot() (Autoboot, error) {
	b, err := s.host.ReadFile(s.cfg.AutobootFile)
	if err != nil {
		return Autoboot{}, err
	}
	return ParseAutoboot(b)
}

func (s *Slots) slotOfPart(n int) (platform.Slot, error) {
	for _, sl := range []platform.Slot{platform.SlotA, platform.SlotB} {
		p, err := s.bootPart(sl)
		if err != nil {
			return "", err
		}
		if p == n {
			return sl, nil
		}
	}
	return "", fmt.Errorf("rpiboot: boot partition %d is neither slot", n)
}

// BootState implements platform.BootSlots. The running slot is the one whose
// root partition holds the mounted root filesystem; the default comes from
// autoboot.txt; the trial flag from the device tree property the firmware
// sets.
func (s *Slots) BootState(context.Context) (platform.BootState, error) {
	var st platform.BootState
	cmd, err := s.host.ReadFile(s.cfg.Cmdline)
	if err != nil {
		return st, err
	}
	spec := ""
	for _, f := range strings.Fields(string(cmd)) {
		if v, ok := strings.CutPrefix(f, "root="); ok {
			spec = v
		}
	}
	if spec == "" {
		return st, errors.New("rpiboot: no root= on the kernel command line")
	}
	rootDev, err := s.host.ResolveRoot(spec)
	if err != nil {
		return st, err
	}
	for _, sl := range []platform.Slot{platform.SlotA, platform.SlotB} {
		dev, err := s.host.ResolveLabel(rootLabel(sl))
		if err != nil {
			return st, err
		}
		if dev == rootDev {
			st.Running = sl
		}
	}
	if st.Running == "" {
		return st, fmt.Errorf("rpiboot: root device %s is not a slot root partition", rootDev)
	}
	ab, err := s.readAutoboot()
	if err != nil {
		return st, err
	}
	if st.Default, err = s.slotOfPart(ab.Default); err != nil {
		return st, err
	}
	if b, err := s.host.ReadFile(s.cfg.TrybootProp); err == nil && len(b) >= 4 {
		st.Tryboot = b[0]|b[1]|b[2]|b[3] != 0
	}
	return st, nil
}

// WriteSlot implements platform.BootSlots. It writes the image, flushes, then
// reads it back and compares hashes, because a worn SD card can lose writes
// without an error.
func (s *Slots) WriteSlot(ctx context.Context, slot platform.Slot, part platform.SlotPart, image io.Reader, size int64) error {
	st, err := s.BootState(ctx)
	if err != nil {
		return err
	}
	if slot == st.Running {
		return fmt.Errorf("rpiboot: refusing to write slot %s, it is running", slot)
	}
	label := rootLabel(slot)
	if part == platform.PartBoot {
		label = bootLabel(slot)
	}
	dev, err := s.host.ResolveLabel(label)
	if err != nil {
		return err
	}
	blk, err := s.host.OpenBlock(dev)
	if err != nil {
		return err
	}
	defer func() { _ = blk.Close() }()
	capacity, err := blk.Size()
	if err != nil {
		return err
	}
	if size <= 0 || size > capacity {
		return fmt.Errorf("rpiboot: image of %d bytes does not fit %s (%d bytes)", size, dev, capacity)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(blk, h), io.LimitReader(&ctxReader{ctx, image}, size))
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("rpiboot: image ended after %d of %d bytes", n, size)
	}
	if err := blk.Sync(); err != nil {
		return err
	}
	if _, err := blk.Seek(0, io.SeekStart); err != nil {
		return err
	}
	back := sha256.New()
	if _, err := io.Copy(back, io.LimitReader(blk, size)); err != nil {
		return err
	}
	if !bytes.Equal(back.Sum(nil), h.Sum(nil)) {
		return fmt.Errorf("rpiboot: read-back of %s does not match what was written", dev)
	}
	return nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// RebootInto implements platform.BootSlots: point [tryboot] at the slot's
// boot partition and restart with the tryboot flag.
func (s *Slots) RebootInto(ctx context.Context, slot platform.Slot) error {
	st, err := s.BootState(ctx)
	if err != nil {
		return err
	}
	if slot == st.Running {
		return fmt.Errorf("rpiboot: slot %s is already running", slot)
	}
	trial, err := s.bootPart(slot)
	if err != nil {
		return err
	}
	def, err := s.bootPart(st.Default)
	if err != nil {
		return err
	}
	if err := s.host.WriteFile(s.cfg.AutobootFile, Autoboot{Default: def, Trial: trial}.Render()); err != nil {
		return err
	}
	return s.host.Run(ctx, "systemctl", "reboot", "--reboot-argument=0 tryboot")
}

// RebootDefault implements platform.BootSlots. Without the tryboot flag the
// firmware boots the [all] partition.
func (s *Slots) RebootDefault(ctx context.Context) error {
	return s.host.Run(ctx, "systemctl", "reboot")
}

// Commit implements platform.BootSlots: the running trial slot becomes the
// default, the old default becomes the (idle) trial entry.
func (s *Slots) Commit(ctx context.Context) error {
	st, err := s.BootState(ctx)
	if err != nil {
		return err
	}
	if !st.Tryboot {
		return errors.New("rpiboot: commit is only valid during a trial boot")
	}
	run, err := s.bootPart(st.Running)
	if err != nil {
		return err
	}
	other, err := s.bootPart(st.Running.Other())
	if err != nil {
		return err
	}
	return s.host.WriteFile(s.cfg.AutobootFile, Autoboot{Default: run, Trial: other}.Render())
}
