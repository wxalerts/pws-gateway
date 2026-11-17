package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform/fake"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
	"github.com/wxalerts/pws-gateway/internal/update/updatetest"
	"github.com/wxalerts/pws-gateway/internal/version"
)

func TestM6FeaturesAreOffWithoutTheirInputs(t *testing.T) {
	cs, dir := newConfig(t)
	a, err := New(Deps{DataDir: dir, Config: cs, Platform: mock.New(), Logger: quietLogger(), ReplayFile: capture})
	if err != nil {
		t.Fatal(err)
	}
	if a.Network() != nil || a.Updates() != nil {
		t.Fatal("network manager and updater must be absent without a NetManager and a pinned root key")
	}
}

func TestM6WiringWithFakes(t *testing.T) {
	old := version.Version
	version.Version = "1.2.3"
	t.Cleanup(func() { version.Version = old })

	cs, dir := newConfig(t)
	if err := os.MkdirAll(filepath.Join(dir, "keys"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keys", "root.pub"), updatetest.Root.PubFile(), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(Deps{
		DataDir: dir, Config: cs, Platform: mock.New(), Logger: quietLogger(), ReplayFile: capture,
		M6: M6Deps{Slots: fake.NewSlots(), Net: fake.NewNet(), Restart: func() {}, ManifestBase: "http://127.0.0.1:1/", AllowInsecureManifest: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Network() == nil || a.Updates() == nil {
		t.Fatal("updater and network manager should be wired")
	}
	if st := a.Updates().Status(); st.State != "idle" || st.Current != "1.2.3" {
		t.Fatalf("status = %+v", st)
	}
	if err := a.updateHealth(context.Background()); err != nil {
		t.Fatalf("an unclaimed device with no radios is healthy: %v", err)
	}
	// A configured radio whose decoder is not running fails the health check.
	if _, err := cs.Update(func(c *config.Config) error {
		c.Radios = []config.Radio{{Serial: "X1", Role: config.RoleRTL433, BandMHz: 433.92}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.updateHealth(context.Background()); err == nil {
		t.Fatal("health must fail when the decoder is not running")
	}
	st, err := a.updateCheckCmd(context.Background())
	if err != nil || st["started"] != true {
		t.Fatalf("update-check command: %v %v", st, err)
	}
}
