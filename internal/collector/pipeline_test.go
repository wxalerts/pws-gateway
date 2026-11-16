package collector

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/store"
)

// harness wires a pipeline to a temp store and records events.
type harness struct {
	p      *Pipeline
	store  *store.Store
	writer *store.Writer

	mu     sync.Mutex
	events []Event
}

func (h *harness) eventsOf(t EventType) []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Event
	for _, e := range h.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

func newHarness(t *testing.T, mod func(*Config)) *harness {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "pws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	w := store.NewWriter(s, store.WriterOptions{AllowShortInterval: true})
	h := &harness{store: s, writer: w}
	cfg := Config{
		Store:     s,
		Writer:    w,
		TickEvery: -1,
		Events: func(e Event) {
			h.mu.Lock()
			h.events = append(h.events, e)
			h.mu.Unlock()
		},
	}
	if mod != nil {
		mod(&cfg)
	}
	h.p = New(cfg)
	return h
}

// replayFile pushes a recording through the pipeline at maximum speed.
func (h *harness) replayFile(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	src := NewReplayFile(ctx, &rtl433.Replay{Radio: "radio0", Location: time.UTC}, path)
	if err := h.p.Run(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := src.Err(); err != nil {
		t.Fatal(err)
	}
}

const wh65bLine = `{"time" : "2026-11-17T09:00:00.215004", "protocol" : 78, "model" : "Fineoffset-WH65B", "id" : 77, "battery_ok" : 1, "temperature_C" : 18.4, "humidity" : 61, "wind_dir_deg" : 200, "wind_avg_m_s" : 2.1, "wind_max_m_s" : 3.0, "rain_mm" : 12.6, "mic" : "CRC", "mod" : "FSK", "freq" : 433.92, "rssi" : -9.0, "snr" : 5.0, "noise" : -14.0}`

func TestHoldAndReleaseOnSync(t *testing.T) {
	var synced bool
	var mono time.Duration
	wall := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	h := newHarness(t, func(c *Config) {
		c.TimeSynced = func() bool { return synced }
		c.Now = func() time.Time { return wall }
		c.Mono = func() time.Duration { return mono }
		c.HoldLimit = 10 * time.Minute
	})
	ctx := context.Background()
	// 50 lines, 16 s apart: the first (49*16s = 13m04s old at sync) are
	// beyond the 10 minute limit.
	for i := 0; i < 50; i++ {
		h.p.Ingest(ctx, rtl433.Line{Radio: "r", At: time.Date(1970, 1, 1, 0, 0, i, 0, time.UTC), Text: []byte(wh65bLine)})
		mono += 16 * time.Second
	}
	// The last line was ingested at mono 49*16s and sync arrives 16 s after.
	if st := h.p.Status(); st.Held != 50 || st.Synced || st.Stations != 0 {
		t.Fatalf("while unsynced: %+v", st)
	}
	if got := h.p.Stations(wall); len(got) != 0 {
		t.Fatalf("stations before sync: %v", got)
	}
	synced = true
	h.p.Tick(ctx, wall)

	st := h.p.Status()
	// Age of line i is (50-i)*16s; under 10 min means 50-i <= 37 (592 s),
	// so lines 13..49 are released (37) and 13 expire.
	if st.HoldExpired != 13 || st.Held != 0 || !st.Synced {
		t.Fatalf("after sync: %+v", st)
	}
	if ev := h.eventsOf(EventHoldExpired); len(ev) != 1 || ev[0].Count != 13 {
		t.Fatalf("hold_expired events = %+v", ev)
	}
	if ev := h.eventsOf(EventTimeSynced); len(ev) != 1 || ev[0].Count != 37 {
		t.Fatalf("time_synced events = %+v", ev)
	}
	// Identical payloads are repeats only when closer than the dedupe
	// window; 16 s apart they are separate sightings, so the station is
	// found, stamped from the sync point.
	found := h.eventsOf(EventSensorFound)
	if len(found) != 1 {
		t.Fatalf("found events = %+v", found)
	}
	wantFirst := wall.Add(-37 * 16 * time.Second)
	if want := wantFirst.Add(2 * 16 * time.Second); !found[0].At.Equal(want) {
		t.Fatalf("found at %v, want %v (third released reading)", found[0].At, want)
	}
	rs := h.p.Readings("Fineoffset-WH65B:77:", time.Time{})
	if len(rs) != 37 || !rs[0].Time.Equal(wantFirst) || !rs[36].Time.Equal(wall.Add(-1*16*time.Second)) {
		t.Fatalf("readings = %d first=%v last=%v", len(rs), rs[0].Time, rs[len(rs)-1].Time)
	}
	// Later lines are stamped from their receipt time.
	at := wall.Add(time.Minute)
	h.p.Ingest(ctx, rtl433.Line{Radio: "r", At: at, Text: []byte(wh65bLine)})
	rs = h.p.Readings("Fineoffset-WH65B:77:", at)
	if len(rs) != 1 || !rs[0].Time.Equal(at) {
		t.Fatalf("post sync readings = %v", rs)
	}
}

func TestHoldRingOverflowDropsOldest(t *testing.T) {
	var mono time.Duration
	h := newHarness(t, func(c *Config) {
		c.TimeSynced = func() bool { return false }
		c.Mono = func() time.Duration { return mono }
		c.HoldMax = 5
	})
	for i := 0; i < 8; i++ {
		h.p.Ingest(context.Background(), rtl433.Line{Radio: "r", Text: []byte(wh65bLine)})
		mono += time.Second
	}
	st := h.p.Status()
	if st.Held != 5 || st.HoldOverflow != 3 {
		t.Fatalf("status = %+v", st)
	}
	if ev := h.eventsOf(EventHoldOverflow); len(ev) != 1 {
		t.Fatalf("overflow events = %+v", ev)
	}
}

func TestUndecodableLinesAreCounted(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	for _, l := range []string{"", "rtl_433 version 22.11", `{"no":"model"}`, "{broken"} {
		h.p.Ingest(ctx, rtl433.Line{Radio: "r", At: time.Now(), Text: []byte(l)})
	}
	if st := h.p.Status(); st.DecodeErrors != 3 || st.Lines != 3 || st.Readings != 0 {
		t.Fatalf("status = %+v", st)
	}
}
