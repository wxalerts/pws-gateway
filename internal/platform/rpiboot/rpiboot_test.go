package rpiboot_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/rpiboot"
)

type memBlock struct {
	buf  []byte
	pos  int
	size int
}

func (m *memBlock) Write(p []byte) (int, error) {
	n := copy(m.buf[m.pos:], p)
	m.pos += n
	if n < len(p) {
		return n, errors.New("no space")
	}
	return n, nil
}
func (m *memBlock) Read(p []byte) (int, error) {
	if m.pos >= len(m.buf) {
		return 0, errors.New("eof")
	}
	n := copy(p, m.buf[m.pos:])
	m.pos += n
	return n, nil
}
func (m *memBlock) Seek(off int64, _ int) (int64, error) { m.pos = int(off); return off, nil }
func (m *memBlock) Size() (int64, error)                 { return int64(m.size), nil }
func (m *memBlock) Sync() error                          { return nil }
func (m *memBlock) Close() error                         { return nil }

// fakeHost models the disk layout: bootfs-a=p2, bootfs-b=p3, rootfs-a=p5,
// rootfs-b=p6 on mmcblk0.
type fakeHost struct {
	files map[string][]byte
	blks  map[string]*memBlock
	runs  [][]string
}

func newHost(running string, trial bool, defPart int) *fakeHost {
	root := "/dev/mmcblk0p5"
	if running == "b" {
		root = "/dev/mmcblk0p6"
	}
	h := &fakeHost{files: map[string][]byte{}, blks: map[string]*memBlock{}}
	h.files["/proc/cmdline"] = []byte("console=tty1 root=" + root + " rootwait\n")
	h.files["/boot/autoboot/autoboot.txt"] = rpiboot.Autoboot{Default: defPart, Trial: 5}.Render()
	if trial {
		h.files["/prop/tryboot"] = []byte{0, 0, 0, 1}
	} else {
		h.files["/prop/tryboot"] = []byte{0, 0, 0, 0}
	}
	for _, d := range []string{"/dev/mmcblk0p2", "/dev/mmcblk0p3", "/dev/mmcblk0p5", "/dev/mmcblk0p6"} {
		h.blks[d] = &memBlock{buf: make([]byte, 1024), size: 1024}
	}
	return h
}

var labels = map[string]string{
	"bootfs-a": "/dev/mmcblk0p2", "bootfs-b": "/dev/mmcblk0p3", "rootfs-a": "/dev/mmcblk0p5", "rootfs-b": "/dev/mmcblk0p6",
}

func (h *fakeHost) ReadFile(n string) ([]byte, error) {
	if b, ok := h.files[n]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("no file %s", n)
}
func (h *fakeHost) WriteFile(n string, d []byte) error { h.files[n] = d; return nil }
func (h *fakeHost) ResolveLabel(l string) (string, error) {
	if d, ok := labels[l]; ok {
		return d, nil
	}
	return "", fmt.Errorf("no label %s", l)
}
func (h *fakeHost) ResolveRoot(s string) (string, error) { return s, nil }
func (h *fakeHost) OpenBlock(d string) (rpiboot.Block, error) {
	b := h.blks[d]
	b.pos = 0
	return b, nil
}
func (h *fakeHost) Run(_ context.Context, name string, args ...string) error {
	h.runs = append(h.runs, append([]string{name}, args...))
	return nil
}

func slots(h *fakeHost) *rpiboot.Slots {
	return rpiboot.New(rpiboot.Config{AutobootFile: "/boot/autoboot/autoboot.txt", TrybootProp: "/prop/tryboot"}, h)
}

func TestAutobootRoundTrip(t *testing.T) {
	text := "# comment\n[all]\ntryboot_a_b=1\nboot_partition=2\n[tryboot]\nboot_partition=3\n"
	a, err := rpiboot.ParseAutoboot([]byte(text))
	if err != nil || a.Default != 2 || a.Trial != 3 {
		t.Fatalf("%+v %v", a, err)
	}
	if got := string(a.Render()); got != "[all]\ntryboot_a_b=1\nboot_partition=2\n[tryboot]\nboot_partition=3\n" {
		t.Fatalf("render:\n%s", got)
	}
	if _, err := rpiboot.ParseAutoboot([]byte("[tryboot]\nboot_partition=3\n")); err == nil {
		t.Fatal("missing [all] boot_partition accepted")
	}
}

func TestBootState(t *testing.T) {
	st, err := slots(newHost("a", false, 2)).BootState(context.Background())
	if err != nil || st != (platform.BootState{Running: platform.SlotA, Default: platform.SlotA}) {
		t.Fatalf("%+v %v", st, err)
	}
	st, _ = slots(newHost("b", true, 2)).BootState(context.Background())
	if st != (platform.BootState{Running: platform.SlotB, Default: platform.SlotA, Tryboot: true}) {
		t.Fatalf("%+v", st)
	}
}

func TestWriteSlotWritesVerifiesAndRefusesRunning(t *testing.T) {
	h := newHost("a", false, 2)
	s := slots(h)
	img := bytes.Repeat([]byte("xyz"), 100)
	if err := s.WriteSlot(context.Background(), platform.SlotB, platform.PartRoot, bytes.NewReader(img), int64(len(img))); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(h.blks["/dev/mmcblk0p6"].buf[:300], img) {
		t.Fatal("rootfs-b does not hold the image")
	}
	if err := s.WriteSlot(context.Background(), platform.SlotA, platform.PartRoot, bytes.NewReader(img), 300); err == nil {
		t.Fatal("wrote the running slot")
	}
	if err := s.WriteSlot(context.Background(), platform.SlotB, platform.PartBoot, bytes.NewReader(img), 5000); err == nil {
		t.Fatal("oversized image accepted")
	}
	if err := s.WriteSlot(context.Background(), platform.SlotB, platform.PartBoot, strings.NewReader("short"), 300); err == nil {
		t.Fatal("short image accepted")
	}
}

func TestRebootIntoArmsTrialOnTheOtherSlot(t *testing.T) {
	h := newHost("a", false, 2)
	if err := slots(h).RebootInto(context.Background(), platform.SlotB); err != nil {
		t.Fatal(err)
	}
	a, _ := rpiboot.ParseAutoboot(h.files["/boot/autoboot/autoboot.txt"])
	if a.Default != 2 || a.Trial != 3 {
		t.Fatalf("autoboot = %+v", a)
	}
	if len(h.runs) != 1 || h.runs[0][len(h.runs[0])-1] != "--reboot-argument=0 tryboot" {
		t.Fatalf("runs = %v", h.runs)
	}
	if err := slots(h).RebootInto(context.Background(), platform.SlotA); err == nil {
		t.Fatal("rebooting into the running slot must fail")
	}
}

func TestCommitSwapsDefaultOnlyDuringTrial(t *testing.T) {
	h := newHost("b", false, 2)
	if err := slots(h).Commit(context.Background()); err == nil {
		t.Fatal("commit outside a trial")
	}
	h = newHost("b", true, 2)
	if err := slots(h).Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, _ := rpiboot.ParseAutoboot(h.files["/boot/autoboot/autoboot.txt"])
	if a.Default != 3 || a.Trial != 2 {
		t.Fatalf("autoboot = %+v", a)
	}
	// RebootDefault carries no tryboot flag, which is the rollback path.
	if err := slots(h).RebootDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	last := h.runs[len(h.runs)-1]
	if strings.Contains(strings.Join(last, " "), "tryboot") {
		t.Fatalf("default reboot carries the tryboot flag: %v", last)
	}
}
