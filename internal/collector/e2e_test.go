package collector

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/aggregate"
	"github.com/wxalerts/pws-gateway/internal/collector/mapping"
	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/model"
)

const (
	realCapture = "../../testdata/real/acurite-433-20261116.jsonl"
	keyAcu5n1   = "Acurite-5n1:4026:A"
	keyNeigh    = "Acurite-5n1:2710:A"
	key609      = "Acurite-609TXC:224:"
)

// capLine is the subset of a recorded line the tests recompute from.
type capLine struct {
	Time    string  `json:"time"`
	Model   string  `json:"model"`
	ID      any     `json:"id"`
	Channel string  `json:"channel"`
	MsgType int     `json:"message_type"`
	TempF   float64 `json:"temperature_F"`
	TempC   float64 `json:"temperature_C"`
	WindDir float64 `json:"wind_dir_deg"`
	RainIn  float64 `json:"rain_in"`
	at      time.Time
	key     string
}

func loadCapture(t *testing.T, path string) []capLine {
	t.Helper()
	raw, err := rtl433.ReadLines(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []capLine
	for _, l := range raw {
		var c capLine
		if err := json.Unmarshal(l, &c); err != nil {
			t.Fatalf("%s: %v", l, err)
		}
		ts, err := rtl433.ParseTime(c.Time, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		c.at = ts
		id, _ := json.Marshal(c.ID)
		c.key = model.StationKey(c.Model, strings.Trim(string(id), `"`), c.Channel)
		out = append(out, c)
	}
	return out
}

// bursts groups a station's lines into transmissions: lines within the
// dedupe window of the first line of a burst are repeats of it.
func bursts(lines []capLine, key string) []capLine {
	var out []capLine
	for i := range lines {
		l := &lines[i]
		if l.key != key {
			continue
		}
		if n := len(out); n > 0 && l.at.Sub(out[n-1].at) < DefaultDedupWindow {
			continue
		}
		out = append(out, *l)
	}
	return out
}

func foundAt(h *harness, key string) (Event, bool) {
	for _, e := range h.eventsOf(EventSensorFound) {
		if e.Station != nil && e.Station.Key == key {
			return e, true
		}
	}
	return Event{}, false
}

func stationKeys(h *harness) []string {
	var keys []string
	sts := h.p.Stations(time.Now())
	for i := range sts {
		keys = append(keys, sts[i].Key)
	}
	return keys
}

// TestRealCaptureDiscovery replays 25 minutes recorded from a Pi 4 at full
// speed and checks the discovery rule against times computed from the file.
//
// Each Acurite transmission arrives as three identical lines, so with the
// default repeat suppression the third consistent sighting is the third
// distinct transmission. The neighbor station 2710 was heard in five
// transmissions, so the rule lists it (it is a real, weak station).
func TestRealCaptureDiscovery(t *testing.T) {
	lines := loadCapture(t, realCapture)
	h := newHarness(t, nil)
	h.replayFile(t, realCapture)

	for key, want := range map[string]int{keyAcu5n1: 3, key609: 3, keyNeigh: 3} {
		ev, ok := foundAt(h, key)
		if !ok {
			t.Fatalf("%s was not discovered; stations: %v", key, stationKeys(h))
		}
		b := bursts(lines, key)
		if !ev.At.Equal(b[want-1].at) {
			t.Errorf("%s found at %v, want third transmission at %v", key, ev.At, b[want-1].at)
		}
		if ev.Station.Sightings != want {
			t.Errorf("%s sightings at discovery = %d, want %d", key, ev.Station.Sightings, want)
		}
	}
	if got := len(h.eventsOf(EventSensorFound)); got != 3 {
		t.Errorf("found events = %d, want 3 (4026, 609TXC, neighbor 2710); stations %v", got, stationKeys(h))
	}
	for _, k := range stationKeys(h) {
		if strings.HasPrefix(k, "Hyundai-VDO") {
			t.Errorf("TPMS became a station: %s", k)
		}
	}
	st := h.p.Status()
	if st.Readings != int64(len(lines)) || st.DecodeErrors != 0 {
		t.Errorf("status = %+v for %d lines", st, len(lines))
	}
	if st.Duplicates == 0 {
		t.Error("expected repeats to be suppressed")
	}
	// The real capture is the 22.11 default: zone-less UTC timestamps.
	if first := h.p.Readings(keyAcu5n1, time.Time{}); len(first) == 0 || first[0].Time.Hour() != 16 {
		t.Errorf("recent readings not stamped from recorded UTC times: %v", first)
	}
}

// TestRealCaptureDiscoveryWithoutRepeatSuppression documents what the rule
// does when every line counts: one transmission (three identical lines) is
// enough, which is why the pipeline suppresses repeats by default.
func TestRealCaptureDiscoveryWithoutRepeatSuppression(t *testing.T) {
	lines := loadCapture(t, realCapture)
	h := newHarness(t, func(c *Config) { c.DedupWindow = -1 })
	h.replayFile(t, realCapture)

	ev, ok := foundAt(h, keyAcu5n1)
	if !ok {
		t.Fatal("4026 not discovered")
	}
	var own []capLine
	for _, l := range lines {
		if l.key == keyAcu5n1 {
			own = append(own, l)
		}
	}
	if !ev.At.Equal(own[2].at) || !own[0].at.Equal(own[2].at) {
		t.Errorf("found at %v, want third line at %v (same transmission as the first)", ev.At, own[2].at)
	}
	if ev.Station.Sightings != 3 {
		t.Errorf("sightings = %d", ev.Station.Sightings)
	}
	if h.p.Status().Duplicates != 0 {
		t.Error("suppression was disabled")
	}
}

func TestRealCaptureBuckets(t *testing.T) {
	lines := loadCapture(t, realCapture)
	h := newHarness(t, nil)
	h.replayFile(t, realCapture)

	ctx := context.Background()
	from := time.Date(2026, 11, 16, 16, 0, 0, 0, time.UTC)
	bs, err := h.store.Rollups(ctx, keyAcu5n1, from, from.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) < 3 {
		t.Fatalf("buckets = %d, want at least 3", len(bs))
	}

	// Independent expectations from the recording, one value per
	// transmission (repeats collapsed).
	type acc struct {
		temps []float64
		dirs  []float64
	}
	want := map[time.Time]*acc{}
	minC, maxC := math.Inf(1), math.Inf(-1)
	for _, l := range bursts(lines, keyAcu5n1) {
		a := want[aggregate.Align(l.at, 5*time.Minute)]
		if a == nil {
			a = &acc{}
			want[aggregate.Align(l.at, 5*time.Minute)] = a
		}
		switch l.MsgType {
		case 56:
			c := (l.TempF - 32) * 5 / 9
			a.temps = append(a.temps, c)
			minC, maxC = math.Min(minC, c), math.Max(maxC, c)
		case 49:
			a.dirs = append(a.dirs, l.WindDir)
		}
	}
	var rainLast float64
	for _, l := range lines {
		if l.key == keyAcu5n1 && l.MsgType == 49 {
			rainLast = l.RainIn * 25.4
		}
	}

	for _, b := range bs {
		a := want[b.Start]
		if a == nil {
			t.Fatalf("unexpected bucket %v", b.Start)
		}
		temp, ok := b.Fields[model.FieldTempC]
		if !ok {
			t.Fatalf("bucket %v has no temp_c", b.Start)
		}
		if temp.Avg < minC || temp.Avg > maxC {
			t.Errorf("%v temp avg %.3f outside capture range [%.3f, %.3f]", b.Start, temp.Avg, minC, maxC)
		}
		if temp.N != len(a.temps) || math.Abs(temp.Avg-mean(a.temps)) > 1e-6 {
			t.Errorf("%v temp n=%d avg=%.4f, want n=%d avg=%.4f", b.Start, temp.N, temp.Avg, len(a.temps), mean(a.temps))
		}

		dir := b.Fields[model.FieldWindDirDeg]
		vm := vectorMean(a.dirs)
		if dir.N != len(a.dirs) || angleDiff(dir.Avg, vm) > 1e-6 {
			t.Errorf("%v wind dir n=%d avg=%.4f, want vector mean %.4f over %d", b.Start, dir.N, dir.Avg, vm, len(a.dirs))
		}
		if dir.Vec <= 0 || dir.Vec > 1 {
			t.Errorf("%v wind dir vec = %v", b.Start, dir.Vec)
		}

		rain := b.Fields[model.FieldRainMM]
		if rain.Kind != model.KindCounter {
			t.Errorf("%v rain kind = %v", b.Start, rain.Kind)
		}
		// The gauge never moved in the capture, so every delta is zero and
		// the raw counter is only visible under Last. A gauge style average
		// would have reported the counter value itself.
		if rain.Sum != 0 || math.Abs(rain.Last-rainLast) > 1e-6 {
			t.Errorf("%v rain sum=%v last=%v, want sum 0 last %v", b.Start, rain.Sum, rain.Last, rainLast)
		}
	}
}

// TestVectorMeanAcrossNorth checks the test helper itself: directions on
// either side of north average to north, not to south.
func TestVectorMeanAcrossNorth(t *testing.T) {
	if got := vectorMean([]float64{350, 10}); angleDiff(got, 0) > 1e-9 {
		t.Fatalf("vectorMean helper = %v", got)
	}
}

func TestMappingOfEveryRealLine(t *testing.T) {
	raw, err := rtl433.ReadLines(realCapture)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]bool{"time": true, "model": true, "id": true, "channel": true, "protocol": true,
		"mod": true, "freq": true, "freq1": true, "freq2": true, "rssi": true, "snr": true, "noise": true}
	ruleTarget := map[string]string{}
	for _, r := range mapping.DefaultRules {
		if _, ok := ruleTarget[r.Source]; !ok {
			ruleTarget[r.Source] = r.Field
		}
	}
	dec := rtl433.Decoder{Location: time.UTC}
	m := mapping.New()
	rawKeys := map[string]map[string]bool{}
	for i, l := range raw {
		msg, err := dec.Decode(l)
		if err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		r := m.Map(msg, "radio0")
		if r.Model != msg.Model || r.ID == "" {
			t.Fatalf("line %d: identity %q %q", i+1, r.Model, r.ID)
		}
		if rawKeys[r.Model] == nil {
			rawKeys[r.Model] = map[string]bool{}
		}
		for k := range msg.Data {
			if _, inRaw := r.Raw[k]; inRaw {
				rawKeys[r.Model][k] = true
				continue
			}
			if metadata[k] {
				continue
			}
			field, ok := ruleTarget[k]
			_, isField := r.Fields[field]
			_, isFlag := r.Flags[field]
			if !ok || (!isField && !isFlag) {
				t.Errorf("line %d: key %q is neither canonical nor in raw", i+1, k)
			}
		}
		if !r.Signal.HasLevel {
			t.Errorf("line %d: -M level data not parsed", i+1)
		}
	}
	for model, set := range rawKeys {
		var ks []string
		for k := range set {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		t.Logf("%s raw passthrough: %v", model, ks)
	}
}

func TestCounterResetFixture(t *testing.T) {
	h := newHarness(t, nil)
	h.replayFile(t, "../../testdata/synthetic/counter-reset.jsonl")
	const key = "Fineoffset-WH65B:31:"
	if _, ok := foundAt(h, key); !ok {
		t.Fatalf("station not discovered: %v", stationKeys(h))
	}
	st := h.p.Status()
	if st.Rejected != 0 {
		t.Errorf("a counter wrap must not be rejected by discovery: %+v", st)
	}
	if st.Aggregate.CounterResets != 1 {
		t.Errorf("counter resets = %d, want 1", st.Aggregate.CounterResets)
	}
	from := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	bs, err := h.store.Rollups(context.Background(), key, from, from.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, b := range bs {
		sum += b.Fields[model.FieldRainMM].Sum
	}
	// Tips of 0.3 mm: five before the wrap (19658.4 to 19659.9), then the
	// wrapped value 0.3 counts as a fresh 0.3 mm, then one more tip.
	if math.Abs(sum-2.1) > 1e-6 {
		t.Errorf("total rain delta = %.4f, want 2.1", sum)
	}
	if sum > 100 {
		t.Errorf("wrap produced a huge delta: %v", sum)
	}
}

func TestNoiseFixtureYieldsNoStations(t *testing.T) {
	h := newHarness(t, nil)
	h.replayFile(t, "../../testdata/synthetic/noise-false-positives.jsonl")
	if got := stationKeys(h); len(got) != 0 {
		t.Fatalf("noise became stations: %v", got)
	}
	if n := len(h.eventsOf(EventSensorFound)); n != 0 {
		t.Fatalf("found events = %d", n)
	}
	if st := h.p.Status(); st.Rejected == 0 || st.Accepted != 0 {
		t.Fatalf("status = %+v", st)
	}
}

func TestSyntheticStationsAreDiscovered(t *testing.T) {
	for file, key := range map[string]string{
		"fineoffset-wh65b.jsonl":    "Fineoffset-WH65B:77:",
		"lacrosse-tx141thbv2.jsonl": "LaCrosse-TX141THBv2:188:1",
		"counter-reset.jsonl":       "Fineoffset-WH65B:31:",
	} {
		h := newHarness(t, nil)
		h.replayFile(t, "../../testdata/synthetic/"+file)
		if _, ok := foundAt(h, key); !ok {
			t.Errorf("%s: %s not discovered: %v", file, key, stationKeys(h))
		}
		if st := h.p.Status(); st.DecodeErrors != 0 || st.Rejected != 0 {
			t.Errorf("%s: status %+v", file, st)
		}
	}
}

func TestStationsSurviveRestart(t *testing.T) {
	h := newHarness(t, nil)
	h.replayFile(t, realCapture)
	saved, err := h.store.Stations(context.Background())
	if err != nil || len(saved) != 3 {
		t.Fatalf("saved stations = %d %v", len(saved), err)
	}
	p2 := New(Config{Store: h.store, Writer: h.writer, TickEvery: -1})
	empty := make(chan rtl433.Line)
	close(empty)
	if err := p2.Run(context.Background(), ChanSource(empty)); err != nil {
		t.Fatal(err)
	}
	got := p2.Stations(time.Now())
	if len(got) != 3 {
		t.Fatalf("restored stations = %d", len(got))
	}
	for _, s := range got {
		if !s.Lost {
			t.Errorf("%s should be lost until heard", s.Key)
		}
	}
}

func mean(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func vectorMean(deg []float64) float64 {
	var sx, sy float64
	for _, d := range deg {
		sx += math.Cos(d * math.Pi / 180)
		sy += math.Sin(d * math.Pi / 180)
	}
	return aggregate.VectorMean(sx, sy)
}

func angleDiff(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	return math.Min(d, 360-d)
}
