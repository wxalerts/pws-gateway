// Package gpsd reads the GNSS state from a local gpsd over its JSON socket
// protocol. gpsd owns the receiver and feeds chrony; this package only asks it
// what it sees, so the daemon never opens the serial port.
package gpsd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

// DefaultAddr is where gpsd listens.
const DefaultAddr = "127.0.0.1:2947"

// ErrNoDaemon means gpsd could not be reached.
var ErrNoDaemon = errors.New("gpsd: daemon not reachable")

// Info is what the daemon reports.
type Info struct {
	// Fix is 0 (none), 2 or 3.
	Fix  int
	Sats int
	Lat  *float64
	Lon  *float64
	AltM *float64
}

// Dialer opens the connection, replaced by tests.
type Dialer func(ctx context.Context, addr string) (net.Conn, error)

// Client queries gpsd.
type Client struct {
	Addr string
	Dial Dialer
	// Wait bounds one query. Default 4 seconds: gpsd reports a TPV about
	// once a second, and a SKY at a similar rate.
	Wait time.Duration
}

func (c *Client) addr() string {
	if c.Addr == "" {
		return DefaultAddr
	}
	return c.Addr
}

type msg struct {
	Class      string  `json:"class"`
	Mode       int     `json:"mode"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	AltMSL     float64 `json:"altMSL"`
	Alt        float64 `json:"alt"`
	USat       *int    `json:"uSat"`
	Satellites []struct {
		Used bool `json:"used"`
	} `json:"satellites"`
	Devices []json.RawMessage `json:"devices"`
}

// Query connects, turns on the JSON watch and collects the first TPV and SKY.
// It returns (nil, nil) when gpsd runs but has no receiver attached.
func (c *Client) Query(ctx context.Context) (*Info, error) {
	wait := c.Wait
	if wait <= 0 {
		wait = 4 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	dial := c.Dial
	if dial == nil {
		dial = func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}
	}
	conn, err := dial(ctx, c.addr())
	if err != nil {
		return nil, errors.Join(ErrNoDaemon, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl) //nolint:errcheck // the context timeout still bounds the read
	}
	if _, err := conn.Write([]byte("?WATCH={\"enable\":true,\"json\":true}\n")); err != nil {
		return nil, errors.Join(ErrNoDaemon, err)
	}

	var st collected
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var m msg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if st.apply(m) {
			return nil, nil // gpsd runs but lists no device
		}
		if st.haveTPV && st.haveSKY {
			return &st.info, nil
		}
	}
	// Timed out or closed. A device that has not produced a TPV yet still
	// exists, so report it with no fix instead of hiding it.
	if st.devicesSeen || st.haveTPV || st.haveSKY {
		return &st.info, nil
	}
	return nil, ErrNoDaemon
}

// collected accumulates the messages of one query.
type collected struct {
	info                          Info
	haveTPV, haveSKY, devicesSeen bool
}

// apply folds one message in. It returns true when gpsd reports an empty
// device list.
func (c *collected) apply(m msg) bool {
	switch m.Class {
	case "DEVICES":
		c.devicesSeen = true
		return len(m.Devices) == 0
	case "TPV":
		c.haveTPV = true
		c.info.Fix = 0
		if m.Mode >= 2 {
			c.info.Fix = m.Mode
			lat, lon := m.Lat, m.Lon
			c.info.Lat, c.info.Lon = &lat, &lon
			if m.Mode >= 3 {
				alt := m.AltMSL
				if alt == 0 {
					alt = m.Alt
				}
				c.info.AltM = &alt
			}
		}
	case "SKY":
		c.haveSKY = true
		if m.USat != nil {
			c.info.Sats = *m.USat
			return false
		}
		n := 0
		for _, sat := range m.Satellites {
			if sat.Used {
				n++
			}
		}
		c.info.Sats = n
	}
	return false
}
