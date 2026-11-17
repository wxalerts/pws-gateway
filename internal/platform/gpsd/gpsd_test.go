package gpsd

import (
	"context"
	"net"
	"testing"
	"time"
)

// serve runs a fake gpsd on a loopback listener that answers a WATCH with the
// given lines.
func serve(t *testing.T, lines ...string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener:", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 256)
				_, _ = c.Read(buf) // the ?WATCH request
				for _, l := range lines {
					_, _ = c.Write([]byte(l + "\n"))
				}
				time.Sleep(200 * time.Millisecond)
			}()
		}
	}()
	return ln.Addr().String()
}

const (
	version = `{"class":"VERSION","release":"3.25"}`
	devices = `{"class":"DEVICES","devices":[{"class":"DEVICE","path":"/dev/ttyUSB1"}]}`
)

func TestThreeDimensionalFix(t *testing.T) {
	addr := serve(t, version, devices,
		`{"class":"TPV","mode":3,"lat":30.6321,"lon":-87.0398,"altMSL":31.4}`,
		`{"class":"SKY","uSat":9,"nSat":14}`)
	info, err := (&Client{Addr: addr}).Query(context.Background())
	if err != nil || info == nil {
		t.Fatalf("info=%v err=%v", info, err)
	}
	if info.Fix != 3 || info.Sats != 9 || info.Lat == nil || *info.Lat != 30.6321 || *info.Lon != -87.0398 || info.AltM == nil || *info.AltM != 31.4 {
		t.Errorf("info = %+v", info)
	}
}

func TestNoFixCarriesNoPosition(t *testing.T) {
	addr := serve(t, devices, `{"class":"TPV","mode":1}`,
		`{"class":"SKY","satellites":[{"used":false},{"used":true}]}`)
	info, err := (&Client{Addr: addr}).Query(context.Background())
	if err != nil || info == nil {
		t.Fatalf("info=%v err=%v", info, err)
	}
	if info.Fix != 0 || info.Lat != nil || info.AltM != nil || info.Sats != 1 {
		t.Errorf("info = %+v", info)
	}
}

func TestNoReceiverAttached(t *testing.T) {
	addr := serve(t, version, `{"class":"DEVICES","devices":[]}`)
	info, err := (&Client{Addr: addr}).Query(context.Background())
	if err != nil || info != nil {
		t.Fatalf("want (nil, nil), got %v, %v", info, err)
	}
}

func TestDaemonDown(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	if _, err := (&Client{Addr: addr}).Query(context.Background()); err == nil {
		t.Fatal("want an error when nothing listens")
	}
}
