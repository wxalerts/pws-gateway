package uplink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/wxalerts/pws-gateway/internal/aggregate"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/model"
)

// obsCycle builds the obs messages for every closed bucket that has not been
// enqueued yet and enqueues them. It returns the number of messages. It does
// nothing while the clock is unsynced.
func (u *Uplink) obsCycle(ctx context.Context) (int, error) {
	if !u.checkTimeGate(ctx) {
		u.log.Info("clock unsynced, observations held back")
		return 0, nil
	}
	if u.opts.FlushRollups != nil {
		if err := u.opts.FlushRollups(ctx); err != nil {
			return 0, fmt.Errorf("flush rollups: %w", err)
		}
	}
	u.buildMu.Lock()
	defer u.buildMu.Unlock()

	now := u.now().UTC()
	// Only buckets that closed at least `settle` ago are complete in the store.
	to := aggregate.Align(now.Add(-settle), BucketWidth)
	st, err := u.loadState(ctx)
	if err != nil {
		return 0, err
	}
	from := st.BuiltThrough
	if from.IsZero() {
		from = to.Add(-firstHistory)
	}
	from = laterOf(from, now.Add(-maxResync))
	if !to.After(from) {
		return 0, nil
	}
	cfg := u.opts.Config.Get()
	keys := selectedKeys(cfg)
	if len(keys) == 0 {
		return 0, nil
	}
	buckets, err := u.loadBuckets(ctx, cfg, keys, from, to)
	if err != nil {
		return 0, err
	}
	n, err := u.emitObs(ctx, st, buckets, now, true)
	if err != nil {
		return n, err
	}
	// Everything up to `to` is now accounted for, including buckets with no
	// data, so an empty gap is not scanned again.
	st, err = u.loadState(ctx)
	if err != nil {
		return n, err
	}
	if to.After(st.BuiltThrough) {
		st.BuiltThrough = to
		if err := u.saveState(ctx, st); err != nil {
			return n, err
		}
	}
	return n, nil
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// selectedKeys lists the stations the owner marked selected, sorted.
func selectedKeys(cfg config.Config) []string {
	var keys []string
	for k, s := range cfg.Stations {
		if s.Selected {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// loadBuckets reads the rollups of the selected stations in [from, to) and
// groups them by bucket start, oldest first.
func (u *Uplink) loadBuckets(ctx context.Context, cfg config.Config, keys []string, from, to time.Time) ([]contract.Bucket, error) {
	byStart := map[time.Time][]contract.Station{}
	for _, key := range keys {
		rolls, err := u.opts.Store.Rollups(ctx, key, from, to)
		if err != nil {
			return nil, err
		}
		if len(rolls) == 0 {
			continue
		}
		var recent []model.Reading
		if u.opts.Collector != nil {
			recent = u.opts.Collector.Readings(key, rolls[0].Start)
		}
		for i := range rolls {
			st, ok := stationFromBucket(&rolls[i], cfg.Stations[key], recent)
			if !ok {
				continue
			}
			byStart[rolls[i].Start] = append(byStart[rolls[i].Start], st)
		}
	}
	starts := make([]time.Time, 0, len(byStart))
	for s := range byStart {
		starts = append(starts, s)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	out := make([]contract.Bucket, 0, len(starts))
	for _, s := range starts {
		out = append(out, contract.Bucket{Start: s.UTC(), Stations: byStart[s]})
	}
	return out, nil
}

// stationFromBucket converts one stored rollup to the wire form, adding the
// owner's site metadata, the radio signal block and the raw passthrough from
// the readings still held in memory for that bucket. Rollups carry no raw or
// signal data, so a bucket older than the in-memory window (a long replay)
// goes out without them.
func stationFromBucket(b *model.Bucket, ann config.Station, recent []model.Reading) (contract.Station, bool) {
	f := fieldsFromBucket(b)
	end := b.Start.Add(BucketWidth)
	st := contract.Station{Key: b.StationKey, N: b.N, F: f}
	alt, wind := ann.AltM, ann.WindHeightM
	st.Meta = &contract.StationMeta{AltM: &alt, WindHeightM: &wind}

	raw := map[string]any{}
	var rssi, snr float64
	levels, count := 0, 0
	for i := range recent {
		r := &recent[i]
		if r.Time.Before(b.Start) || !r.Time.Before(end) {
			continue
		}
		count++
		for k, v := range r.Raw {
			raw[k] = v
		}
		if r.Signal.HasLevel {
			rssi += r.Signal.RSSI
			snr += r.Signal.SNR
			levels++
		}
	}
	if count > st.N {
		st.N = count
	}
	if levels > 0 {
		a, s := finite(rssi/float64(levels)), finite(snr/float64(levels))
		st.Sig = &contract.Signal{RSSIdB: &a, SNRdB: &s}
	}
	if len(raw) > 0 {
		if data, err := json.Marshal(raw); err == nil {
			st.Raw = data
		}
	}
	return st, true
}

func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// fieldsFromBucket maps stored aggregates to the contract field types. A
// field with a non finite number is skipped rather than failing the message.
func fieldsFromBucket(b *model.Bucket) contract.Fields {
	var f contract.Fields
	gauge := func(a model.Aggregate) *contract.Gauge {
		if !allFinite(a.Avg, a.Min, a.Max) {
			return nil
		}
		avg, lo, hi := a.Avg, a.Min, a.Max
		return &contract.Gauge{Avg: &avg, Min: &lo, Max: &hi}
	}
	for _, name := range b.FieldNames() {
		a := b.Fields[name]
		switch name {
		case model.FieldTempC:
			f.TempC = gauge(a)
		case model.FieldHumidityPct:
			f.HumidityPct = gauge(a)
		case model.FieldPressureHPa:
			f.PressureStationHPa = gauge(a)
		case model.FieldWindSpeedMS:
			f.WindSpeedMS = gauge(a)
		case model.FieldWindGustMS:
			f.WindGustMS = gauge(a)
		case model.FieldUVIndex:
			f.UVIndex = gauge(a)
		case model.FieldLightLux:
			f.LightLux = gauge(a)
		case model.FieldSolarWM2:
			f.SolarWM2 = gauge(a)
		case model.FieldLightningKM:
			f.LightningKM = gauge(a)
		case model.FieldWindDirDeg:
			if allFinite(a.Avg) {
				f.WindDirDeg = &contract.Direction{Vec: math.Mod(math.Mod(a.Avg, 360)+360, 360)}
			}
		case model.FieldRainMM:
			if allFinite(a.Sum) {
				f.RainMM = &contract.Counter{Sum: a.Sum}
			}
		case model.FieldLightningCount:
			if allFinite(a.Sum) {
				f.LightningCount = &contract.Counter{Sum: a.Sum}
			}
		case model.FieldBatteryOK:
			last := 0
			if a.Last >= 0.5 {
				last = 1
			}
			f.BatteryOK = &contract.Flag{Last: last}
		}
	}
	return f
}

func allFinite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// emitObs packs buckets into messages and enqueues them, outbox first: the
// serialized message is stored before the cursor moves and before anything is
// published, so a crash loses nothing. With advance the cursor moves past each
// message's last bucket; a resync leaves it alone but still takes fresh seqs.
//
// Packing: up to NormalBuckets in one message when that is all there is, else
// up to MaxReplayBuckets per message (a backlog). A message that would exceed
// 64 KB is split at bucket boundaries; a single oversized bucket loses its raw
// passthrough before giving up.
func (u *Uplink) emitObs(ctx context.Context, st state, buckets []contract.Bucket, now time.Time, advance bool) (int, error) {
	per := NormalBuckets
	if len(buckets) > NormalBuckets {
		per = MaxReplayBuckets
	}
	msgs := 0
	for len(buckets) > 0 {
		n := min(per, len(buckets))
		st.Seq++
		data, used, err := u.encodeFit(st.Seq, buckets, n, now)
		if err != nil {
			return msgs, err
		}
		topic := contract.Topic(u.opts.DeviceID, contract.KindObs)
		if err := u.enqueue(ctx, topic, data); err != nil {
			return msgs, err
		}
		if u.afterEnqueue != nil {
			if err := u.afterEnqueue(); err != nil {
				return msgs, err
			}
		}
		last := buckets[used-1].Start.Add(BucketWidth)
		if advance && last.After(st.BuiltThrough) {
			st.BuiltThrough = last
		}
		if err := u.saveState(ctx, st); err != nil {
			return msgs, err
		}
		u.wake()
		msgs++
		buckets = buckets[used:]
	}
	return msgs, nil
}

// errBucketTooLarge reports a single bucket that cannot fit in a message.
var errBucketTooLarge = errors.New("uplink: one bucket exceeds the 64 KB message limit")

// encodeFit serializes the first n buckets, shrinking n until the message is
// within the limit, and returns the payload and how many buckets it holds.
func (u *Uplink) encodeFit(seq uint64, buckets []contract.Bucket, n int, now time.Time) ([]byte, int, error) {
	for {
		msg := contract.Obs{
			V: contract.Version, DeviceID: u.opts.DeviceID, Seq: seq,
			Sent: now.UTC().Truncate(time.Second), IntervalS: int(BucketWidth / time.Second),
			Buckets: buckets[:n],
		}
		data, err := contract.Marshal(&msg)
		switch {
		case err == nil:
			return data, n, nil
		case !errors.Is(err, contract.ErrTooLarge):
			return nil, 0, err
		case n > 1:
			n = (n + 1) / 2
		case stripRaw(&buckets[0]):
			u.log.Warn("bucket too large, dropped raw passthrough", "start", buckets[0].Start)
		default:
			return nil, 0, errBucketTooLarge
		}
	}
}

// stripRaw clears the raw passthrough of every station in b and reports
// whether there was anything to clear.
func stripRaw(b *contract.Bucket) bool {
	cleared := false
	for i := range b.Stations {
		if b.Stations[i].Raw != nil {
			b.Stations[i].Raw = nil
			cleared = true
		}
	}
	return cleared
}
