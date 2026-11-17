package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AppSlots keeps downloaded pwsgw binaries under Dir and an "active" symlink
// that names the one to run. The systemd unit runs /usr/local/bin/pwsgw (part
// of the OS image, read only) and that binary hands over to the active one at
// startup (ReexecTarget), so an app update needs no write access outside the
// data directory and a rollback is repointing a symlink.
//
//	Dir/<version_number>/pwsgw    one directory per installed binary
//	Dir/active                    symlink to <version_number>
//	Dir/prev                      name of the previous active, empty for none
type AppSlots struct {
	Dir string
	// Run executes path with args and returns its stdout. nil runs the real
	// binary with a ten second limit.
	Run func(ctx context.Context, path string, args ...string) ([]byte, error)
}

const (
	appBinary   = "pwsgw"
	activeLink  = "active"
	prevFile    = "prev"
	smokeLimit  = 10 * time.Second
	keepOldApps = 2
)

func (a *AppSlots) run(ctx context.Context, path string, args ...string) ([]byte, error) {
	if a.Run != nil {
		return a.Run(ctx, path, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, smokeLimit)
	defer cancel()
	return exec.CommandContext(ctx, path, args...).Output()
}

// Install copies the hash-verified staged binary into place, proves it starts
// by asking it for its version, and makes it the active one. The previous
// active slot is remembered for Revert.
func (a *AppSlots) Install(ctx context.Context, staged, version string, versionNumber int) error {
	name := strconv.Itoa(versionNumber)
	dir := filepath.Join(a.Dir, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	dst := filepath.Join(dir, appBinary)
	if err := copyExecutable(staged, dst+".tmp"); err != nil {
		return err
	}
	out, err := a.run(ctx, dst+".tmp", "version")
	if err != nil {
		_ = os.Remove(dst + ".tmp")
		return fmt.Errorf("new binary does not start: %w", err)
	}
	if !strings.Contains(string(out), appBinary+" "+version) {
		_ = os.Remove(dst + ".tmp")
		return fmt.Errorf("new binary reports %q, expected version %s", strings.TrimSpace(string(out)), version)
	}
	if err := os.Rename(dst+".tmp", dst); err != nil {
		return err
	}
	prev := a.activeName()
	if prev == name {
		prev = a.readPrev()
	}
	if err := os.WriteFile(filepath.Join(a.Dir, prevFile), []byte(prev), 0o600); err != nil {
		return err
	}
	return a.point(name)
}

// Revert makes the previous binary active again, or removes the override so
// the built-in binary runs when there was no previous one.
func (a *AppSlots) Revert(_ context.Context) error {
	prev := a.readPrev()
	if prev == "" {
		if err := os.Remove(filepath.Join(a.Dir, activeLink)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	} else if err := a.point(prev); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.Dir, prevFile), nil, 0o600)
}

// Commit forgets the previous slot and removes binaries that are no longer
// needed, keeping the active one and keepOldApps older ones.
func (a *AppSlots) Commit() {
	active := a.activeName()
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return
	}
	var old []int
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err == nil && e.IsDir() && e.Name() != active {
			old = append(old, n)
		}
	}
	for i := 0; i < len(old); i++ { // small list, simple selection by max
		for j := i + 1; j < len(old); j++ {
			if old[j] > old[i] {
				old[i], old[j] = old[j], old[i]
			}
		}
	}
	for i, n := range old {
		if i >= keepOldApps {
			_ = os.RemoveAll(filepath.Join(a.Dir, strconv.Itoa(n)))
		}
	}
}

func (a *AppSlots) activeName() string {
	t, err := os.Readlink(filepath.Join(a.Dir, activeLink))
	if err != nil {
		return ""
	}
	return filepath.Base(t)
}

func (a *AppSlots) readPrev() string {
	b, err := os.ReadFile(filepath.Join(a.Dir, prevFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// point swaps the active symlink atomically.
func (a *AppSlots) point(name string) error {
	tmp := filepath.Join(a.Dir, activeLink+".tmp")
	_ = os.Remove(tmp)
	if err := os.Symlink(name, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(a.Dir, activeLink))
}

// ReexecTarget returns the binary the daemon should hand over to at startup:
// the active app slot, when its version number is higher than the running
// binary's. A binary baked into a newer OS image therefore wins over a stale
// app slot, and a missing or broken link means "run the built-in binary".
func ReexecTarget(dir string, selfNumber int) (string, bool) {
	t, err := os.Readlink(filepath.Join(dir, activeLink))
	if err != nil {
		return "", false
	}
	n, err := strconv.Atoi(filepath.Base(t))
	if err != nil || n <= selfNumber {
		return "", false
	}
	p := filepath.Join(dir, filepath.Base(t), appBinary)
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&0o100 == 0 {
		return "", false
	}
	return p, true
}

func copyExecutable(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o750) //nolint:gosec // the staged binary must be executable
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
