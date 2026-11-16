package main

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/collector"
	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
)

var simStart = time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)

func TestOutputFeedsThePipeline(t *testing.T) {
	cfg := config{Stations: 4, Start: simStart, Duration: 10 * time.Minute, Seed: 3}
	var buf bytes.Buffer
	if err := emit(bufio.NewWriter(&buf), generate(cfg), cfg.Start, 0, nil); err != nil {
		t.Fatal(err)
	}
	p := collector.New(collector.Config{TickEvery: -1})
	var last time.Time
	n := 0
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		msg, err := (&rtl433.Decoder{Location: time.UTC}).Decode([]byte(l))
		if err != nil {
			t.Fatalf("%v: %s", err, l)
		}
		if msg.Time.Before(last) {
			t.Fatalf("output not in time order at %s", l)
		}
		last = msg.Time
		p.Ingest(context.Background(), rtl433.Line{Radio: "sim", At: msg.Time, Text: []byte(l)})
		n++
	}
	st := p.Status()
	if st.DecodeErrors != 0 || st.Rejected != 0 || st.Stations != 4 {
		t.Fatalf("%d lines, status %+v", n, st)
	}
}

func TestSpeedPacesByEventGaps(t *testing.T) {
	cfg := config{Stations: 1, Start: simStart, Duration: time.Minute, Seed: 1}
	evs := generate(cfg)
	var slept time.Duration
	var buf bytes.Buffer
	if err := emit(bufio.NewWriter(&buf), evs, cfg.Start, 10, func(d time.Duration) { slept += d }); err != nil {
		t.Fatal(err)
	}
	want := evs[len(evs)-1].at.Sub(cfg.Start) / 10
	if slept < want-time.Millisecond || slept > want+time.Millisecond {
		t.Fatalf("slept %v, want %v", slept, want)
	}
}

func TestSameSeedSameOutput(t *testing.T) {
	cfg := config{Stations: 2, Start: simStart, Duration: 5 * time.Minute, Seed: 9}
	a, b := generate(cfg), generate(cfg)
	if len(a) != len(b) || a[10].line != b[10].line {
		t.Fatal("generate is not deterministic")
	}
}
