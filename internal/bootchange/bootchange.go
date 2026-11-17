// Package bootchange wraps a change to the boot configuration in the same
// commit-confirm-rollback machine the network settings use (config.Commit).
//
// Enabling I2C edits the boot partition and only takes effect after a reboot,
// so the process that applied the change is not the one that confirms it. The
// pending change is therefore written to the data directory, reloaded at
// start, and rolled back if the confirmation window passes without a
// confirm: a bad edit cannot stay in place unnoticed.
package bootchange

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

// File is the persisted pending change, inside the data directory.
const File = "bootchange.json"

// DefaultWindow is how long the operator has to reboot, come back and confirm.
// It is longer than the network window because a Pi takes a minute or two to
// boot and the owner has to trigger the reboot.
const DefaultWindow = 15 * time.Minute

// applyTimeout bounds one call into the platform.
const applyTimeout = 30 * time.Second

// Settings are the boot settings under commit-confirm.
type Settings struct {
	I2C bool `json:"i2c"`
}

// Manager owns one commit-confirm machine for the boot configuration.
type Manager struct {
	plat   platform.Platform
	path   string
	window time.Duration
	now    func() time.Time
	commit *config.Commit[Settings]
}

type persisted struct {
	Previous Settings  `json:"previous"`
	Active   Settings  `json:"active"`
	Deadline time.Time `json:"deadline"`
}

// New builds the manager. current is the I2C setting in the boot
// configuration right now. A change that was applied before a restart is
// restored; one whose deadline has already passed is rolled back at once.
func New(plat platform.Platform, dataDir string, current bool, window time.Duration, now func() time.Time) (*Manager, error) {
	if window <= 0 {
		window = DefaultWindow
	}
	if now == nil {
		now = time.Now
	}
	m := &Manager{plat: plat, path: filepath.Join(dataDir, File), window: window, now: now}
	m.commit = config.NewCommit(Settings{I2C: current}, m.applySettings, now)

	b, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var p persisted
	if err := json.Unmarshal(b, &p); err != nil {
		_ = os.Remove(m.path) // an unreadable record cannot be rolled back; drop it
		return m, nil
	}
	m.commit.Restore(p.Active, p.Previous, p.Deadline)
	if _, err := m.Expire(); err != nil {
		return m, err
	}
	return m, nil
}

func (m *Manager) applySettings(s Settings) error {
	ctx, cancel := context.WithTimeout(context.Background(), applyTimeout)
	defer cancel()
	return m.plat.SetI2CEnabled(ctx, s.I2C)
}

// Enable stages and applies a new I2C setting and starts the confirmation
// window. It returns the deadline. The change needs a reboot to take effect.
func (m *Manager) Enable(enabled bool) (time.Time, error) {
	if err := m.commit.Stage(Settings{I2C: enabled}); err != nil {
		return time.Time{}, err
	}
	deadline, err := m.commit.Apply(m.window)
	if err != nil {
		_ = m.Rollback() //nolint:errcheck // the apply error is the one to report
		return time.Time{}, err
	}
	st := m.commit.State()
	var prev Settings
	prev.I2C = !st.Active.I2C // a change always flips the setting
	if err := m.save(persisted{Previous: prev, Active: st.Active, Deadline: deadline}); err != nil {
		// Without a record a rollback cannot survive a restart; undo now.
		_ = m.Rollback() //nolint:errcheck // the save error is the one to report
		return time.Time{}, err
	}
	return deadline, nil
}

// Confirm keeps the applied change.
func (m *Manager) Confirm() error {
	err := m.commit.Confirm()
	if err == nil || errors.Is(err, config.ErrDeadlinePassed) {
		_ = os.Remove(m.path)
	}
	return err
}

// Rollback reinstates the previous setting, or discards a staged one.
func (m *Manager) Rollback() error {
	err := m.commit.Rollback()
	_ = os.Remove(m.path)
	return err
}

// Expire rolls back an applied change whose window has passed. It returns true
// when it did.
func (m *Manager) Expire() (bool, error) {
	done, err := m.commit.Expire()
	if done {
		_ = os.Remove(m.path)
	}
	return done, err
}

// State is a snapshot for the API.
func (m *Manager) State() config.State[Settings] { return m.commit.State() }

func (m *Manager) save(p persisted) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
