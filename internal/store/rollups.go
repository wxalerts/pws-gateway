package store

import (
	"context"
	"fmt"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

// WriteRollups stores buckets in one transaction. A bucket already present
// for the same station, start and field is replaced, so re-flushing after a
// crash is harmless.
func (s *Store) WriteRollups(ctx context.Context, buckets []model.Bucket) error {
	if len(buckets) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO rollups
		(station_key, bucket_start, field, kind, n, avg, min, max, sum, vec, last)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(station_key, bucket_start, field) DO UPDATE SET
			kind = excluded.kind, n = excluded.n, avg = excluded.avg, min = excluded.min,
			max = excluded.max, sum = excluded.sum, vec = excluded.vec, last = excluded.last`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, b := range buckets {
		start := FormatTime(b.Start)
		for _, name := range b.FieldNames() {
			a := b.Fields[name]
			if _, err := stmt.ExecContext(ctx, b.StationKey, start, name, a.Kind.String(),
				a.N, a.Avg, a.Min, a.Max, a.Sum, a.Vec, a.Last); err != nil {
				return fmt.Errorf("store: rollup %s %s %s: %w", b.StationKey, start, name, err)
			}
		}
	}
	return tx.Commit()
}

// Rollups returns the buckets for one station with from <= start < to,
// oldest first. Bucket.N is not stored and comes back as the largest field
// count, which is exact for single-message stations and a lower bound for
// stations like the 5n1 that split fields across messages.
func (s *Store) Rollups(ctx context.Context, stationKey string, from, to time.Time) ([]model.Bucket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT bucket_start, field, kind, n, avg, min, max, sum, vec, last
		FROM rollups WHERE station_key = ? AND bucket_start >= ? AND bucket_start < ?
		ORDER BY bucket_start, field`, stationKey, FormatTime(from), FormatTime(to))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Bucket
	var cur *model.Bucket
	for rows.Next() {
		var startStr, field, kindStr string
		var a model.Aggregate
		if err := rows.Scan(&startStr, &field, &kindStr, &a.N, &a.Avg, &a.Min, &a.Max, &a.Sum, &a.Vec, &a.Last); err != nil {
			return nil, err
		}
		start, err := ParseTime(startStr)
		if err != nil {
			return nil, err
		}
		kind, ok := model.ParseKind(kindStr)
		if !ok {
			return nil, fmt.Errorf("store: rollup has unknown kind %q", kindStr)
		}
		a.Kind = kind
		if cur == nil || !cur.Start.Equal(start) {
			out = append(out, model.Bucket{Start: start, StationKey: stationKey, Fields: map[string]model.Aggregate{}})
			cur = &out[len(out)-1]
		}
		cur.Fields[field] = a
		if a.N > cur.N {
			cur.N = a.N
		}
	}
	return out, rows.Err()
}

// PruneRollups deletes buckets older than before and returns how many rows
// went. The history cadence the owner sets decides how long to keep.
func (s *Store) PruneRollups(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM rollups WHERE bucket_start < ?", FormatTime(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RollupCount is for tests and the status page.
func (s *Store) RollupCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM rollups").Scan(&n)
	return n, err
}
