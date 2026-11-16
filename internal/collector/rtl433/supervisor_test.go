package rtl433

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRadioArgs(t *testing.T) {
	r := Radio{ID: "radio0", Serial: "00000433", FrequencyHz: Band433MHz}
	got := strings.Join(r.Args(), " ")
	want := "-d :00000433 -f 433920000 -F json -M level -M protocol -M time:iso:usec:utc:tz"
	if got != want {
		t.Fatalf("args = %q\nwant  %q", got, want)
	}
	noSerial := Radio{FrequencyHz: Band915MHz}
	if got := strings.Join(noSerial.Args(), " "); strings.Contains(got, "-d") || !strings.Contains(got, "-f 915000000") {
		t.Fatalf("no serial args = %q", got)
	}
}

func TestClassifyStderr(t *testing.T) {
	cases := []struct {
		line string
		want error
	}{
		{"No supported devices found.", ErrNoDevice},
		{"Could not find device with serial '00000433' (err -3)", ErrNoDevice},
		{"Async read stalled, exiting!", ErrDeviceLost},
		{"async read failed (-5).", ErrDeviceLost},
		{"LIBUSB_ERROR_IO: Input/Output Error! Check your RTL-SDR dongle, USB cables, and power supply.", ErrUSB},
		{"Found Rafael Micro R820T tuner", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := ClassifyStderr(c.line)
		if c.want == nil {
			if got != nil {
				t.Errorf("%q: got %v want nil", c.line, got)
			}
			continue
		}
		if !errors.Is(got, c.want) {
			t.Errorf("%q: got %v want %v", c.line, got, c.want)
		}
		if !strings.Contains(got.Error(), strings.TrimSpace(c.line)) {
			t.Errorf("%q: message should include the line: %v", c.line, got)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	b := Backoff{Min: time.Second, Max: time.Minute, Factor: 2, Jitter: 0.2}
	if d := b.Delay(0, 0.5); d != time.Second {
		t.Fatalf("attempt 0 = %v", d)
	}
	if d := b.Delay(3, 0.5); d != 8*time.Second {
		t.Fatalf("attempt 3 = %v", d)
	}
	if d := b.Delay(20, 0.5); d != time.Minute {
		t.Fatalf("capped = %v", d)
	}
	lo, hi := b.Delay(2, 0), b.Delay(2, 0.999999)
	if lo < 3200*time.Millisecond-time.Millisecond || hi > 4800*time.Millisecond+time.Millisecond || lo >= hi {
		t.Fatalf("jitter range = %v .. %v", lo, hi)
	}
	if d := (Backoff{}).Delay(0, 0); d < time.Second {
		t.Fatalf("defaults should give at least 1s, got %v", d)
	}
}

// fakeProc is a scripted rtl_433 stand-in. The script writes to stdout and
// stderr pipes and then finishes with an exit error, or blocks until killed.
type fakeProc struct {
	pid      int
	stdoutR  *io.PipeReader
	stdoutW  *io.PipeWriter
	stderrR  *io.PipeReader
	stderrW  *io.PipeWriter
	done     chan struct{}
	once     sync.Once
	exitErr  error
	killed   bool
	killedMu sync.Mutex
}

func newFakeProc(pid int) *fakeProc {
	p := &fakeProc{pid: pid, done: make(chan struct{})}
	p.stdoutR, p.stdoutW = io.Pipe()
	p.stderrR, p.stderrW = io.Pipe()
	return p
}

func (p *fakeProc) Stdout() io.Reader { return p.stdoutR }
func (p *fakeProc) Stderr() io.Reader { return p.stderrR }
func (p *fakeProc) PID() int          { return p.pid }
func (p *fakeProc) Wait() error       { <-p.done; return p.exitErr }
func (p *fakeProc) Kill() error {
	p.killedMu.Lock()
	p.killed = true
	p.killedMu.Unlock()
	p.finish(errors.New("signal: killed"))
	return nil
}
func (p *fakeProc) finish(err error) {
	p.once.Do(func() {
		p.exitErr = err
		_ = p.stdoutW.Close()
		_ = p.stderrW.Close()
		close(p.done)
	})
}

type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func (l *eventLog) add(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) types() []EventType {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]EventType, len(l.events))
	for i, e := range l.events {
		out[i] = e.Type
	}
	return out
}

func (l *eventLog) get(i int) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.events[i]
}

func TestSupervisorRestartsAfterDeviceLoss(t *testing.T) {
	var starts int
	var startsMu sync.Mutex
	var slept []time.Duration
	var sleptMu sync.Mutex
	second := make(chan *fakeProc, 1)

	start := func(_ context.Context, name string, args []string) (Process, error) {
		startsMu.Lock()
		starts++
		n := starts
		startsMu.Unlock()
		if name != "rtl_433" || !strings.Contains(strings.Join(args, " "), "-M level") {
			t.Errorf("unexpected command %s %v", name, args)
		}
		p := newFakeProc(1000 + n)
		switch n {
		case 1:
			go func() {
				_, _ = fmt.Fprintln(p.stdoutW, `{"time" : "2024-05-01T12:00:00Z", "model" : "X", "id" : 1}`)
				_, _ = fmt.Fprintln(p.stdoutW, ``)
				_, _ = fmt.Fprintln(p.stdoutW, `{"time" : "2024-05-01T12:00:01Z", "model" : "X", "id" : 1}`)
				_, _ = fmt.Fprintln(p.stderrW, `rtl_433 version 23.11`)
				_, _ = fmt.Fprintln(p.stderrW, `Async read stalled, exiting!`)
				p.finish(errors.New("exit status 1"))
			}()
		default:
			go func() {
				_, _ = fmt.Fprintln(p.stdoutW, `{"time" : "2024-05-01T12:01:00Z", "model" : "X", "id" : 1}`)
			}()
			second <- p
		}
		return p, nil
	}

	var log eventLog
	s := New(Config{
		Radio:  Radio{ID: "radio0", Serial: "abc", FrequencyHz: Band433MHz},
		Start:  start,
		Events: log.add,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff: Backoff{
			Min: time.Second, Max: time.Minute, Factor: 2, Jitter: 0, ResetAfter: time.Hour,
		},
		Sleep: func(_ context.Context, d time.Duration) error {
			sleptMu.Lock()
			slept = append(slept, d)
			sleptMu.Unlock()
			return nil
		},
		Rand: func() float64 { return 0.5 },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	var lines []Line
	timeout := time.After(5 * time.Second)
	for len(lines) < 3 {
		select {
		case l := <-s.Lines():
			lines = append(lines, l)
		case <-timeout:
			t.Fatalf("got %d lines before timeout", len(lines))
		}
	}
	if lines[0].Radio != "radio0" || !strings.Contains(string(lines[0].Text), `"id" : 1`) {
		t.Fatalf("line 0 = %+v", lines[0])
	}

	// The second process is now running. Check status and events.
	p2 := <-second
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := s.Status()
		if st.Running && st.PID == p2.pid {
			if st.Restarts != 1 {
				t.Fatalf("restarts = %d", st.Restarts)
			}
			if st.PacketsLast15m != 3 {
				t.Fatalf("packets = %d", st.PacketsLast15m)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("second process never reported running: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}

	types := log.types()
	wantTypes := []EventType{EventStarted, EventExited, EventRestartScheduled, EventStarted}
	if len(types) != len(wantTypes) {
		t.Fatalf("events = %v", types)
	}
	for i := range wantTypes {
		if types[i] != wantTypes[i] {
			t.Fatalf("events = %v want %v", types, wantTypes)
		}
	}
	exited := log.get(1)
	if !errors.Is(exited.Err, ErrDeviceLost) {
		t.Fatalf("exit reason = %v", exited.Err)
	}
	if log.get(2).Backoff != time.Second {
		t.Fatalf("backoff = %v", log.get(2).Backoff)
	}
	sleptMu.Lock()
	if len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("slept = %v", slept)
	}
	sleptMu.Unlock()

	cancel()
	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	p2.killedMu.Lock()
	killed := p2.killed
	p2.killedMu.Unlock()
	if !killed {
		t.Fatal("process should be killed on cancel")
	}
	if _, open := <-s.Lines(); open {
		t.Fatal("Lines should be closed after Run returns")
	}
}

func TestSupervisorStartFailureBacksOff(t *testing.T) {
	var log eventLog
	var slept []time.Duration
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(Config{
		Radio:  Radio{ID: "radio0"},
		Binary: "rtl_433",
		Start: func(_ context.Context, _ string, _ []string) (Process, error) {
			return nil, errors.New("exec: not found")
		},
		Events: log.add,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff: Backoff{
			Min: time.Second, Max: 4 * time.Second, Factor: 2, Jitter: 0, ResetAfter: time.Hour,
		},
		Sleep: func(ctx context.Context, d time.Duration) error {
			mu.Lock()
			slept = append(slept, d)
			n := len(slept)
			mu.Unlock()
			if n >= 4 {
				cancel()
				return ctx.Err()
			}
			return nil
		},
		Rand: func() float64 { return 0.5 },
	})
	if err := s.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("run err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	if len(slept) != len(want) {
		t.Fatalf("slept = %v", slept)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("slept = %v want %v", slept, want)
		}
	}
	st := s.Status()
	if st.Running || st.Restarts != 4 || !strings.Contains(st.LastError, "not found") {
		t.Fatalf("status = %+v", st)
	}
	if log.get(0).Type != EventStartFailed {
		t.Fatalf("first event = %v", log.get(0).Type)
	}
}

func TestMinuteWindow(t *testing.T) {
	w := newMinuteWindow(15)
	base := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		w.add(base.Add(time.Duration(i) * time.Minute))
	}
	now := base.Add(19 * time.Minute)
	if got := w.count(now); got != 15 {
		t.Fatalf("count = %d", got)
	}
	if len(w.counts) != 15 {
		t.Fatalf("stale minutes not pruned: %d", len(w.counts))
	}
}
