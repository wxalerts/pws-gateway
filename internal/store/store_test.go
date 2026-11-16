package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

var t0 = time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)

func openTemp(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pws.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenAppliesPragmasAndMigrations(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != 1 {
		t.Fatalf("schema version = %d %v", v, err)
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q %v", mode, err)
	}
	var sync int
	if err := s.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil || sync != 1 {
		t.Fatalf("synchronous = %d %v (want 1 = NORMAL)", sync, err)
	}
	var fk int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d %v", fk, err)
	}
	var busy int
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil || busy != 5000 {
		t.Fatalf("busy_timeout = %d %v", busy, err)
	}
	// Reopening must not re-run migrations.
	path := s.Path()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	var n int
	if err := s2.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil || n != 1 {
		t.Fatalf("migrations rows = %d %v", n, err)
	}
	size, err := s2.FileSize(ctx)
	if err != nil || size <= 0 {
		t.Fatalf("file size = %d %v", size, err)
	}
}

func TestOpenBadPath(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "missing", "dir", "x.db")); err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestKV(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if _, ok, err := s.Get(ctx, "a"); err != nil || ok {
		t.Fatalf("missing key = %v %v", ok, err)
	}
	if err := s.Set(ctx, "a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "a", "2"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.Get(ctx, "a")
	if err != nil || !ok || v != "2" {
		t.Fatalf("get = %q %v %v", v, ok, err)
	}
}

func TestStationsRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	on := true
	st := model.Station{
		Key: "Acurite-5n1:2342:A", Model: "Acurite-5n1", ID: "2342", Channel: "A",
		FirstSeen: t0, LastSeen: t0.Add(time.Minute), Sightings: 4,
		Fields: []string{model.FieldTempC}, RawFields: []string{"mic"},
		LastSignal: model.Signal{RSSI: -7, SNR: 20, Noise: -27, HasLevel: true},
		BatteryOK:  &on, Radio: "radio0",
	}
	if err := s.UpsertStations(ctx, []model.Station{st}); err != nil {
		t.Fatal(err)
	}
	// Update with a later last_seen and an earlier first_seen claim that
	// must not move first_seen forward.
	st2 := st
	st2.FirstSeen = t0.Add(time.Hour)
	st2.LastSeen = t0.Add(2 * time.Hour)
	st2.Sightings = 10
	st2.Lost = true
	if err := s.UpsertStations(ctx, []model.Station{st2}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stations(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("stations = %v %v", got, err)
	}
	g := got[0]
	if !g.FirstSeen.Equal(t0) || !g.LastSeen.Equal(t0.Add(2*time.Hour)) || g.Sightings != 10 || !g.Lost {
		t.Fatalf("got %+v", g)
	}
	if g.BatteryOK == nil || !*g.BatteryOK || g.Radio != "radio0" || g.LastSignal.RSSI != -7 || len(g.Fields) != 1 || len(g.RawFields) != 1 {
		t.Fatalf("meta = %+v", g)
	}
	if err := s.DeleteStation(ctx, g.Key); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Stations(ctx); len(got) != 0 {
		t.Fatal("station not deleted")
	}
	if err := s.UpsertStations(ctx, nil); err != nil {
		t.Fatal("empty upsert should be a no-op")
	}
}

func bucket(start time.Time, key string, temp float64) model.Bucket {
	return model.Bucket{Start: start, StationKey: key, N: 3, Fields: map[string]model.Aggregate{
		model.FieldTempC:      {Kind: model.KindGauge, N: 3, Avg: temp, Min: temp - 1, Max: temp + 1, Sum: temp * 3},
		model.FieldWindDirDeg: {Kind: model.KindDirection, N: 3, Avg: 180, Vec: 0.9},
		model.FieldRainMM:     {Kind: model.KindCounter, N: 2, Sum: 0.5, Last: 12.5, Min: 12, Max: 12.5},
		model.FieldBatteryOK:  {Kind: model.KindFlag, N: 3, Last: 1, Max: 1},
	}}
}

func TestRollupsRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	key := "X:1:"
	in := []model.Bucket{
		bucket(t0, key, 20),
		bucket(t0.Add(5*time.Minute), key, 21),
		bucket(t0.Add(10*time.Minute), "Y:2:", 5),
	}
	if err := s.WriteRollups(ctx, in); err != nil {
		t.Fatal(err)
	}
	// Writing the same bucket again replaces rather than duplicates.
	if err := s.WriteRollups(ctx, []model.Bucket{bucket(t0, key, 22)}); err != nil {
		t.Fatal(err)
	}
	n, err := s.RollupCount(ctx)
	if err != nil || n != 12 {
		t.Fatalf("rollup rows = %d %v", n, err)
	}
	got, err := s.Rollups(ctx, key, t0, t0.Add(15*time.Minute))
	if err != nil || len(got) != 2 {
		t.Fatalf("rollups = %d %v", len(got), err)
	}
	if !got[0].Start.Equal(t0) || got[0].N != 3 || got[0].Fields[model.FieldTempC].Avg != 22 {
		t.Fatalf("bucket 0 = %+v", got[0])
	}
	a := got[0].Fields[model.FieldRainMM]
	if a.Kind != model.KindCounter || a.N != 2 || a.Sum != 0.5 || a.Last != 12.5 {
		t.Fatalf("counter = %+v", a)
	}
	if got[0].Fields[model.FieldWindDirDeg].Vec != 0.9 || got[0].Fields[model.FieldBatteryOK].Kind != model.KindFlag {
		t.Fatalf("fields = %+v", got[0].Fields)
	}
	if got[1].Fields[model.FieldTempC].Avg != 21 {
		t.Fatalf("bucket 1 = %+v", got[1])
	}
	// Range end is exclusive.
	got, _ = s.Rollups(ctx, key, t0, t0.Add(5*time.Minute))
	if len(got) != 1 {
		t.Fatalf("exclusive end: %d", len(got))
	}
	dropped, err := s.PruneRollups(ctx, t0.Add(5*time.Minute))
	if err != nil || dropped != 4 {
		t.Fatalf("pruned = %d %v", dropped, err)
	}
	if err := s.WriteRollups(ctx, nil); err != nil {
		t.Fatal("empty write should be a no-op")
	}
}

func TestOutboxLifecycle(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	seq1, err := s.Enqueue(ctx, "pws/v0/dev1/obs", []byte(`{"a":1}`), t0)
	if err != nil || seq1 != 1 {
		t.Fatalf("seq1 = %d %v", seq1, err)
	}
	seq2, err := s.Enqueue(ctx, "pws/v0/dev1/events", []byte(`{"b":2}`), t0.Add(time.Second))
	if err != nil || seq2 != 2 {
		t.Fatalf("seq2 = %d %v", seq2, err)
	}
	items, err := s.Pending(ctx, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("pending = %d %v", len(items), err)
	}
	if items[0].Seq != 1 || items[0].Kind != KindObs || items[1].Kind != KindEvent || !items[0].CreatedAt.Equal(t0) {
		t.Fatalf("items = %+v", items)
	}
	if !bytes.Equal(items[0].Payload, []byte(`{"a":1}`)) {
		t.Fatalf("payload = %s", items[0].Payload)
	}
	if err := s.Fail(ctx, 1, errors.New("broker down")); err != nil {
		t.Fatal(err)
	}
	items, _ = s.Pending(ctx, 1)
	if len(items) != 1 || items[0].Attempts != 1 || items[0].LastError != "broker down" {
		t.Fatalf("after fail = %+v", items)
	}
	st, err := s.OutboxStatus(ctx)
	if err != nil || st.Count != 2 || st.Bytes != 14 || st.Obs != 1 || st.Events != 1 || st.NextSeq != 3 || !st.Oldest.Equal(t0) {
		t.Fatalf("status = %+v %v", st, err)
	}
	if err := s.Ack(ctx, 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(ctx); err != nil {
		t.Fatal("empty ack should be a no-op")
	}
	st, _ = s.OutboxStatus(ctx)
	if st.Count != 0 || st.NextSeq != 3 {
		t.Fatalf("status after ack = %+v", st)
	}
	// Sequence numbers never reuse even after the table empties.
	seq3, _ := s.Enqueue(ctx, "pws/v0/dev1/tel", []byte(`{}`), t0)
	if seq3 != 3 {
		t.Fatalf("seq3 = %d", seq3)
	}
}

func TestClassifyTopic(t *testing.T) {
	if ClassifyTopic("pws/v0/abc/obs") != KindObs || ClassifyTopic("obs") != KindObs {
		t.Fatal("obs")
	}
	if ClassifyTopic("pws/v0/abc/events") != KindEvent || ClassifyTopic("pws/v0/abc/tel") != KindEvent || ClassifyTopic("") != KindEvent {
		t.Fatal("event")
	}
}

func TestPrunePolicy(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	now := t0.Add(20 * 24 * time.Hour)
	payload := bytes.Repeat([]byte("x"), 100)

	// Ancient: dropped by age regardless of kind.
	if _, err := s.Enqueue(ctx, "d/obs", payload, t0); err != nil {
		t.Fatal(err)
	}
	// Old but inside 14 days: 3 events then 3 obs, all outside the 24 h window.
	for i := 0; i < 3; i++ {
		if _, err := s.Enqueue(ctx, "d/events", payload, now.Add(-72*time.Hour+time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Enqueue(ctx, "d/obs", payload, now.Add(-48*time.Hour+time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	// Recent: inside the protected window, never pruned for size.
	for i := 0; i < 4; i++ {
		if _, err := s.Enqueue(ctx, "d/obs", payload, now.Add(-time.Hour+time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Enqueue(ctx, "d/events", payload, now.Add(-time.Hour+time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	// 15 rows at 100 bytes. Cap at 950 bytes: must drop the ancient row by
	// age (14 left = 1400), then events outside protect (3 -> 1100), then
	// obs outside protect one chunk at a time (chunk 1) until <= 950: two
	// obs go (900). The 8 protected rows and 1 old obs remain.
	res, err := s.Prune(ctx, now, PrunePolicy{MaxBytes: 950, Chunk: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.DroppedByAge != 1 || res.DroppedEvents != 3 || res.DroppedObs != 2 || res.BytesRemaining != 900 || res.OverCap {
		t.Fatalf("result = %+v", res)
	}
	items, _ := s.Pending(ctx, 100)
	if len(items) != 9 {
		t.Fatalf("remaining = %d", len(items))
	}
	// The one surviving old obs must be the newest of the three.
	if items[0].Kind != KindObs || !items[0].CreatedAt.Equal(now.Add(-48*time.Hour+2*time.Minute)) {
		t.Fatalf("oldest survivor = %+v", items[0])
	}

	// With a cap below the protected window, nothing protected goes and
	// OverCap reports it.
	res, err = s.Prune(ctx, now, PrunePolicy{MaxBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.DroppedObs != 1 || res.DroppedEvents != 0 || !res.OverCap || res.BytesRemaining != 800 {
		t.Fatalf("protected prune = %+v", res)
	}
	if err := s.Vacuum(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWriterBatchesAndRetries(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	w := NewWriter(s, WriterOptions{Interval: time.Minute, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if w.Interval() != MinFlushInterval {
		t.Fatalf("interval not clamped: %v", w.Interval())
	}
	w = NewWriter(s, WriterOptions{Interval: 10 * time.Millisecond, AllowShortInterval: true,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxPending: 100})
	w.Add(bucket(t0, "X:1:", 1), bucket(t0.Add(5*time.Minute), "X:1:", 2))
	w.Add()
	if w.Pending() != 2 {
		t.Fatalf("pending = %d", w.Pending())
	}
	if n, _ := s.RollupCount(ctx); n != 0 {
		t.Fatal("nothing should be written before flush")
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.RollupCount(ctx); n != 8 || w.Pending() != 0 || w.Flushes() != 1 {
		t.Fatalf("after flush rows=%d pending=%d flushes=%d", n, w.Pending(), w.Flushes())
	}
	if err := w.Flush(ctx); err != nil || w.Flushes() != 1 {
		t.Fatal("empty flush should not count")
	}

	// A failed flush keeps the batch.
	w.Add(bucket(t0.Add(10*time.Minute), "X:1:", 3))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := w.Flush(canceled); err == nil {
		t.Fatal("expected error from canceled context")
	}
	if w.Pending() != 1 {
		t.Fatalf("batch lost on failure: pending = %d", w.Pending())
	}

	// Run flushes on the ticker and once more on shutdown.
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	deadline := time.Now().Add(2 * time.Second)
	for w.Pending() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	w.Add(bucket(t0.Add(15*time.Minute), "X:1:", 4))
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v", err)
	}
	if n, _ := s.RollupCount(ctx); n != 16 {
		t.Fatalf("rows after run = %d", n)
	}
}

func TestTimeLayoutSorts(t *testing.T) {
	a := FormatTime(t0)
	b := FormatTime(t0.Add(time.Microsecond))
	c := FormatTime(t0.Add(365 * 24 * time.Hour))
	if a >= b || b >= c {
		t.Fatalf("not sortable: %s %s %s", a, b, c)
	}
	back, err := ParseTime(a)
	if err != nil || !back.Equal(t0) {
		t.Fatalf("parse = %v %v", back, err)
	}
	if _, err := ParseTime("2024-05-01T12:00:00Z"); err != nil {
		t.Fatalf("rfc3339 fallback: %v", err)
	}
	if _, err := ParseTime("nope"); err == nil {
		t.Fatal("garbage parsed")
	}
}
