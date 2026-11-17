package chrony

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"path/filepath"
	"testing"
	"time"
)

const sourcesNTP = `^,-,time4.lshiy.com,2,10,377,601,-0.002738,-0.002738,0.127
^,*,172.16.50.207,1,10,377,607,-0.000106,-0.000106,0.0003
`

const sourcesGPS = `#,*,GPS,0,4,377,3,0.000100,0.000100,0.0001
^,+,pool.example,2,6,377,10,0.001000,0.001000,0.02
`

func TestSelectedClass(t *testing.T) {
	cases := map[string]string{
		sourcesNTP:                               RefNTP,
		sourcesGPS:                               RefGPS,
		"#,*,LTE,5,6,377,2,0.1,0.1,0.5\n":        RefLTE,
		"#,*,PPS,0,4,377,1,0.0,0.0,0.0\n":        RefGPS,
		"#,*,NMEA,0,4,377,1,0.0,0.0,0.0\n":       RefGPS,
		"^,-,a.example,2,10,377,1,0.1,0.1,0.1\n": "",
		"":                                       "",
	}
	for in, want := range cases {
		if got := SelectedClass(in); got != want {
			t.Errorf("SelectedClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuery(t *testing.T) {
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[1] == "sources" {
			return []byte(sourcesGPS), nil
		}
		return []byte("47505300,GPS,1,1763409408.123456789,0.000012,-0.000030,0.0001,1.2,0.1,0.0,0.001,0.0,0.01,Normal\n"), nil
	}
	st, err := Query(context.Background(), run)
	if err != nil || st.Ref != RefGPS || st.LastSync.Unix() != 1763409408 {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

func TestSockSampleReachesChronySocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lte.sock")
	l, err := net.ListenPacket("unixgram", path)
	if err != nil {
		t.Skip("no unix sockets:", err)
	}
	defer l.Close()
	at := time.Unix(1763409408, 250_000_000)
	if err := (&SockFeeder{Path: path}).Send(at, 750*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_ = l.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := l.ReadFrom(buf)
	if err != nil || n != 40 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	e := binary.NativeEndian
	if e.Uint64(buf[0:]) != 1763409408 || e.Uint64(buf[8:]) != 250000 ||
		math.Float64frombits(e.Uint64(buf[16:])) != 0.75 || e.Uint32(buf[36:]) != 0x534f434b {
		t.Errorf("bad sample % x", buf[:n])
	}
}
