package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/aggregate"
	"github.com/wxalerts/pws-gateway/internal/collector/mapping"
	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/discovery"
	"github.com/wxalerts/pws-gateway/internal/model"
	"github.com/wxalerts/pws-gateway/internal/store"
)

// Defaults for Config.
const (
	DefaultHoldMax          = 2000
	DefaultHoldLimit        = 15 * time.Minute
	DefaultDedupWindow      = 2 * time.Second
	DefaultRecentPerStation = 1000
	DefaultTickEvery        = 30 * time.Second
	// maxCandidateKeys bounds the per transmitter buffers kept before
	// promotion, matching the tracker's own candidate limit.
	maxCandidateKeys = 500
)

// EventType classifies pipeline events.
type EventType string

const (
	// EventSensorFound and EventSensorLost mirror the discovery events.
	EventSensorFound EventType = "sensor_found"
	EventSensorLost  EventType = "sensor_lost"
	// EventTimeSynced fires when held readings are released after the clock
	// syncs. Count is the number released.
	EventTimeSynced EventType = "time_synced"
	// EventHoldExpired fires at sync when held readings were too old to
	// stamp. Count is the number dropped.
	EventHoldExpired EventType = "hold_expired"
	// EventHoldOverflow fires when the hold ring is full and the oldest
	// reading is dropped. It fires for the first drop and then every 100th.
	EventHoldOverflow EventType = "hold_overflow"
	// EventStoreError fires when persisting stations or rollups fails.
	EventStoreError EventType = "store_error"
)

// Event is something the operator or the uplink should see. Station is set
// for sensor events.
type Event struct {
	Type    EventType
	At      time.Time
	Count   int
	Message string
	Station *model.Station
}

// Config configures a Pipeline. Zero values take the defaults.
type Config struct {
	// Store and Writer persist stations and 5 minute rollups. Both may be
	// nil in tests that only look at the in-memory state.
	Store  *store.Store
	Writer *store.Writer

	// TimeSynced reports whether the wall clock can be trusted. Nil means
	// always synced.
	TimeSynced func() bool
	// Now is the wall clock, used at the sync point and for housekeeping.
	// Mono is a monotonic offset, used to age held readings. Both are test
	// hooks; defaults are time.Now and time.Since the pipeline was built.
	Now  func() time.Time
	Mono func() time.Duration

	// HoldMax bounds the unsynced hold ring. HoldLimit is the oldest a held
	// reading may be and still be stamped at sync.
	HoldMax   int
	HoldLimit time.Duration

	// Location reads zone-less rtl_433 timestamps. Default UTC, because the
	// supervisor runs rtl_433 with "-M time:iso:usec:utc:tz" and a capture
	// made without ":tz" is still UTC.
	Location *time.Location

	// Selection decides, per discovered station, whether rollups are stored
	// and which canonical fields go into them. It is called for every
	// accepted reading with the tracker's current view of the station (so
	// Fields lists what the station has reported so far) and must be cheap
	// and safe for concurrent use. A nil Selection stores every field of
	// every station, which is what tests that do not care about selection
	// want; the app always sets it.
	Selection func(st model.Station) (selected bool, fields []string)

	// Mapper overrides the default mapping tables.
	Mapper *mapping.Mapper
	// Discovery tunes the tracker. Its Events field is ignored; use Events.
	Discovery discovery.Config
	// DedupWindow is the repeat suppression window; negative disables it.
	DedupWindow time.Duration
	// RecentPerStation bounds the in-memory readings kept per station for
	// the live charts.
	RecentPerStation int
	// TickEvery is the housekeeping interval in Run; negative disables the
	// timer (housekeeping then runs only as readings arrive).
	TickEvery time.Duration

	// Events receives pipeline events. It is never called with a pipeline
	// lock held, so it may call back into the Pipeline.
	Events func(Event)
	Logger *slog.Logger
}

// Status is a snapshot of the counters, for the web UI and telemetry.
type Status struct {
	Lines        int64 `json:"lines"`
	DecodeErrors int64 `json:"decode_errors"`
	Readings     int64 `json:"readings"`
	// Duplicates counts repeats suppressed before discovery.
	Duplicates int64 `json:"duplicates"`
	// Rejected counts readings that failed a discovery check.
	Rejected int64 `json:"rejected"`
	// Accepted counts readings from listed stations that reached the
	// bucketer.
	Accepted int64 `json:"accepted"`
	// Unselected counts accepted readings that were kept in memory only
	// because their station is not selected.
	Unselected int64 `json:"unselected"`

	Synced       bool  `json:"synced"`
	Held         int   `json:"held"`
	HoldOverflow int64 `json:"hold_overflow"`
	HoldExpired  int64 `json:"hold_expired"`

	Stations   int `json:"stations"`
	Candidates int `json:"candidates"`

	LastReading   time.Time       `json:"last_reading"`
	Aggregate     aggregate.Stats `json:"aggregate"`
	WriterPending int             `json:"writer_pending"`
	WriterFlushes int             `json:"writer_flushes"`
}

type heldReading struct {
	r    model.Reading
	mono time.Duration
}

type lastSeen struct {
	sig string
	at  time.Time
}

// Pipeline is the collector. Run drives it from a Source; Ingest drives it
// one line at a time for tests. The query methods are safe to call from any
// goroutine.
type Pipeline struct {
	cfg     Config
	dec     rtl433.Decoder
	mapper  *mapping.Mapper
	tracker *discovery.Tracker
	buckets *aggregate.Bucketer
	log     *slog.Logger

	mu        sync.Mutex
	st        Status
	synced    bool
	held      []heldReading
	cand      map[string][]model.Reading
	last      map[string]lastSeen
	recent    map[string][]model.Reading
	evq       []Event
	dirty     bool
	lastTick  time.Time
	overflown int64
}

// New builds a Pipeline.
func New(cfg Config) *Pipeline {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Mono == nil {
		start := time.Now()
		cfg.Mono = func() time.Duration { return time.Since(start) }
	}
	if cfg.HoldMax <= 0 {
		cfg.HoldMax = DefaultHoldMax
	}
	if cfg.HoldLimit <= 0 {
		cfg.HoldLimit = DefaultHoldLimit
	}
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}
	if cfg.DedupWindow == 0 {
		cfg.DedupWindow = DefaultDedupWindow
	}
	if cfg.RecentPerStation <= 0 {
		cfg.RecentPerStation = DefaultRecentPerStation
	}
	if cfg.TickEvery == 0 {
		cfg.TickEvery = DefaultTickEvery
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Mapper == nil {
		cfg.Mapper = mapping.New()
	}
	p := &Pipeline{
		cfg:     cfg,
		dec:     rtl433.Decoder{Location: cfg.Location},
		mapper:  cfg.Mapper,
		buckets: aggregate.New(),
		log:     cfg.Logger,
		cand:    map[string][]model.Reading{},
		last:    map[string]lastSeen{},
		recent:  map[string][]model.Reading{},
	}
	dc := cfg.Discovery
	dc.Events = p.onDiscovery
	p.tracker = discovery.New(dc)
	return p
}

// onDiscovery runs inside tracker calls, which only happen with p.mu held,
// so it only queues; emit delivers after the lock is released.
func (p *Pipeline) onDiscovery(ev discovery.Event) {
	st := ev.Station
	t := EventSensorFound
	if ev.Type == discovery.EventSensorLost {
		t = EventSensorLost
	}
	p.evq = append(p.evq, Event{Type: t, At: ev.At, Station: &st, Message: st.Key})
	p.dirty = true
}

// unlockAndEmit releases p.mu and then delivers queued events.
func (p *Pipeline) unlockAndEmit() {
	evs := p.evq
	p.evq = nil
	p.mu.Unlock()
	if p.cfg.Events == nil {
		return
	}
	for _, ev := range evs {
		p.cfg.Events(ev)
	}
}

func (p *Pipeline) queue(ev Event) { p.evq = append(p.evq, ev) }

// Run consumes src until it closes or ctx ends, then flushes open buckets
// and stations to the store. Stations saved by an earlier run are restored
// first, marked lost until heard.
func (p *Pipeline) Run(ctx context.Context, src Source) error {
	if p.cfg.Store != nil {
		if sts, err := p.cfg.Store.Stations(ctx); err != nil {
			p.log.Error("restore stations failed", "err", err)
		} else {
			p.tracker.Restore(sts)
		}
	}
	var tick <-chan time.Time
	if p.cfg.TickEvery > 0 {
		t := time.NewTicker(p.cfg.TickEvery)
		defer t.Stop()
		tick = t.C
	}
	lines := src.Lines()
	for {
		select {
		case <-ctx.Done():
			p.Shutdown()
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				p.Shutdown()
				return nil
			}
			p.Ingest(ctx, line)
		case <-tick:
			p.Tick(ctx, p.cfg.Now())
		}
	}
}

// Ingest processes one line.
func (p *Pipeline) Ingest(ctx context.Context, line rtl433.Line) {
	p.mu.Lock()
	p.st.Lines++
	msg, err := p.dec.Decode(line.Text)
	switch {
	case errors.Is(err, rtl433.ErrEmptyLine):
		p.st.Lines--
	case err != nil:
		p.st.DecodeErrors++
	default:
		p.st.Readings++
		r := p.mapper.Map(msg, line.Radio)
		r.Received = line.At
		p.route(ctx, r, line.At)
	}
	p.unlockAndEmit()
}

// IngestReading processes a reading that did not come from an rtl_433 line,
// such as one from a sensor on the I2C bus. It takes the same path as a
// decoded line: held while the clock is unsynced, stamped at sync, then
// discovered, deduplicated and aggregated. at is the receipt time.
func (p *Pipeline) IngestReading(ctx context.Context, r model.Reading, at time.Time) {
	p.mu.Lock()
	p.st.Lines++
	p.st.Readings++
	r.Received = at
	p.route(ctx, r, at)
	p.unlockAndEmit()
}

// route holds or processes one mapped reading depending on clock sync.
func (p *Pipeline) route(ctx context.Context, r model.Reading, at time.Time) {
	if p.checkSync(ctx) {
		if at.IsZero() {
			at = p.cfg.Now()
		}
		r.Time = at.UTC()
		p.process(ctx, r)
		p.tickIfDue(ctx, r.Time)
		return
	}
	r.Time = time.Time{}
	if len(p.held) >= p.cfg.HoldMax {
		p.held = append(p.held[:0], p.held[1:]...)
		p.st.HoldOverflow++
		p.overflown++
		if p.overflown == 1 || p.overflown%100 == 0 {
			p.queue(Event{Type: EventHoldOverflow, Count: int(p.overflown), Message: "unsynced hold ring full, oldest reading dropped"})
		}
	}
	p.held = append(p.held, heldReading{r: r, mono: p.cfg.Mono()})
}

// checkSync polls TimeSynced and releases the hold ring on the first true.
func (p *Pipeline) checkSync(ctx context.Context) bool {
	if p.synced {
		return true
	}
	if p.cfg.TimeSynced != nil && !p.cfg.TimeSynced() {
		return false
	}
	p.synced = true
	p.release(ctx)
	return true
}

func (p *Pipeline) release(ctx context.Context) {
	wall := p.cfg.Now().UTC()
	mono := p.cfg.Mono()
	held := p.held
	p.held = nil
	released, expired := 0, 0
	for i := range held {
		h := &held[i]
		age := mono - h.mono
		if age > p.cfg.HoldLimit {
			expired++
			continue
		}
		if age < 0 {
			age = 0
		}
		r := h.r
		r.Time = wall.Add(-age)
		p.process(ctx, r)
		released++
	}
	p.st.HoldExpired += int64(expired)
	if expired > 0 {
		p.queue(Event{Type: EventHoldExpired, At: wall, Count: expired,
			Message: fmt.Sprintf("%d readings older than %s dropped at clock sync", expired, p.cfg.HoldLimit)})
	}
	if released+expired > 0 {
		p.queue(Event{Type: EventTimeSynced, At: wall, Count: released,
			Message: fmt.Sprintf("clock synced, %d held readings stamped", released)})
	}
}

// signature is the repeat suppression key for a reading's content.
func signature(r model.Reading) string {
	raw := make(map[string]any, len(r.Raw))
	for k, v := range r.Raw {
		if k == "sequence_num" || k == "repeat" {
			continue
		}
		raw[k] = v
	}
	return fmt.Sprintf("%v|%v|%v", r.Fields, r.Flags, raw)
}

func (p *Pipeline) isDuplicate(key string, r model.Reading) bool {
	if p.cfg.DedupWindow < 0 {
		return false
	}
	sig := signature(r)
	if prev, ok := p.last[key]; ok && prev.sig == sig {
		if d := r.Time.Sub(prev.at); d >= 0 && d < p.cfg.DedupWindow {
			return true
		}
	}
	p.last[key] = lastSeen{sig: sig, at: r.Time}
	return false
}

// process feeds a stamped reading through discovery and aggregation.
func (p *Pipeline) process(ctx context.Context, r model.Reading) {
	key := r.StationKey()
	if p.isDuplicate(key, r) {
		p.st.Duplicates++
		return
	}
	p.st.LastReading = r.Time
	res := p.tracker.Observe(r)
	switch {
	case res.Rejected != "":
		p.st.Rejected++
		if !res.Discovered {
			delete(p.cand, key)
		}
	case res.Promoted:
		cands := p.cand[key]
		for i := range cands {
			p.accept(cands[i])
		}
		delete(p.cand, key)
		p.accept(r)
		p.persistStations(ctx, r.Time)
	case res.Discovered:
		p.accept(r)
	default:
		p.remember(key, r)
	}
}

// remember buffers a candidate's readings so that promotion does not lose
// the sightings that earned it.
func (p *Pipeline) remember(key string, r model.Reading) {
	if _, ok := p.cand[key]; !ok && len(p.cand) >= maxCandidateKeys {
		return
	}
	p.cand[key] = append(p.cand[key], r)
	if n := len(p.cand[key]); n > 16 {
		p.cand[key] = p.cand[key][n-16:]
	}
}

func (p *Pipeline) accept(r model.Reading) {
	p.st.Accepted++
	key := r.StationKey()
	// Every discovered station is previewable (recent readings below), but
	// only selected stations are aggregated for storage, and only with the
	// fields the owner wants.
	if sel, ok := p.forStorage(r); ok {
		p.buckets.Add(sel)
	} else {
		p.st.Unselected++
	}
	buf := p.recent[key]
	buf = append(buf, r)
	if len(buf) > p.cfg.RecentPerStation {
		buf = append([]model.Reading(nil), buf[len(buf)-p.cfg.RecentPerStation:]...)
	}
	p.recent[key] = buf
}

// forStorage applies Config.Selection to a reading. ok is false when the
// station is not selected. Otherwise the returned reading carries only the
// effective fields; the input is not modified because the recent buffer keeps
// the full reading.
func (p *Pipeline) forStorage(r model.Reading) (model.Reading, bool) {
	if p.cfg.Selection == nil {
		return r, true
	}
	st, found := p.tracker.Lookup(r.StationKey(), r.Time)
	if !found {
		st = model.Station{Key: r.StationKey(), Model: r.Model, ID: r.ID, Channel: r.Channel, Fields: r.FieldNames()}
	}
	selected, fields := p.cfg.Selection(st)
	if !selected {
		return r, false
	}
	keep := make(map[string]bool, len(fields))
	for _, f := range fields {
		keep[f] = true
	}
	out := r
	out.Fields, out.Flags = nil, nil
	for k, v := range r.Fields {
		if keep[k] {
			if out.Fields == nil {
				out.Fields = map[string]float64{}
			}
			out.Fields[k] = v
		}
	}
	for k, v := range r.Flags {
		if keep[k] {
			if out.Flags == nil {
				out.Flags = map[string]bool{}
			}
			out.Flags[k] = v
		}
	}
	return out, true
}

func (p *Pipeline) tickIfDue(ctx context.Context, now time.Time) {
	if p.lastTick.IsZero() {
		p.lastTick = now
		return
	}
	if now.Sub(p.lastTick) >= time.Minute {
		p.tickLocked(ctx, now)
	}
}

// Tick runs housekeeping: closes finished buckets into the writer, marks
// quiet stations lost, forgets stale candidates and persists station
// changes. Run calls it on a timer and as readings advance; tests may call
// it directly.
func (p *Pipeline) Tick(ctx context.Context, now time.Time) {
	p.mu.Lock()
	if p.checkSync(ctx) {
		p.tickLocked(ctx, now.UTC())
	}
	p.unlockAndEmit()
}

func (p *Pipeline) tickLocked(ctx context.Context, now time.Time) {
	p.lastTick = now
	p.tracker.Tick(now)
	if out := p.buckets.Flush(now); len(out) > 0 && p.cfg.Writer != nil {
		p.cfg.Writer.Add(out...)
	}
	for k, c := range p.cand {
		if len(c) == 0 || now.Sub(c[len(c)-1].Time) > 30*time.Minute {
			delete(p.cand, k)
		}
	}
	for k, l := range p.last {
		if now.Sub(l.at) > 10*time.Minute {
			delete(p.last, k)
		}
	}
	if p.dirty {
		p.persistStations(ctx, now)
	}
}

func (p *Pipeline) persistStations(ctx context.Context, now time.Time) {
	p.dirty = false
	if p.cfg.Store == nil {
		return
	}
	if err := p.cfg.Store.UpsertStations(ctx, p.tracker.Stations(now)); err != nil {
		p.dirty = true
		p.queue(Event{Type: EventStoreError, At: now, Message: "save stations: " + err.Error()})
	}
}

// Shutdown closes every open bucket, hands them to the writer, saves the
// stations and flushes the writer so nothing is left in memory. Run calls it
// on exit.
func (p *Pipeline) Shutdown() {
	p.mu.Lock()
	// Release anything held if the clock synced since the last line.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.checkSync(ctx)
	if out := p.buckets.FlushAll(); len(out) > 0 && p.cfg.Writer != nil {
		p.cfg.Writer.Add(out...)
	}
	p.persistStations(ctx, p.cfg.Now().UTC())
	if p.cfg.Writer != nil {
		if err := p.cfg.Writer.Flush(ctx); err != nil {
			p.queue(Event{Type: EventStoreError, Message: "flush rollups: " + err.Error()})
		}
	}
	p.unlockAndEmit()
}

// Stations returns the discovered stations as of now, sorted by key.
func (p *Pipeline) Stations(now time.Time) []model.Station {
	return p.tracker.Stations(now)
}

// Readings returns the recent readings of one station at or after since,
// oldest first. The buffer is bounded (Config.RecentPerStation), so older
// history comes from the store's rollups. The slice is a copy; the maps
// inside are shared and must be treated as read only.
func (p *Pipeline) Readings(key string, since time.Time) []model.Reading {
	p.mu.Lock()
	defer p.mu.Unlock()
	buf := p.recent[key]
	i := sort.Search(len(buf), func(i int) bool { return !buf[i].Time.Before(since) })
	return append([]model.Reading(nil), buf[i:]...)
}

// OutboxStatus reports the uplink queue depth from the store.
func (p *Pipeline) OutboxStatus(ctx context.Context) (store.OutboxStatus, error) {
	if p.cfg.Store == nil {
		return store.OutboxStatus{}, nil
	}
	return p.cfg.Store.OutboxStatus(ctx)
}

// Status returns the counters.
func (p *Pipeline) Status() Status {
	p.mu.Lock()
	s := p.st
	s.Synced = p.synced || p.cfg.TimeSynced == nil
	s.Held = len(p.held)
	p.mu.Unlock()
	s.Candidates = p.tracker.CandidateCount()
	s.Stations = len(p.tracker.Stations(p.cfg.Now()))
	s.Aggregate = p.buckets.Stats()
	if p.cfg.Writer != nil {
		s.WriterPending = p.cfg.Writer.Pending()
		s.WriterFlushes = p.cfg.Writer.Flushes()
	}
	return s
}
