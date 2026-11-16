package rtl433

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const replayFixture = `# comment line
{"time" : "2024-05-01T12:00:00.000000Z", "model" : "X", "id" : 1, "temperature_C" : 20.0}

{"time" : "2024-05-01T12:00:18.000000Z", "model" : "X", "id" : 1, "temperature_C" : 20.1}
{"time" : "2024-05-01T12:00:36.000000Z", "model" : "X", "id" : 1, "temperature_C" : 20.2}
{"model" : "NoTime", "id" : 2}
`

func collect(t *testing.T, r *Replay, src string) []Line {
	t.Helper()
	out := make(chan Line, 16)
	if err := r.Run(context.Background(), strings.NewReader(src), out); err != nil {
		t.Fatal(err)
	}
	close(out)
	var lines []Line
	for l := range out {
		lines = append(lines, l)
	}
	return lines
}

func TestReplayMaxSpeed(t *testing.T) {
	slept := 0
	r := &Replay{Radio: "replay", Sleep: func(_ context.Context, _ time.Duration) error {
		slept++
		return nil
	}}
	lines := collect(t, r, replayFixture)
	if len(lines) != 4 {
		t.Fatalf("got %d lines", len(lines))
	}
	if slept != 0 {
		t.Fatalf("slept %d times at max speed", slept)
	}
	if lines[0].Radio != "replay" {
		t.Fatalf("radio = %q", lines[0].Radio)
	}
	want := time.Date(2024, 5, 1, 12, 0, 18, 0, time.UTC)
	if !lines[1].At.Equal(want) {
		t.Fatalf("line 1 at = %v", lines[1].At)
	}
	if !strings.Contains(string(lines[1].Text), `"2024-05-01T12:00:18.000000Z"`) {
		t.Fatalf("timestamp rewritten without RewriteTime: %s", lines[1].Text)
	}
}

func TestReplayPaced(t *testing.T) {
	var slept []time.Duration
	r := &Replay{Speed: 10, Sleep: func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}}
	collect(t, r, replayFixture)
	if len(slept) != 2 {
		t.Fatalf("slept = %v", slept)
	}
	for _, d := range slept {
		if d != 1800*time.Millisecond {
			t.Fatalf("slept = %v want 1.8s each", slept)
		}
	}
}

func TestReplayRewriteTime(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	r := &Replay{RewriteTime: true, Now: func() time.Time { return now }}
	lines := collect(t, r, replayFixture)
	if !lines[0].At.Equal(now) {
		t.Fatalf("first line at %v want %v", lines[0].At, now)
	}
	if !lines[2].At.Equal(now.Add(36 * time.Second)) {
		t.Fatalf("third line at %v", lines[2].At)
	}
	var d Decoder
	msg, err := d.Decode(lines[2].Text)
	if err != nil {
		t.Fatal(err)
	}
	if !msg.HasTime || !msg.Time.Equal(now.Add(36*time.Second)) {
		t.Fatalf("rewritten text time = %v (%s)", msg.Time, lines[2].Text)
	}
	if v, _ := msg.Float("temperature_C"); v != 20.2 {
		t.Fatalf("payload damaged by rewrite: %s", lines[2].Text)
	}
	// Lines without a timestamp are stamped with now.
	if !lines[3].At.Equal(now) {
		t.Fatalf("untimed line at %v", lines[3].At)
	}
}

func TestReplayFileAndReadLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jsonl")
	if err := os.WriteFile(path, []byte(replayFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadLines(path)
	if err != nil || len(lines) != 4 {
		t.Fatalf("ReadLines = %d %v", len(lines), err)
	}
	out := make(chan Line, 16)
	if err := (&Replay{}).RunFile(context.Background(), path, out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("RunFile emitted %d", len(out))
	}
	if err := (&Replay{}).RunFile(context.Background(), filepath.Join(dir, "missing"), out); err == nil {
		t.Fatal("missing file should error")
	}
}

func TestReplayStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := make(chan Line) // unbuffered, nobody reads
	err := (&Replay{}).Run(ctx, strings.NewReader(replayFixture), out)
	if err == nil {
		t.Fatal("expected context error")
	}
}
