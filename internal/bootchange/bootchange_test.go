package bootchange

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup(t *testing.T, dir string, current bool, c *clock) (*Manager, *mock.Platform) {
	t.Helper()
	p := mock.New()
	p.I2CValue.Enabled = current
	m, err := New(p, dir, current, time.Minute, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return m, p
}

func TestEnableThenConfirmKeepsTheChange(t *testing.T) {
	c := &clock{time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	m, p := setup(t, t.TempDir(), false, c)
	deadline, err := m.Enable(true)
	if err != nil || !deadline.Equal(c.t.Add(time.Minute)) {
		t.Fatalf("deadline=%v err=%v", deadline, err)
	}
	if !slices.Equal(p.I2CSetCalls, []bool{true}) || m.State().Phase != config.PhaseApplied {
		t.Fatalf("calls=%v state=%+v", p.I2CSetCalls, m.State())
	}
	if err := m.Confirm(); err != nil {
		t.Fatal(err)
	}
	if m.State().Phase != config.PhaseIdle || !m.State().Active.I2C || !slices.Equal(p.I2CSetCalls, []bool{true}) {
		t.Fatalf("after confirm: calls=%v state=%+v", p.I2CSetCalls, m.State())
	}
}

func TestUnconfirmedChangeRollsBackAtDeadline(t *testing.T) {
	c := &clock{time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	m, p := setup(t, t.TempDir(), false, c)
	if _, err := m.Enable(true); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(30 * time.Second)
	if done, _ := m.Expire(); done {
		t.Fatal("rolled back before the deadline")
	}
	c.t = c.t.Add(time.Minute)
	done, err := m.Expire()
	if !done || err != nil {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if !slices.Equal(p.I2CSetCalls, []bool{true, false}) || p.I2CValue.Enabled {
		t.Fatalf("boot config not restored: calls=%v", p.I2CSetCalls)
	}
	if err := m.Confirm(); !errors.Is(err, config.ErrNothingApplied) {
		t.Errorf("confirm after rollback = %v", err)
	}
}

// The reboot that makes I2C take effect restarts the daemon. The pending
// change must survive it, still be confirmable, and still roll back.
func TestPendingChangeSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	c := &clock{time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	m, _ := setup(t, dir, false, c)
	if _, err := m.Enable(true); err != nil {
		t.Fatal(err)
	}

	c.t = c.t.Add(20 * time.Second) // reboot, new process; the boot config now says on
	m2, p2 := setup(t, dir, true, c)
	if st := m2.State(); st.Phase != config.PhaseApplied || st.Deadline == nil {
		t.Fatalf("not restored: %+v", st)
	}
	if err := m2.Confirm(); err != nil {
		t.Fatal(err)
	}
	if len(p2.I2CSetCalls) != 0 {
		t.Errorf("a confirm must not touch the boot config: %v", p2.I2CSetCalls)
	}

	// Second change, never confirmed, and the restart happens after the deadline.
	m3, _ := setup(t, dir, true, c)
	if _, err := m3.Enable(false); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(10 * time.Minute)
	_, p4 := setup(t, dir, false, c)
	if !slices.Equal(p4.I2CSetCalls, []bool{true}) {
		t.Fatalf("an expired change must roll back at startup: %v", p4.I2CSetCalls)
	}
}

func TestFailedApplyLeavesNothingPending(t *testing.T) {
	c := &clock{time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	m, p := setup(t, t.TempDir(), false, c)
	p.I2CSetErr = errors.New("helper not installed")
	if _, err := m.Enable(true); err == nil {
		t.Fatal("want an error")
	}
	if m.State().Phase != config.PhaseIdle {
		t.Fatalf("state = %+v", m.State())
	}
}

func TestSecondChangeBlockedUntilConfirmed(t *testing.T) {
	c := &clock{time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	m, _ := setup(t, t.TempDir(), false, c)
	if _, err := m.Enable(true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Enable(false); !errors.Is(err, config.ErrAlreadyApplied) {
		t.Fatalf("err = %v", err)
	}
}
