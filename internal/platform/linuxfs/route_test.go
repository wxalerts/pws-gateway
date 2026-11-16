package linuxfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const routeHeader = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"

func writeRoute(t *testing.T, body string) Host {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "proc", "net")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "route"), []byte(routeHeader+body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Host{Root: root}
}

func TestDefaultRouteInterface(t *testing.T) {
	h := writeRoute(t, ""+
		"wlan0\t00000000\t0100A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n"+
		"eth0\t00000000\t0100A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"+
		"eth0\t0000A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n")
	got, err := h.DefaultRouteInterface()
	if err != nil || got != "eth0" {
		t.Fatalf("got %q, %v; want eth0 (lowest metric)", got, err)
	}
}

func TestDefaultRouteInterfaceNone(t *testing.T) {
	h := writeRoute(t, "eth0\t0000A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n")
	if _, err := h.DefaultRouteInterface(); !errors.Is(err, ErrNoDefaultRoute) {
		t.Fatalf("got %v, want ErrNoDefaultRoute", err)
	}
	if _, err := (Host{Root: t.TempDir()}).DefaultRouteInterface(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: got %v", err)
	}
}
