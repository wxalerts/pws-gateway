package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind classes outbox entries for the prune policy.
type Kind string

const (
	// KindObs is an observation bucket message. Dropped last.
	KindObs Kind = "obs"
	// KindEvent is an event or telemetry message. Dropped first.
	KindEvent Kind = "event"
)

// ClassifyTopic maps an MQTT topic to a Kind by its last path segment. The
// contract puts observations under ".../obs"; everything else is an event
// or telemetry and is the first thing to go when space runs out.
func ClassifyTopic(topic string) Kind {
	seg := topic
	if i := strings.LastIndex(topic, "/"); i >= 0 {
		seg = topic[i+1:]
	}
	if seg == "obs" {
		return KindObs
	}
	return KindEvent
}

// OutboxItem is one queued message.
type OutboxItem struct {
	Seq       int64     `json:"seq"`
	Topic     string    `json:"topic"`
	Kind      Kind      `json:"kind"`
	Payload   []byte    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error,omitempty"`
}

const seqKey = "outbox_next_seq"

// Enqueue appends a message and returns its sequence number. The number is
// taken from the kv counter inside the same transaction so it is gapless
// and survives a prune of the table, and it doubles as the MQTT seq.
func (s *Store) Enqueue(ctx context.Context, topic string, payload []byte, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var cur string
	err = tx.QueryRowContext(ctx, "SELECT value FROM kv WHERE key = ?", seqKey).Scan(&cur)
	var seq int64 = 1
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return 0, err
	default:
		seq, err = strconv.ParseInt(cur, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("store: corrupt %s: %w", seqKey, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		seqKey, strconv.FormatInt(seq+1, 10)); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO outbox (seq, topic, kind, payload, created_at) VALUES (?, ?, ?, ?, ?)",
		seq, topic, string(ClassifyTopic(topic)), payload, FormatTime(now)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return seq, nil
}

// Pending returns up to limit queued messages, oldest first.
func (s *Store) Pending(ctx context.Context, limit int) ([]OutboxItem, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq, topic, kind, payload, created_at, attempts, last_error
		FROM outbox ORDER BY seq LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		var kind, created string
		if err := rows.Scan(&it.Seq, &it.Topic, &kind, &it.Payload, &created, &it.Attempts, &it.LastError); err != nil {
			return nil, err
		}
		it.Kind = Kind(kind)
		if it.CreatedAt, err = ParseTime(created); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Ack deletes delivered messages.
func (s *Store) Ack(ctx context.Context, seqs ...int64) error {
	if len(seqs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, seq := range seqs {
		if _, err := tx.ExecContext(ctx, "DELETE FROM outbox WHERE seq = ?", seq); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Fail records a delivery attempt that did not succeed.
func (s *Store) Fail(ctx context.Context, seq int64, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE outbox SET attempts = attempts + 1, last_error = ? WHERE seq = ?", msg, seq)
	return err
}

// OutboxStatus is the queue summary for the UI and telemetry.
type OutboxStatus struct {
	Count   int64     `json:"count"`
	Bytes   int64     `json:"bytes"`
	Oldest  time.Time `json:"oldest,omitempty"`
	Newest  time.Time `json:"newest,omitempty"`
	NextSeq int64     `json:"next_seq"`
	Obs     int64     `json:"obs"`
	Events  int64     `json:"events"`
}

// OutboxStatus summarises the queue.
func (s *Store) OutboxStatus(ctx context.Context) (OutboxStatus, error) {
	var st OutboxStatus
	var oldest, newest sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(LENGTH(payload)), 0), MIN(created_at), MAX(created_at),
		COALESCE(SUM(kind = 'obs'), 0), COALESCE(SUM(kind <> 'obs'), 0) FROM outbox`).
		Scan(&st.Count, &st.Bytes, &oldest, &newest, &st.Obs, &st.Events)
	if err != nil {
		return st, err
	}
	if oldest.Valid {
		if st.Oldest, err = ParseTime(oldest.String); err != nil {
			return st, err
		}
	}
	if newest.Valid {
		if st.Newest, err = ParseTime(newest.String); err != nil {
			return st, err
		}
	}
	v, ok, err := s.Get(ctx, seqKey)
	if err != nil {
		return st, err
	}
	st.NextSeq = 1
	if ok {
		st.NextSeq, _ = strconv.ParseInt(v, 10, 64)
	}
	return st, nil
}

// PrunePolicy is the documented outbox retention policy.
type PrunePolicy struct {
	// MaxAge drops anything older regardless of kind. Default 14 days.
	MaxAge time.Duration
	// MaxBytes caps the payload bytes kept. Default 25 MB.
	MaxBytes int64
	// Protect is the trailing window that is never pruned for size, so a
	// day of observations always survives an outage. Default 24 hours.
	Protect time.Duration
	// Chunk is how many rows go per delete statement. Default 200.
	Chunk int
}

func (p PrunePolicy) withDefaults() PrunePolicy {
	if p.MaxAge <= 0 {
		p.MaxAge = 14 * 24 * time.Hour
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = 25 << 20
	}
	if p.Protect <= 0 {
		p.Protect = 24 * time.Hour
	}
	if p.Chunk <= 0 {
		p.Chunk = 200
	}
	return p
}

// PruneResult reports what Prune removed.
type PruneResult struct {
	DroppedByAge   int64 `json:"dropped_by_age"`
	DroppedEvents  int64 `json:"dropped_events"`
	DroppedObs     int64 `json:"dropped_obs"`
	BytesRemaining int64 `json:"bytes_remaining"`
	// OverCap is true when the protected window alone exceeds MaxBytes and
	// nothing more could be dropped.
	OverCap bool `json:"over_cap"`
}

// Prune applies the policy: first drop everything past MaxAge, then while
// the queue is over MaxBytes drop the oldest events outside the protected
// window, then the oldest observations outside it. The last Protect hours
// are never touched for size.
func (s *Store) Prune(ctx context.Context, now time.Time, p PrunePolicy) (PruneResult, error) {
	p = p.withDefaults()
	s.mu.Lock()
	defer s.mu.Unlock()
	var res PruneResult

	ageCutoff := FormatTime(now.Add(-p.MaxAge))
	r, err := s.db.ExecContext(ctx, "DELETE FROM outbox WHERE created_at < ?", ageCutoff)
	if err != nil {
		return res, err
	}
	res.DroppedByAge, _ = r.RowsAffected()

	protectCutoff := FormatTime(now.Add(-p.Protect))
	bytes, err := s.outboxBytes(ctx)
	if err != nil {
		return res, err
	}
	for _, kind := range []Kind{KindEvent, KindObs} {
		for bytes > p.MaxBytes {
			r, err := s.db.ExecContext(ctx, `DELETE FROM outbox WHERE seq IN (
				SELECT seq FROM outbox WHERE kind = ? AND created_at < ? ORDER BY seq LIMIT ?)`,
				string(kind), protectCutoff, p.Chunk)
			if err != nil {
				return res, err
			}
			n, _ := r.RowsAffected()
			if n == 0 {
				break
			}
			if kind == KindObs {
				res.DroppedObs += n
			} else {
				res.DroppedEvents += n
			}
			if bytes, err = s.outboxBytes(ctx); err != nil {
				return res, err
			}
		}
	}
	res.BytesRemaining = bytes
	res.OverCap = bytes > p.MaxBytes
	return res, nil
}

func (s *Store) outboxBytes(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(LENGTH(payload)), 0) FROM outbox").Scan(&n)
	return n, err
}
