package nmdbus

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestIsConnLost(t *testing.T) {
	for _, err := range []error{io.EOF, dbus.ErrClosed, fmt.Errorf("update: %w", io.EOF), errors.New("read unix @->/run/dbus/system_bus_socket: EOF")} {
		if !isConnLost(err) {
			t.Errorf("%v not treated as a lost connection", err)
		}
	}
	if isConnLost(errors.New("org.freedesktop.DBus.Error.InvalidArgs: bad")) {
		t.Error("an application error must not trigger a reconnect")
	}
}
