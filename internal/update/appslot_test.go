package update_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wxalerts/pws-gateway/internal/update"
)

func newSlots(t *testing.T, reply string) (*update.AppSlots, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	return &update.AppSlots{Dir: dir, Run: func(_ context.Context, path string, args ...string) ([]byte, error) {
		if len(args) != 1 || args[0] != "version" {
			t.Errorf("unexpected args %v", args)
		}
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
		if reply == "" {
			return nil, errors.New("exec format error")
		}
		return []byte(reply), nil
	}}, dir
}

func stagedFile(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "staged")
	if err := os.WriteFile(p, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAppSlotsInstallRevertAndReexec(t *testing.T) {
	s, dir := newSlots(t, "pwsgw 1.3.0 (abc, today)\n")
	ctx := context.Background()
	if _, ok := update.ReexecTarget(dir, 1020399); ok {
		t.Fatal("re-exec target before any install")
	}
	if err := s.Install(ctx, stagedFile(t), "1.3.0", 1030099); err != nil {
		t.Fatal(err)
	}
	p, ok := update.ReexecTarget(dir, 1020399)
	if !ok || !strings.HasSuffix(p, "1030099/pwsgw") {
		t.Fatalf("target = %q %v", p, ok)
	}
	// A binary at least as new as the slot, for instance one that arrived in
	// an OS image, wins over the slot.
	if _, ok := update.ReexecTarget(dir, 1030099); ok {
		t.Fatal("equal version must not re-exec")
	}

	s.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte("pwsgw 1.4.0 (x, y)"), nil }
	if err := s.Install(ctx, stagedFile(t), "1.4.0", 1040099); err != nil {
		t.Fatal(err)
	}
	if p, _ := update.ReexecTarget(dir, 0); !strings.Contains(p, "1040099") {
		t.Fatalf("active = %q", p)
	}
	if err := s.Revert(ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := update.ReexecTarget(dir, 0); !strings.Contains(p, "1030099") {
		t.Fatalf("after revert active = %q", p)
	}
	if err := s.Revert(ctx); err != nil { // back to the built-in binary
		t.Fatal(err)
	}
	if _, ok := update.ReexecTarget(dir, 0); ok {
		t.Fatal("active link should be gone")
	}
}

func TestAppSlotsRefuseBinaryThatDoesNotStartOrLies(t *testing.T) {
	s, dir := newSlots(t, "")
	if err := s.Install(context.Background(), stagedFile(t), "1.3.0", 1030099); err == nil {
		t.Fatal("a binary that fails to start was installed")
	}
	if _, ok := update.ReexecTarget(dir, 0); ok {
		t.Fatal("link points at a rejected binary")
	}
	s2, _ := newSlots(t, "pwsgw 9.9.9 (x, y)")
	if err := s2.Install(context.Background(), stagedFile(t), "1.3.0", 1030099); err == nil {
		t.Fatal("a binary reporting the wrong version was installed")
	}
}

func TestAppSlotsCommitKeepsFewOldBinaries(t *testing.T) {
	s, dir := newSlots(t, "")
	for _, v := range []struct {
		ver string
		n   int
	}{{"1.0.0", 1000099}, {"1.1.0", 1010099}, {"1.2.0", 1020099}, {"1.3.0", 1030099}} {
		v := v
		s.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte("pwsgw " + v.ver), nil }
		if err := s.Install(context.Background(), stagedFile(t), v.ver, v.n); err != nil {
			t.Fatal(err)
		}
	}
	s.Commit()
	entries, _ := os.ReadDir(dir)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 3 { // active plus two old
		t.Fatalf("dirs = %v", dirs)
	}
}

func TestLoadTrustFromDataDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := update.LoadTrust(dir); !errors.Is(err, update.ErrNoRoots) {
		t.Fatalf("err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "keys"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{"root.pub": rootPub(), "root-backup.pub": backupPub()} {
		if err := os.WriteFile(filepath.Join(dir, "keys", name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := update.LoadTrust(dir)
	if err != nil || len(tr.Roots) != 2 {
		t.Fatalf("trust = %+v, %v", tr, err)
	}
}
