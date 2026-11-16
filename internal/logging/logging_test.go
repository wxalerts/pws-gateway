package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":        slog.LevelInfo,
		"DEBUG":   slog.LevelDebug,
		" warn ":  slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"bogus":   slog.LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRingWrapsAndTails(t *testing.T) {
	r := NewRing(3)
	for _, l := range []string{"a", "b", "c", "d"} {
		if _, err := r.Write([]byte(l + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	if got := strings.Join(r.Tail(0), ","); got != "b,c,d" {
		t.Fatalf("Tail(0) = %q", got)
	}
	if got := strings.Join(r.Tail(2), ","); got != "c,d" {
		t.Fatalf("Tail(2) = %q", got)
	}
	if got := strings.Join(r.Tail(10), ","); got != "b,c,d" {
		t.Fatalf("Tail(10) = %q", got)
	}
}

func TestRingHoldsPartialLines(t *testing.T) {
	r := NewRing(4)
	_, _ = r.Write([]byte("hel"))
	_, _ = r.Write([]byte("lo\nwor"))
	if got := r.Tail(0); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("Tail = %v", got)
	}
	_, _ = r.Write([]byte("ld\n"))
	if got := r.Tail(0); len(got) != 2 || got[1] != "world" {
		t.Fatalf("Tail = %v", got)
	}
}

func TestNewWritesJSONAndRing(t *testing.T) {
	var out bytes.Buffer
	log, ring := New(Options{Level: slog.LevelInfo, Format: "json", Output: &out, RingLines: 10})
	log.Debug("hidden")
	log.Info("visible", "k", "v")

	var rec map[string]any
	if err := json.Unmarshal(out.Bytes(), &rec); err != nil {
		t.Fatalf("primary output is not JSON: %v: %q", err, out.String())
	}
	if rec["msg"] != "visible" || rec["k"] != "v" {
		t.Fatalf("unexpected record %v", rec)
	}
	tail := ring.Tail(0)
	if len(tail) != 1 || !strings.Contains(tail[0], "msg=visible") {
		t.Fatalf("ring tail = %v", tail)
	}
}

func TestMultiPropagatesAttrsAndGroups(t *testing.T) {
	var a, b bytes.Buffer
	h := Multi(
		slog.NewTextHandler(&a, &slog.HandlerOptions{Level: slog.LevelInfo}),
		slog.NewTextHandler(&b, &slog.HandlerOptions{Level: slog.LevelInfo}),
	)
	log := slog.New(h).With("svc", "x").WithGroup("g")
	log.Info("m", "n", 1)
	for name, buf := range map[string]*bytes.Buffer{"a": &a, "b": &b} {
		s := buf.String()
		if !strings.Contains(s, "svc=x") || !strings.Contains(s, "g.n=1") {
			t.Errorf("%s: %q", name, s)
		}
	}
}
