package modemmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// SystemBus is the production Bus, talking to the system D-Bus.
type SystemBus struct {
	conn *dbus.Conn
}

// Connect opens the system bus. The connection is shared and private to this
// package; Close ends it.
func Connect() (*SystemBus, error) {
	c, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, errors.Join(ErrNoManager, err)
	}
	return &SystemBus{conn: c}, nil
}

// Close releases the bus connection.
func (s *SystemBus) Close() error { return s.conn.Close() }

// Modems implements Bus through the ObjectManager of ModemManager.
func (s *SystemBus) Modems(ctx context.Context) ([]dbus.ObjectPath, error) {
	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	call := s.conn.Object(service, "/org/freedesktop/ModemManager1").
		CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0)
	if call.Err != nil {
		return nil, errors.Join(ErrNoManager, call.Err)
	}
	if err := call.Store(&objs); err != nil {
		return nil, err
	}
	var out []dbus.ObjectPath
	for p, ifaces := range objs {
		if _, ok := ifaces[ifaceModem]; ok {
			out = append(out, p)
		}
	}
	// Stable order so the first modem is the same one every cycle.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// Props implements Bus.
func (s *SystemBus) Props(ctx context.Context, path dbus.ObjectPath, iface string) (map[string]dbus.Variant, error) {
	var props map[string]dbus.Variant
	call := s.conn.Object(service, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, iface)
	if call.Err != nil {
		return nil, fmt.Errorf("modemmanager: %s properties: %w", iface, call.Err)
	}
	if err := call.Store(&props); err != nil {
		return nil, err
	}
	return props, nil
}

// Call implements Bus.
func (s *SystemBus) Call(ctx context.Context, path dbus.ObjectPath, iface, method string, args ...any) ([]any, error) {
	call := s.conn.Object(service, path).CallWithContext(ctx, iface+"."+method, 0, args...)
	if call.Err != nil {
		return nil, call.Err
	}
	return call.Body, nil
}
