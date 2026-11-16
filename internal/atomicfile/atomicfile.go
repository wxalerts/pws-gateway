// Package atomicfile writes small state files so that a power cut never leaves
// a half-written file behind. The Pi runs from an SD card and loses power
// without warning, so every JSON file pwsgw owns goes through WriteFile.
//
// The sequence is the usual one: write to a temporary file in the same
// directory, fsync it, rename it over the target, then fsync the directory so
// the rename itself is durable.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile atomically replaces path with data using the given permissions.
// The parent directory must already exist.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Any failure below removes the temp file so a crash loop cannot fill the
	// directory with stale fragments.
	cleanup := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}

	if err := tmp.Chmod(perm); err != nil {
		return cleanup(fmt.Errorf("atomicfile: chmod: %w", err))
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(fmt.Errorf("atomicfile: write: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(fmt.Errorf("atomicfile: fsync: %w", err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("atomicfile: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	return syncDir(dir)
}

// syncDir flushes the directory entry. Some filesystems (and the tmpfs used in
// tests) do not support fsync on directories; that is not an error.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("atomicfile: open dir: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !isUnsupported(err) {
		return fmt.Errorf("atomicfile: fsync dir: %w", err)
	}
	return nil
}
