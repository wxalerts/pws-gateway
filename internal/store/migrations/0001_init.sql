-- Initial schema. Timestamps are UTC text in the fixed width form
-- 2006-01-02T15:04:05.000000Z so that string order is time order.

CREATE TABLE IF NOT EXISTS stations (
    key        TEXT PRIMARY KEY,
    model      TEXT NOT NULL,
    id         TEXT NOT NULL,
    channel    TEXT NOT NULL DEFAULT '',
    first_seen TEXT NOT NULL,
    last_seen  TEXT NOT NULL,
    sightings  INTEGER NOT NULL DEFAULT 0,
    meta       TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS rollups (
    station_key  TEXT NOT NULL,
    bucket_start TEXT NOT NULL,
    field        TEXT NOT NULL,
    kind         TEXT NOT NULL,
    n            INTEGER NOT NULL,
    avg          REAL NOT NULL DEFAULT 0,
    min          REAL NOT NULL DEFAULT 0,
    max          REAL NOT NULL DEFAULT 0,
    sum          REAL NOT NULL DEFAULT 0,
    vec          REAL NOT NULL DEFAULT 0,
    last         REAL NOT NULL DEFAULT 0,
    UNIQUE (station_key, bucket_start, field)
);

CREATE INDEX IF NOT EXISTS rollups_by_start ON rollups (bucket_start);

-- The uplink queue. seq is assigned from the kv counter inside the enqueue
-- transaction so it is also the MQTT message sequence number. kind is
-- "obs" for observation buckets and "event" for everything else; the prune
-- policy drops events first.
CREATE TABLE IF NOT EXISTS outbox (
    seq        INTEGER PRIMARY KEY,
    topic      TEXT NOT NULL,
    kind       TEXT NOT NULL DEFAULT 'event',
    payload    BLOB NOT NULL,
    created_at TEXT NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS outbox_by_kind_created ON outbox (kind, created_at);

CREATE TABLE IF NOT EXISTS kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
