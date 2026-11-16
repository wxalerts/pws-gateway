package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wxalerts/pws-gateway/internal/model"
)

// stationMeta is the JSON stored in stations.meta. Everything that is not a
// column lives here so adding a field is not a migration.
type stationMeta struct {
	Fields     []string     `json:"fields,omitempty"`
	RawFields  []string     `json:"raw_fields,omitempty"`
	LastSignal model.Signal `json:"last_signal"`
	BatteryOK  *bool        `json:"battery_ok,omitempty"`
	Radio      string       `json:"radio,omitempty"`
	Lost       bool         `json:"lost,omitempty"`
}

// UpsertStations writes stations in one transaction. The discovery tracker
// is the source of truth in RAM; this is called on a timer and at shutdown
// so a restart does not rediscover everything.
func (s *Store) UpsertStations(ctx context.Context, stations []model.Station) error {
	if len(stations) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO stations
		(key, model, id, channel, first_seen, last_seen, sightings, meta)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			model = excluded.model, id = excluded.id, channel = excluded.channel,
			first_seen = MIN(stations.first_seen, excluded.first_seen),
			last_seen = MAX(stations.last_seen, excluded.last_seen),
			sightings = excluded.sightings, meta = excluded.meta`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for i := range stations {
		st := &stations[i]
		meta, err := json.Marshal(stationMeta{
			Fields: st.Fields, RawFields: st.RawFields, LastSignal: st.LastSignal,
			BatteryOK: st.BatteryOK, Radio: st.Radio, Lost: st.Lost,
		})
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, st.Key, st.Model, st.ID, st.Channel,
			FormatTime(st.FirstSeen), FormatTime(st.LastSeen), st.Sightings, string(meta)); err != nil {
			return fmt.Errorf("store: upsert station %s: %w", st.Key, err)
		}
	}
	return tx.Commit()
}

// Stations loads every stored station sorted by key.
func (s *Store) Stations(ctx context.Context) ([]model.Station, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, model, id, channel, first_seen, last_seen, sightings, meta
		FROM stations ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Station
	for rows.Next() {
		var st model.Station
		var first, last, meta string
		if err := rows.Scan(&st.Key, &st.Model, &st.ID, &st.Channel, &first, &last, &st.Sightings, &meta); err != nil {
			return nil, err
		}
		if st.FirstSeen, err = ParseTime(first); err != nil {
			return nil, fmt.Errorf("store: station %s first_seen: %w", st.Key, err)
		}
		if st.LastSeen, err = ParseTime(last); err != nil {
			return nil, fmt.Errorf("store: station %s last_seen: %w", st.Key, err)
		}
		var m stationMeta
		if err := json.Unmarshal([]byte(meta), &m); err != nil {
			return nil, fmt.Errorf("store: station %s meta: %w", st.Key, err)
		}
		st.Fields, st.RawFields, st.LastSignal = m.Fields, m.RawFields, m.LastSignal
		st.BatteryOK, st.Radio, st.Lost = m.BatteryOK, m.Radio, m.Lost
		out = append(out, st)
	}
	return out, rows.Err()
}

// DeleteStation removes a station and its rollups, for the owner's forget
// action. Outbox entries already queued are left alone.
func (s *Store) DeleteStation(ctx context.Context, key string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, "DELETE FROM rollups WHERE station_key = ?", key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM stations WHERE key = ?", key); err != nil {
		return err
	}
	return tx.Commit()
}
