// Package aggregate folds readings into clock-aligned buckets.
//
// A bucket is five minutes wide and aligned to the clock, so 12:00:00 to
// 12:04:59.999 is one bucket regardless of when the process started. Every
// 15 minutes the uplink sends the three buckets that closed since the last
// send. The Bucketer knows nothing about the uplink; it only answers "which
// buckets are closed as of now".
//
// Field kinds, from the model registry:
//
//	gauge      avg, min, max, sum over the samples in the bucket
//	direction  vector mean of unit vectors, so 350 and 10 average to 0
//	counter    sum of deltas between consecutive samples; a drop in the
//	           counter is a reset and the new value counts as the delta
//	flag       last value in the bucket
//
// Counter state (the previous raw value) lives across buckets and across
// flushes, because the first sample of a bucket needs the last sample of the
// previous one to produce a delta.
package aggregate

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

// DefaultWidth is the v1 bucket width.
const DefaultWidth = 5 * time.Minute

// Align returns the start of the bucket containing t.
func Align(t time.Time, width time.Duration) time.Time {
	return t.UTC().Truncate(width)
}

// NextBoundary returns the first bucket boundary strictly after t. A flush
// timer should fire at this instant (plus a small grace period for late
// readings).
func NextBoundary(t time.Time, width time.Duration) time.Time {
	return Align(t, width).Add(width)
}

// Option configures a Bucketer.
type Option func(*Bucketer)

// WithWidth sets the bucket width. It must divide an hour evenly so buckets
// stay aligned to wall clock quarter hours.
func WithWidth(w time.Duration) Option {
	return func(b *Bucketer) { b.width = w }
}

// WithGrace sets how long after a boundary a bucket stays open for late
// readings. rtl_433 stamps at reception, so this only needs to cover
// pipeline latency. Default is 2 seconds.
func WithGrace(g time.Duration) Option {
	return func(b *Bucketer) { b.grace = g }
}

// Stats counts what the bucketer did, for telemetry.
type Stats struct {
	Readings      int `json:"readings"`
	Unstamped     int `json:"unstamped"`
	Late          int `json:"late"`
	Implausible   int `json:"implausible"`
	CounterResets int `json:"counter_resets"`
	OpenBuckets   int `json:"open_buckets"`
}

type fieldState struct {
	agg model.Aggregate
	sx  float64 // direction: sum of cos
	sy  float64 // direction: sum of sin
}

type bucketState struct {
	start  time.Time
	key    string
	n      int
	fields map[string]*fieldState
}

type counterState struct {
	last float64
	seen bool
}

// Bucketer accumulates readings. It is safe for concurrent use.
type Bucketer struct {
	width time.Duration
	grace time.Duration

	mu sync.Mutex
	// open buckets by station key then start.
	open map[string]map[time.Time]*bucketState
	// counters by station key then field.
	counters map[string]map[string]*counterState
	// closedBefore is the start time before which buckets are already
	// flushed; readings for earlier buckets are late and dropped.
	closedBefore map[string]time.Time
	stats        Stats
}

// New makes a Bucketer.
func New(opts ...Option) *Bucketer {
	b := &Bucketer{
		width:        DefaultWidth,
		grace:        2 * time.Second,
		open:         map[string]map[time.Time]*bucketState{},
		counters:     map[string]map[string]*counterState{},
		closedBefore: map[string]time.Time{},
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Width returns the bucket width.
func (b *Bucketer) Width() time.Duration { return b.width }

// Add folds one reading into its bucket. Readings without a Time are
// dropped and counted: the collector must stamp before aggregating.
// Implausible field values are skipped field by field so one bad gauge does
// not discard the rest of the message.
func (b *Bucketer) Add(r model.Reading) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stats.Readings++
	if r.Time.IsZero() {
		b.stats.Unstamped++
		return
	}
	key := r.StationKey()
	start := Align(r.Time, b.width)
	if cb, ok := b.closedBefore[key]; ok && start.Before(cb) {
		b.stats.Late++
		return
	}
	byStart := b.open[key]
	if byStart == nil {
		byStart = map[time.Time]*bucketState{}
		b.open[key] = byStart
	}
	bs := byStart[start]
	if bs == nil {
		bs = &bucketState{start: start, key: key, fields: map[string]*fieldState{}}
		byStart[start] = bs
	}
	bs.n++

	for name, v := range r.Fields {
		f, ok := model.Lookup(name)
		if !ok {
			continue
		}
		if !f.Plausible(v) {
			b.stats.Implausible++
			continue
		}
		switch f.Kind {
		case model.KindGauge:
			addGauge(bs.field(name, f.Kind), v)
		case model.KindDirection:
			addDirection(bs.field(name, f.Kind), v)
		case model.KindCounter:
			b.addCounter(bs, key, name, v)
		case model.KindFlag:
			addFlag(bs.field(name, f.Kind), v)
		}
	}
	for name, on := range r.Flags {
		f, ok := model.Lookup(name)
		if !ok || f.Kind != model.KindFlag {
			continue
		}
		v := 0.0
		if on {
			v = 1
		}
		addFlag(bs.field(name, f.Kind), v)
	}
}

func (bs *bucketState) field(name string, kind model.Kind) *fieldState {
	fs := bs.fields[name]
	if fs == nil {
		fs = &fieldState{agg: model.Aggregate{Kind: kind, Min: math.Inf(1), Max: math.Inf(-1)}}
		bs.fields[name] = fs
	}
	return fs
}

func addGauge(fs *fieldState, v float64) {
	a := &fs.agg
	a.N++
	a.Sum += v
	a.Avg = a.Sum / float64(a.N)
	a.Min = math.Min(a.Min, v)
	a.Max = math.Max(a.Max, v)
}

func addDirection(fs *fieldState, deg float64) {
	a := &fs.agg
	rad := deg * math.Pi / 180
	fs.sx += math.Cos(rad)
	fs.sy += math.Sin(rad)
	a.N++
	a.Min = math.Min(a.Min, deg)
	a.Max = math.Max(a.Max, deg)
	a.Last = deg
	a.Avg = VectorMean(fs.sx, fs.sy)
	a.Vec = math.Hypot(fs.sx, fs.sy) / float64(a.N)
}

// VectorMean converts summed unit vector components to a heading in
// [0, 360). An all-canceling sum (no preferred direction) yields 0.
func VectorMean(sx, sy float64) float64 {
	if math.Abs(sx) < 1e-12 && math.Abs(sy) < 1e-12 {
		return 0
	}
	deg := math.Atan2(sy, sx) * 180 / math.Pi
	deg = math.Mod(deg+360, 360)
	if deg >= 360 {
		deg -= 360
	}
	return deg
}

func (b *Bucketer) addCounter(bs *bucketState, key, name string, v float64) {
	byField := b.counters[key]
	if byField == nil {
		byField = map[string]*counterState{}
		b.counters[key] = byField
	}
	cs := byField[name]
	if cs == nil {
		cs = &counterState{}
		byField[name] = cs
	}
	fs := bs.field(name, model.KindCounter)
	a := &fs.agg
	a.Min = math.Min(a.Min, v)
	a.Max = math.Max(a.Max, v)
	a.Last = v
	if cs.seen {
		delta := v - cs.last
		if delta < 0 {
			// The counter wrapped or the sensor lost power. We cannot know
			// how much fell between the last reading and the reset, so the
			// new value since zero is the best estimate.
			delta = v
			b.stats.CounterResets++
		}
		a.N++
		a.Sum += delta
	} else {
		// First ever sample: no delta yet. The bucket still records that
		// the field exists, with N zero, so the store can show the raw
		// counter value under Last.
		a.N = 0
	}
	cs.last = v
	cs.seen = true
}

func addFlag(fs *fieldState, v float64) {
	a := &fs.agg
	a.N++
	a.Last = v
	a.Min = math.Min(a.Min, v)
	a.Max = math.Max(a.Max, v)
}

// Flush returns every bucket whose window ended at or before now minus the
// grace period, sorted by station key then start, and removes them. Call it
// shortly after each boundary.
func (b *Bucketer) Flush(now time.Time) []model.Bucket {
	b.mu.Lock()
	defer b.mu.Unlock()
	cutoff := now.UTC().Add(-b.grace)
	var out []model.Bucket
	for key, byStart := range b.open {
		for start, bs := range byStart {
			if start.Add(b.width).After(cutoff) {
				continue
			}
			out = append(out, bs.finish())
			delete(byStart, start)
			if cb, ok := b.closedBefore[key]; !ok || start.Add(b.width).After(cb) {
				b.closedBefore[key] = start.Add(b.width)
			}
		}
		if len(byStart) == 0 {
			delete(b.open, key)
		}
	}
	sortBuckets(out)
	return out
}

// FlushAll closes every open bucket regardless of time, for shutdown.
func (b *Bucketer) FlushAll() []model.Bucket {
	return b.Flush(time.Unix(1<<40, 0))
}

// Stats returns counters for telemetry.
func (b *Bucketer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.stats
	for _, byStart := range b.open {
		s.OpenBuckets += len(byStart)
	}
	return s
}

// Forget drops counter state for a station, for when a station is removed.
func (b *Bucketer) Forget(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.counters, key)
	delete(b.open, key)
	delete(b.closedBefore, key)
}

func (bs *bucketState) finish() model.Bucket {
	out := model.Bucket{Start: bs.start, StationKey: bs.key, N: bs.n, Fields: make(map[string]model.Aggregate, len(bs.fields))}
	for name, fs := range bs.fields {
		a := fs.agg
		if math.IsInf(a.Min, 0) {
			a.Min = 0
		}
		if math.IsInf(a.Max, 0) {
			a.Max = 0
		}
		out.Fields[name] = a
	}
	return out
}

func sortBuckets(bs []model.Bucket) {
	sort.Slice(bs, func(i, j int) bool {
		if bs[i].StationKey != bs[j].StationKey {
			return bs[i].StationKey < bs[j].StationKey
		}
		return bs[i].Start.Before(bs[j].Start)
	})
}
