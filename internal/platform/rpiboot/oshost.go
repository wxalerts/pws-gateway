package rpiboot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wxalerts/pws-gateway/internal/atomicfile"
)

// OSHost is the Host backed by the running Linux system.
type OSHost struct{}

var _ Host = OSHost{}

// ReadFile implements Host.
func (OSHost) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// WriteFile implements Host.
func (OSHost) WriteFile(name string, data []byte) error {
	return atomicfile.WriteFile(name, data, 0o644)
}

// ResolveLabel implements Host.
func (OSHost) ResolveLabel(label string) (string, error) {
	return filepath.EvalSymlinks("/dev/disk/by-label/" + label)
}

// ResolveRoot implements Host.
func (OSHost) ResolveRoot(spec string) (string, error) {
	for prefix, dir := range map[string]string{
		"PARTUUID=": "by-partuuid", "UUID=": "by-uuid", "LABEL=": "by-label",
	} {
		if v, ok := strings.CutPrefix(spec, prefix); ok {
			return filepath.EvalSymlinks("/dev/disk/" + dir + "/" + strings.ToLower(v))
		}
	}
	return filepath.EvalSymlinks(spec)
}

type osBlock struct{ *os.File }

func (b osBlock) Size() (int64, error) {
	end, err := b.Seek(0, 2)
	if err != nil {
		return 0, err
	}
	_, err = b.Seek(0, 0)
	return end, err
}

// OpenBlock implements Host.
func (OSHost) OpenBlock(dev string) (Block, error) {
	if !strings.HasPrefix(dev, "/dev/") {
		return nil, fmt.Errorf("rpiboot: %q is not a device node", dev)
	}
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return osBlock{f}, nil
}

// Run implements Host.
func (OSHost) Run(ctx context.Context, name string, args ...string) error {
	if out, err := exec.CommandContext(ctx, name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
