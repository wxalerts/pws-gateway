package nmdbus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
)

// Well known bus names, paths and interfaces.
const (
	nmDest       = "org.freedesktop.NetworkManager"
	nmPath       = dbus.ObjectPath("/org/freedesktop/NetworkManager")
	nmIface      = "org.freedesktop.NetworkManager"
	settingsPath = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings")
	settingsIf   = "org.freedesktop.NetworkManager.Settings"
	connIf       = "org.freedesktop.NetworkManager.Settings.Connection"
	deviceIf     = "org.freedesktop.NetworkManager.Device"
	wirelessIf   = "org.freedesktop.NetworkManager.Device.Wireless"
	activeIf     = "org.freedesktop.NetworkManager.Connection.Active"
	ip4If        = "org.freedesktop.NetworkManager.IP4Config"
	apIf         = "org.freedesktop.NetworkManager.AccessPoint"

	hostnameDest = "org.freedesktop.hostname1"
	hostnamePath = dbus.ObjectPath("/org/freedesktop/hostname1")
	hostnameIf   = "org.freedesktop.hostname1"

	noObject = dbus.ObjectPath("/")
)

// bus is the small slice of D-Bus the manager needs. Tests replace it with a
// fake object tree so nothing needs a real system bus.
type bus interface {
	// call invokes a method. method is the full "interface.Name" form and the
	// result is the reply body.
	call(ctx context.Context, dest string, path dbus.ObjectPath, method string, args ...any) ([]any, error)
	// prop reads one property and returns its unwrapped value.
	prop(ctx context.Context, dest string, path dbus.ObjectPath, iface, name string) (any, error)
}

// systemBus connects to the system bus on first use, so building a Manager on
// a machine without a bus (a dev laptop, CI) does not fail until it is used.
type systemBus struct {
	mu   sync.Mutex
	conn *dbus.Conn
}

func (s *systemBus) connect() (*dbus.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil && s.conn.Connected() {
		return s.conn, nil
	}
	c, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("nmdbus: connect system bus: %w", err)
	}
	s.conn = c
	return c, nil
}

func (s *systemBus) call(ctx context.Context, dest string, path dbus.ObjectPath, method string, args ...any) ([]any, error) {
	body, err := s.callOnce(ctx, dest, path, method, args...)
	if err != nil && isConnLost(err) && ctx.Err() == nil {
		// The bus dropped our connection (seen on a Pi after minutes idle).
		// Every call the manager makes is safe to repeat, so reconnect once.
		s.drop()
		return s.callOnce(ctx, dest, path, method, args...)
	}
	return body, err
}

func (s *systemBus) callOnce(ctx context.Context, dest string, path dbus.ObjectPath, method string, args ...any) ([]any, error) {
	c, err := s.connect()
	if err != nil {
		return nil, err
	}
	call := c.Object(dest, path).CallWithContext(ctx, method, 0, args...)
	if call.Err != nil {
		return nil, call.Err
	}
	return call.Body, nil
}

// drop forgets a dead connection so the next call dials again.
func (s *systemBus) drop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
}

func isConnLost(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, dbus.ErrClosed) || errors.Is(err, net.ErrClosed) ||
		strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "use of closed")
}

func (s *systemBus) prop(ctx context.Context, dest string, path dbus.ObjectPath, iface, name string) (any, error) {
	body, err := s.call(ctx, dest, path, "org.freedesktop.DBus.Properties.Get", iface, name)
	if err != nil {
		return nil, err
	}
	if len(body) != 1 {
		return nil, errors.New("nmdbus: unexpected property reply")
	}
	v, ok := body[0].(dbus.Variant)
	if !ok {
		return nil, errors.New("nmdbus: property reply is not a variant")
	}
	return v.Value(), nil
}

// as asserts v to T, turning a bus error or a type mismatch into an error.
func as[T any](v any, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	t, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("nmdbus: unexpected D-Bus type %T, want %T", v, zero)
	}
	return t, nil
}

// reply0 returns the first element of a reply body as T.
func reply0[T any](body []any, err error) (T, error) {
	if err != nil {
		var zero T
		return zero, err
	}
	if len(body) == 0 {
		var zero T
		return zero, errors.New("nmdbus: empty D-Bus reply")
	}
	return as[T](body[0], nil)
}
