package cwop

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/relay"
)

// fakeAPRSIS is a loopback server speaking the documented exchange. It never
// leaves the machine.
func fakeAPRSIS(t *testing.T, ack string) (addr string, got chan []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback")
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan []string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte("# javAPRSSrvr 4.0\r\n"))
		rd := bufio.NewReader(c)
		login, _ := rd.ReadString('\n')
		_, _ = c.Write([]byte(ack + "\r\n"))
		pkt, _ := rd.ReadString('\n')
		got <- []string{login, pkt}
	}()
	return ln.Addr().String(), got
}

func TestTCPSenderExchange(t *testing.T) {
	addr, got := fakeAPRSIS(t, "# logresp CW1234 unverified, server T2TEST")
	err := TCPSender{Timeout: 2 * time.Second}.Send(context.Background(), []string{addr}, "user CW1234 pass -1 vers x 1", "CW1234>APRS,TCPIP*:@x")
	if err != nil {
		t.Fatal(err)
	}
	g := <-got
	if g[0] != "user CW1234 pass -1 vers x 1\r\n" || g[1] != "CW1234>APRS,TCPIP*:@x\r\n" {
		t.Errorf("wire %q", g)
	}
}

func TestTCPSenderLoginRefused(t *testing.T) {
	addr, _ := fakeAPRSIS(t, "# logresp W5XYZ invalid passcode")
	err := TCPSender{Timeout: 2 * time.Second}.Send(context.Background(), []string{addr}, "login", "pkt")
	if relay.CodeOf(err) != relay.CodeLogin {
		t.Errorf("err %v", err)
	}
}

func TestTCPSenderFallsBack(t *testing.T) {
	addr, got := fakeAPRSIS(t, "# logresp ok")
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().String()
	dead.Close()
	if err := (TCPSender{Timeout: time.Second}).Send(context.Background(), []string{deadAddr, addr}, "l", "p"); err != nil {
		t.Fatal(err)
	}
	if g := <-got; !strings.HasPrefix(g[1], "p") {
		t.Errorf("%q", g)
	}
	err := TCPSender{Timeout: time.Second}.Send(context.Background(), []string{deadAddr}, "l", "p")
	if relay.CodeOf(err) != relay.CodeConnect {
		t.Errorf("err %v", err)
	}
}
