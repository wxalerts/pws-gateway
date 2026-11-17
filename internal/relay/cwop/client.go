package cwop

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/relay"
)

// Sender delivers one login and one packet to an APRS-IS server. The plugin
// talks to it through this interface so tests use a fake and nothing in the
// test suite reaches CWOP.
type Sender interface {
	// Send connects to the first reachable server in servers, logs in with
	// login, sends packet and disconnects. login and packet carry no line
	// terminator; the sender adds CR LF.
	Send(ctx context.Context, servers []string, login, packet string) error
}

// TCPSender is the real APRS-IS client.
type TCPSender struct {
	// Timeout bounds each connection attempt and each read or write.
	Timeout time.Duration
}

// Send implements Sender.
func (t TCPSender) Send(ctx context.Context, servers []string, login, packet string) error {
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	var last error
	for _, addr := range servers {
		err := t.sendOne(ctx, addr, timeout, login, packet)
		if err == nil {
			return nil
		}
		last = err
		var ce *relay.Error
		if errors.As(err, &ce) && ce.Code == relay.CodeLogin {
			return err
		}
	}
	return last
}

func (t TCPSender) sendOne(ctx context.Context, addr string, timeout time.Duration, login, packet string) error {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return relay.Errorf(relay.CodeConnect, err.Error())
	}
	defer conn.Close()
	step := func() error { return conn.SetDeadline(time.Now().Add(timeout)) }
	rd := bufio.NewReader(conn)

	if err := step(); err != nil {
		return relay.Errorf(relay.CodeConnect, err.Error())
	}
	if _, err := rd.ReadString('\n'); err != nil { // server banner
		return relay.Errorf(relay.CodeConnect, "no banner from "+addr+": "+err.Error())
	}
	if err := step(); err != nil {
		return relay.Errorf(relay.CodeLogin, err.Error())
	}
	if _, err := fmt.Fprintf(conn, "%s\r\n", login); err != nil {
		return relay.Errorf(relay.CodeLogin, "writing the login line: "+err.Error())
	}
	if err := step(); err != nil {
		return relay.Errorf(relay.CodeLogin, err.Error())
	}
	ack, err := rd.ReadString('\n')
	if err != nil {
		return relay.Errorf(relay.CodeLogin, "no login acknowledgement from "+addr+": "+err.Error())
	}
	if strings.Contains(strings.ToLower(ack), "invalid") || strings.Contains(strings.ToLower(ack), "denied") {
		return relay.Errorf(relay.CodeLogin, "the server refused the login: "+strings.TrimSpace(ack))
	}
	if err := step(); err != nil {
		return relay.Errorf(relay.CodeSend, err.Error())
	}
	if _, err := fmt.Fprintf(conn, "%s\r\n", packet); err != nil {
		return relay.Errorf(relay.CodeSend, "writing the packet: "+err.Error())
	}
	return nil
}
