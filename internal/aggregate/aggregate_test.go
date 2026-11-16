package aggregate

import (
	"math"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

var t0 = time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)

func reading(at time.Time, fields map[string]float64) model.Reading {
	return model.Reading{Model: "X", ID: "1", Time: at, Fields: fields}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestAlignAndNextBoundary(t *testing.T) {
	at := t0.Add(7*time.Minute + 13*time.Second)
	if got := Align(at, DefaultWidth); !got.Equal(t0.Add(5 * time.Minute)) {
		t.Fatalf("align = %v", got)
	}
	if got := NextBoundary(at, DefaultWidth); !got.Equal(t0.Add(10 * time.Minute)) {
		t.Fatalf("next = %v", got)
	}
	if got := NextBoundary(t0, DefaultWidth); !got.Equal(t0.Add(5 * time.Minute)) {
		t.Fatalf("next on boundary = %v", got)
	}
}

func TestGaugeAvgMinMax(t *testing.T) {
	b := New()
	for i, v := range []float64{10, 20, 30} {
		b.Add(reading(t0.Add(time.Duration(i)*time.Minute), map[string]float64{model.FieldTempC: v}))
	}
	if got := b.Flush(t0.Add(4 * time.Minute)); len(got) != 0 {
		t.Fatalf("bucket flushed before its end: %v", got)
	}
	if got := b.Flush(t0.Add(5 * time.Minute)); len(got) != 0 {
		t.Fatalf("bucket flushed inside grace period: %v", got)
	}
	got := b.Flush(t0.Add(5*time.Minute + 3*time.Second))
	if len(got) != 1 {
		t.Fatalf("buckets = %d", len(got))
	}
	bk := got[0]
	if !bk.Start.Equal(t0) || bk.StationKey != "X:1:" || bk.N != 3 {
		t.Fatalf("bucket = %+v", bk)
	}
	a := bk.Fields[model.FieldTempC]
	if a.Kind != model.KindGauge || a.N != 3 || a.Avg != 20 || a.Min != 10 || a.Max != 30 || a.Sum != 60 {
		t.Fatalf("agg = %+v", a)
	}
	if b.Stats().OpenBuckets != 0 {
		t.Fatal("bucket should be removed after flush")
	}
}

func TestDirectionVectorMean(t *testing.T) {
	b := New()
	for i, v := range []float64{350, 10} {
		b.Add(reading(t0.Add(time.Duration(i)*time.Second), map[string]float64{model.FieldWindDirDeg: v}))
	}
	a := b.FlushAll()[0].Fields[model.FieldWindDirDeg]
	if a.Kind != model.KindDirection || a.N != 2 {
		t.Fatalf("agg = %+v", a)
	}
	if !near(a.Avg, 0) && !near(a.Avg, 360) {
		t.Fatalf("mean of 350 and 10 = %v", a.Avg)
	}
	if a.Avg >= 360 {
		t.Fatalf("mean not normalised: %v", a.Avg)
	}
	if !near(a.Vec, math.Cos(10*math.Pi/180)) {
		t.Fatalf("vec = %v", a.Vec)
	}

	b = New()
	for i, v := range []float64{90, 180} {
		b.Add(reading(t0.Add(time.Duration(i)*time.Second), map[string]float64{model.FieldWindDirDeg: v}))
	}
	a = b.FlushAll()[0].Fields[model.FieldWindDirDeg]
	if !near(a.Avg, 135) {
		t.Fatalf("mean of 90 and 180 = %v", a.Avg)
	}
	if VectorMean(0, 0) != 0 {
		t.Fatal("canceled vectors should give 0")
	}
	if v := VectorMean(1, -1e-9); v < 0 || v >= 360 {
		t.Fatalf("near north negative = %v", v)
	}
}

func TestCounterDeltaAndReset(t0ff *testing.T) {
	t := t0ff
	b := New()
	values := []float64{100.0, 100.5, 101.5, 0.3, 0.8}
	for i, v := range values {
		b.Add(reading(t0.Add(time.Duration(i)*time.Second), map[string]float64{model.FieldRainMM: v}))
	}
	a := b.FlushAll()[0].Fields[model.FieldRainMM]
	if a.Kind != model.KindCounter {
		t.Fatalf("kind = %v", a.Kind)
	}
	// deltas: 0.5, 1.0, reset -> 0.3, 0.5 = 2.3 over 4 deltas
	if a.N != 4 || !near(a.Sum, 2.3) {
		t.Fatalf("counter = %+v", a)
	}
	if a.Last != 0.8 || a.Min != 0.3 || a.Max != 101.5 {
		t.Fatalf("counter raw range = %+v", a)
	}
	if b.Stats().CounterResets != 1 {
		t.Fatalf("resets = %d", b.Stats().CounterResets)
	}
}

func TestCounterStateCrossesBuckets(t *testing.T) {
	b := New()
	b.Add(reading(t0.Add(4*time.Minute), map[string]float64{model.FieldRainMM: 10}))
	first := b.Flush(t0.Add(5*time.Minute + 5*time.Second))
	if len(first) != 1 || first[0].Fields[model.FieldRainMM].N != 0 || first[0].Fields[model.FieldRainMM].Last != 10 {
		t.Fatalf("first bucket = %+v", first)
	}
	b.Add(reading(t0.Add(6*time.Minute), map[string]float64{model.FieldRainMM: 12.5}))
	second := b.Flush(t0.Add(10*time.Minute + 5*time.Second))
	a := second[0].Fields[model.FieldRainMM]
	if a.N != 1 || !near(a.Sum, 2.5) {
		t.Fatalf("delta across buckets = %+v", a)
	}
}

func TestFlagLast(t *testing.T) {
	b := New()
	b.Add(model.Reading{Model: "X", ID: "1", Time: t0, Flags: map[string]bool{model.FieldBatteryOK: true}})
	b.Add(model.Reading{Model: "X", ID: "1", Time: t0.Add(time.Second), Flags: map[string]bool{model.FieldBatteryOK: false}})
	a := b.FlushAll()[0].Fields[model.FieldBatteryOK]
	if a.Kind != model.KindFlag || a.N != 2 || a.Last != 0 || a.Max != 1 {
		t.Fatalf("flag = %+v", a)
	}
}

func TestLateUnstampedAndImplausible(t *testing.T) {
	b := New()
	b.Add(reading(t0, map[string]float64{model.FieldTempC: 20, model.FieldHumidityPct: 150, "bogus": 1}))
	b.Add(reading(time.Time{}, map[string]float64{model.FieldTempC: 20}))
	got := b.Flush(t0.Add(6 * time.Minute))
	if len(got) != 1 {
		t.Fatalf("buckets = %d", len(got))
	}
	if _, ok := got[0].Fields[model.FieldHumidityPct]; ok {
		t.Fatal("implausible humidity should be skipped")
	}
	if _, ok := got[0].Fields["bogus"]; ok {
		t.Fatal("unknown field should be skipped")
	}
	// A reading for the already flushed bucket is late.
	b.Add(reading(t0.Add(time.Minute), map[string]float64{model.FieldTempC: 21}))
	s := b.Stats()
	if s.Readings != 3 || s.Unstamped != 1 || s.Late != 1 || s.Implausible != 1 {
		t.Fatalf("stats = %+v", s)
	}
	if len(b.FlushAll()) != 0 {
		t.Fatal("late reading should not open a bucket")
	}
}

func TestMultipleStationsSorted(t *testing.T) {
	b := New()
	b.Add(model.Reading{Model: "B", ID: "1", Time: t0.Add(6 * time.Minute), Fields: map[string]float64{model.FieldTempC: 1}})
	b.Add(model.Reading{Model: "A", ID: "1", Time: t0.Add(6 * time.Minute), Fields: map[string]float64{model.FieldTempC: 1}})
	b.Add(model.Reading{Model: "A", ID: "1", Time: t0, Fields: map[string]float64{model.FieldTempC: 1}})
	got := b.FlushAll()
	if len(got) != 3 {
		t.Fatalf("buckets = %d", len(got))
	}
	if got[0].StationKey != "A:1:" || !got[0].Start.Equal(t0) || got[1].StationKey != "A:1:" || got[2].StationKey != "B:1:" {
		t.Fatalf("order = %v %v / %v %v / %v", got[0].StationKey, got[0].Start, got[1].StationKey, got[1].Start, got[2].StationKey)
	}
}

func TestForget(t *testing.T) {
	b := New()
	b.Add(reading(t0, map[string]float64{model.FieldRainMM: 5}))
	b.Forget("X:1:")
	b.Add(reading(t0.Add(time.Second), map[string]float64{model.FieldRainMM: 7}))
	a := b.FlushAll()[0].Fields[model.FieldRainMM]
	if a.N != 0 {
		t.Fatalf("counter state should be forgotten: %+v", a)
	}
}
