package config

import (
	"errors"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time              { return f.t }
func (f *fakeClock) Advance(d time.Duration)     { f.t = f.t.Add(d) }
func newClock() *fakeClock                       { return &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} }
func settings(mode, addr string) NetworkSettings { return NetworkSettings{Mode: mode, Address: addr} }

func TestCommitApplyConfirm(t *testing.T) {
	clk := newClock()
	var applied []NetworkSettings
	c := NewCommit(settings("dhcp", ""), func(n NetworkSettings) error {
		applied = append(applied, n)
		return nil
	}, clk.Now)

	if st := c.State(); st.Phase != PhaseIdle || st.Pending != nil || st.Deadline != nil {
		t.Fatalf("initial state %+v", st)
	}
	if _, err := c.Apply(time.Minute); !errors.Is(err, ErrNothingStaged) {
		t.Fatalf("Apply without stage err = %v", err)
	}
	if err := c.Confirm(); !errors.Is(err, ErrNothingApplied) {
		t.Fatalf("Confirm without apply err = %v", err)
	}

	cand := settings("static", "192.168.1.50/24")
	if err := c.Stage(cand); err != nil {
		t.Fatal(err)
	}
	if st := c.State(); st.Phase != PhaseStaged || st.Pending == nil || st.Pending.Address != cand.Address {
		t.Fatalf("staged state %+v", st)
	}
	if _, err := c.Apply(0); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("Apply(0) err = %v", err)
	}
	deadline, err := c.Apply(2 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if want := clk.Now().Add(2 * time.Minute); !deadline.Equal(want) {
		t.Fatalf("deadline = %v, want %v", deadline, want)
	}
	if len(applied) != 1 || applied[0].Address != cand.Address {
		t.Fatalf("apply callback calls = %+v", applied)
	}
	if err := c.Stage(settings("dhcp", "")); !errors.Is(err, ErrAlreadyApplied) {
		t.Fatalf("Stage while applied err = %v", err)
	}
	st := c.State()
	if st.Phase != PhaseApplied || st.Deadline == nil || st.Active.Address != cand.Address {
		t.Fatalf("applied state %+v", st)
	}

	clk.Advance(90 * time.Second)
	if rolled, err := c.Expire(); rolled || err != nil {
		t.Fatalf("Expire before deadline = %v %v", rolled, err)
	}
	if err := c.Confirm(); err != nil {
		t.Fatal(err)
	}
	st = c.State()
	if st.Phase != PhaseIdle || st.Pending != nil || st.Deadline != nil || st.Active.Address != cand.Address {
		t.Fatalf("confirmed state %+v", st)
	}
	if len(applied) != 1 {
		t.Fatalf("confirm must not re-apply, calls = %d", len(applied))
	}
}

func TestCommitTimeoutRollsBack(t *testing.T) {
	clk := newClock()
	var applied []NetworkSettings
	c := NewCommit(settings("dhcp", ""), func(n NetworkSettings) error {
		applied = append(applied, n)
		return nil
	}, clk.Now)

	if err := c.Stage(settings("static", "10.0.0.9/24")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(time.Minute); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Minute + time.Second)
	rolled, err := c.Expire()
	if err != nil || !rolled {
		t.Fatalf("Expire = %v %v", rolled, err)
	}
	st := c.State()
	if st.Phase != PhaseIdle || st.Active.Mode != "dhcp" || st.Pending != nil {
		t.Fatalf("state after rollback %+v", st)
	}
	if len(applied) != 2 || applied[1].Mode != "dhcp" {
		t.Fatalf("rollback must re-apply previous, calls = %+v", applied)
	}
	// A second Expire is a no-op.
	if rolled, err := c.Expire(); rolled || err != nil {
		t.Fatalf("second Expire = %v %v", rolled, err)
	}
}

func TestCommitLateConfirmRollsBack(t *testing.T) {
	clk := newClock()
	c := NewCommit(settings("dhcp", ""), nil, clk.Now)
	_ = c.Stage(settings("static", "10.0.0.9/24"))
	if _, err := c.Apply(time.Minute); err != nil {
		t.Fatal(err)
	}
	clk.Advance(2 * time.Minute)
	if err := c.Confirm(); !errors.Is(err, ErrDeadlinePassed) {
		t.Fatalf("late Confirm err = %v", err)
	}
	if st := c.State(); st.Phase != PhaseIdle || st.Active.Mode != "dhcp" {
		t.Fatalf("state %+v", st)
	}
}

func TestCommitApplyFailureStaysStaged(t *testing.T) {
	clk := newClock()
	boom := errors.New("boom")
	c := NewCommit(settings("dhcp", ""), func(NetworkSettings) error { return boom }, clk.Now)
	_ = c.Stage(settings("static", "10.0.0.9/24"))
	if _, err := c.Apply(time.Minute); !errors.Is(err, ErrApplyFailed) || !errors.Is(err, boom) {
		t.Fatalf("Apply err = %v", err)
	}
	if st := c.State(); st.Phase != PhaseStaged || st.Active.Mode != "dhcp" {
		t.Fatalf("state %+v", st)
	}
	if err := c.Rollback(); err != nil {
		t.Fatal(err)
	}
	if st := c.State(); st.Phase != PhaseIdle || st.Pending != nil {
		t.Fatalf("state after discard %+v", st)
	}
	if err := c.Rollback(); err != nil {
		t.Fatalf("idle Rollback must be a no-op, err = %v", err)
	}
}
