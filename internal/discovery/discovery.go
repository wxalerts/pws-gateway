// Package discovery decides which transmitters are real weather stations.
//
// rtl_433 decodes noise into plausible looking messages several times an
// hour, and a neighbour's sensor may be heard once and never again. A
// transmitter becomes a station only after MinSightings consistent
// sightings: each reading must have plausible values for every canonical
// field it carries, counters must not decrease except on a reset, and
// gauges must not jump further than a sensor physically can between
// sightings. Candidates that fail start over, and candidates not heard for
// the quiet period are dropped so noise cannot fill memory.
//
// Discovered stations are kept forever (until the owner removes them). When
// one is quiet for longer than QuietPeriod it is marked lost and a
// sensor_lost event fires; the next sighting clears the flag with a
// sensor_found event.
package discovery

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

// EventType is a discovery event kind. The names match the MQTT event
// vocabulary so the core agent can forward them unchanged.
type EventType string

const (
	// EventSensorFound fires on promotion and when a lost station returns.
	EventSensorFound EventType = "sensor_found"
	// EventSensorLost fires when a station passes the quiet period.
	EventSensorLost EventType = "sensor_lost"
)

// Event describes a station state change.
type Event struct {
	Type    EventType
	At      time.Time
	Station model.Station
}

// Config tunes the tracker. Zero values take the defaults.
type Config struct {
	// MinSightings is the number of consistent readings before a candidate
	// is listed. Default 3.
	MinSightings int
	// QuietPeriod is how long a station may be silent before it is lost
	// and a candidate before it is forgotten. Default 30 minutes, which is
	// many missed transmissions for every supported cadence.
	QuietPeriod time.Duration
	// RateWindow is the trailing window for the decode rate. Default 15
	// minutes to match the uplink interval.
	RateWindow time.Duration
	// MaxCandidates bounds memory under heavy noise. Default 500. When
	// full, the oldest candidate is evicted.
	MaxCandidates int
	// Events receives sensor_found and sensor_lost. Called synchronously
	// from Observe and Tick; keep it quick.
	Events func(Event)
	// MaxJumps overrides the per field jump limits between consecutive
	// sightings. Keys are canonical field names; absent means no limit.
	MaxJumps map[string]float64
}

// DefaultMaxJumps are the largest changes a gauge can make between two
// consecutive sightings of the same transmitter and still be the same
// sensor. They are generous: a 5n1 reports every 18 s, a WH65B every 16 s,
// and even indoor-outdoor moves do not change temperature 15 degrees in
// that time. Noise, by contrast, produces random values.
var DefaultMaxJumps = map[string]float64{
	model.FieldTempC:       15,
	model.FieldHumidityPct: 40,
	model.FieldPressureHPa: 20,
}

func (c Config) withDefaults() Config {
	if c.MinSightings <= 0 {
		c.MinSightings = 3
	}
	if c.QuietPeriod <= 0 {
		c.QuietPeriod = 30 * time.Minute
	}
	if c.RateWindow <= 0 {
		c.RateWindow = 15 * time.Minute
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = 500
	}
	if c.MaxJumps == nil {
		c.MaxJumps = DefaultMaxJumps
	}
	return c
}

type entry struct {
	st model.Station
	// last values per field for consistency checks across sightings
	lastFields map[string]float64
	// sighting times inside the rate window
	times     []time.Time
	rawFields map[string]bool
	fields    map[string]bool
	// consecutive counts consistent sightings for candidates
	consecutive int
}

// Tracker is the discovery state machine. It is safe for concurrent use.
type Tracker struct {
	cfg Config

	mu         sync.Mutex
	candidates map[string]*entry
	stations   map[string]*entry
}

// New builds a tracker.
func New(cfg Config) *Tracker {
	return &Tracker{
		cfg:        cfg.withDefaults(),
		candidates: map[string]*entry{},
		stations:   map[string]*entry{},
	}
}

// Result says what Observe did with a reading.
type Result struct {
	// Discovered is true when the reading belongs to a listed station,
	// including the reading that caused the promotion.
	Discovered bool
	// Promoted is true only for the reading that completed discovery.
	Promoted bool
	// Rejected is non-empty when the reading failed a consistency check,
	// naming the field or rule.
	Rejected string
}

// Observe feeds a stamped reading. Readings with a zero Time are ignored
// because every rule here reasons about time.
func (t *Tracker) Observe(r model.Reading) Result {
	if r.Time.IsZero() {
		return Result{Rejected: "unstamped"}
	}
	key := r.StationKey()
	t.mu.Lock()
	defer t.mu.Unlock()

	if e, ok := t.stations[key]; ok {
		if why := t.consistent(e, r); why != "" {
			// A listed station emitted nonsense. Do not update it, and do
			// not count the sighting, but do not unlist it either: one bad
			// decode should not hide a working station.
			return Result{Discovered: true, Rejected: why}
		}
		wasLost := e.st.Lost
		t.update(e, r)
		if wasLost {
			e.st.Lost = false
			t.emit(Event{Type: EventSensorFound, At: r.Time, Station: e.st})
		}
		return Result{Discovered: true}
	}

	if why := plausible(r); why != "" {
		// Implausible values never count, and they reset a candidate so a
		// noisy id has to start over.
		if e, ok := t.candidates[key]; ok {
			e.consecutive = 0
			e.lastFields = map[string]float64{}
		}
		return Result{Rejected: why}
	}

	e, ok := t.candidates[key]
	if !ok {
		t.evictIfFull()
		e = newEntry(r)
		t.candidates[key] = e
	} else if why := t.consistent(e, r); why != "" {
		e.consecutive = 0
		e.lastFields = map[string]float64{}
		t.update(e, r)
		e.st.Sightings = 0
		return Result{Rejected: why}
	}
	t.update(e, r)
	e.consecutive++
	if e.consecutive >= t.cfg.MinSightings {
		delete(t.candidates, key)
		t.stations[key] = e
		e.st.Sightings = e.consecutive
		t.emit(Event{Type: EventSensorFound, At: r.Time, Station: e.st})
		return Result{Discovered: true, Promoted: true}
	}
	return Result{}
}

// Tick runs time based housekeeping: marks quiet stations lost and forgets
// quiet candidates. Call it about once a minute.
func (t *Tracker) Tick(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, e := range t.stations {
		if !e.st.Lost && now.Sub(e.st.LastSeen) > t.cfg.QuietPeriod {
			e.st.Lost = true
			ev := e.st
			ev.DecodeRate = t.rate(e, now)
			t.emit(Event{Type: EventSensorLost, At: now, Station: ev})
		}
	}
	for key, e := range t.candidates {
		if now.Sub(e.st.LastSeen) > t.cfg.QuietPeriod {
			delete(t.candidates, key)
		}
	}
}

// Stations returns the discovered stations sorted by key, with the decode
// rate computed as of now.
func (t *Tracker) Stations(now time.Time) []model.Station {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]model.Station, 0, len(t.stations))
	for _, e := range t.stations {
		st := e.st
		st.DecodeRate = t.rate(e, now)
		st.Fields = sortedKeys(e.fields)
		st.RawFields = sortedKeys(e.rawFields)
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Lookup returns one discovered station.
func (t *Tracker) Lookup(key string, now time.Time) (model.Station, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.stations[key]
	if !ok {
		return model.Station{}, false
	}
	st := e.st
	st.DecodeRate = t.rate(e, now)
	st.Fields = sortedKeys(e.fields)
	st.RawFields = sortedKeys(e.rawFields)
	return st, true
}

// IsDiscovered reports whether the key is a listed station.
func (t *Tracker) IsDiscovered(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.stations[key]
	return ok
}

// CandidateCount reports how many transmitters are being watched but not
// yet listed, for the UI's "listening" indicator.
func (t *Tracker) CandidateCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.candidates)
}

// Restore seeds the tracker with stations loaded from the store at startup
// so they do not have to be rediscovered after a restart. Restored stations
// are marked lost until heard, without an event.
func (t *Tracker) Restore(stations []model.Station) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range stations {
		st := &stations[i]
		if st.Key == "" {
			continue
		}
		e := &entry{st: *st, lastFields: map[string]float64{}, fields: map[string]bool{}, rawFields: map[string]bool{}}
		for _, f := range st.Fields {
			e.fields[f] = true
		}
		for _, f := range st.RawFields {
			e.rawFields[f] = true
		}
		e.st.Lost = true
		t.stations[st.Key] = e
		delete(t.candidates, st.Key)
	}
}

// Remove unlists a station, for the owner's "forget this sensor" action.
func (t *Tracker) Remove(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.stations[key]
	delete(t.stations, key)
	delete(t.candidates, key)
	return ok
}

func newEntry(r model.Reading) *entry {
	return &entry{
		st: model.Station{
			Key: r.StationKey(), Model: r.Model, ID: r.ID, Channel: r.Channel,
			FirstSeen: r.Time,
		},
		lastFields: map[string]float64{},
		fields:     map[string]bool{},
		rawFields:  map[string]bool{},
	}
}

func (t *Tracker) update(e *entry, r model.Reading) {
	e.st.LastSeen = r.Time
	if e.st.FirstSeen.IsZero() || r.Time.Before(e.st.FirstSeen) {
		e.st.FirstSeen = r.Time
	}
	e.st.Sightings++
	e.st.Radio = r.Radio
	if r.Signal.HasLevel {
		e.st.LastSignal = r.Signal
	}
	for k, v := range r.Fields {
		e.fields[k] = true
		e.lastFields[k] = v
	}
	for k, on := range r.Flags {
		e.fields[k] = true
		if k == model.FieldBatteryOK {
			b := on
			e.st.BatteryOK = &b
		}
	}
	for k := range r.Raw {
		e.rawFields[k] = true
	}
	e.times = append(e.times, r.Time)
	cutoff := r.Time.Add(-t.cfg.RateWindow)
	i := 0
	for i < len(e.times) && e.times[i].Before(cutoff) {
		i++
	}
	e.times = e.times[i:]
}

func (t *Tracker) rate(e *entry, now time.Time) float64 {
	cutoff := now.Add(-t.cfg.RateWindow)
	n := 0
	for _, ts := range e.times {
		if !ts.Before(cutoff) {
			n++
		}
	}
	return float64(n) / t.cfg.RateWindow.Minutes()
}

// plausible checks every canonical field against the registry. It returns
// the offending field name or "".
func plausible(r model.Reading) string {
	for name, v := range r.Fields {
		if !model.Plausible(name, v) {
			return name
		}
	}
	return ""
}

// consistent checks a reading against the previous sighting of the same
// transmitter: plausibility, counters that only grow unless reset, and
// gauges that do not jump beyond the configured limits.
func (t *Tracker) consistent(e *entry, r model.Reading) string {
	if why := plausible(r); why != "" {
		return why
	}
	for name, v := range r.Fields {
		prev, ok := e.lastFields[name]
		if !ok {
			continue
		}
		f, _ := model.Lookup(name)
		switch f.Kind {
		case model.KindCounter:
			if v < prev && !isReset(prev, v) {
				return name + " decreased"
			}
		case model.KindGauge:
			if limit, ok := t.cfg.MaxJumps[name]; ok && math.Abs(v-prev) > limit {
				return name + " jumped"
			}
		}
	}
	return ""
}

// isReset decides whether a counter drop is a reset rather than a decode
// error. A reset lands near zero: the new value is under a tenth of the old
// one or under one unit. A counter reading 100 then 95 is a bad decode.
func isReset(prev, v float64) bool {
	return v < 1 || v < prev*0.1
}

func (t *Tracker) emit(ev Event) {
	if t.cfg.Events != nil {
		t.cfg.Events(ev)
	}
}

func (t *Tracker) evictIfFull() {
	if len(t.candidates) < t.cfg.MaxCandidates {
		return
	}
	var oldestKey string
	var oldest time.Time
	for k, e := range t.candidates {
		if oldestKey == "" || e.st.LastSeen.Before(oldest) {
			oldestKey, oldest = k, e.st.LastSeen
		}
	}
	delete(t.candidates, oldestKey)
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
