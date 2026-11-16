# On-device storage and SD wear protection

## Goal

Months of history without wearing out the SD card.

## Design

- tmpfs does not survive power loss and cannot hold months of history. Use it
  only as the hot buffer.
- Read-only overlay root, volatile journald, no swap. Wear comes from fsyncs,
  journaling and logs, not data volume.
- Decision: SQLite, no Influx or Postgres on the Pi. At about 1M rows/year per
  station, plain tables indexed on (station, field, ts) are enough. SQLite is
  one file and no daemon, and it survives A/B rollback.
- Raw samples stay in RAM (collector buffer, optionally a small SQLite file on
  tmpfs). Persist 5-minute rollups to the on-disk SQLite file in one batched
  transaction every 15 minutes (WAL, `synchronous=NORMAL`).
- Storage estimate: a few KB/min raw. Rollups for months fit in well under
  100 MB.
- User-set history cadence in the UI, minimum 10 minutes.
- Schema is versioned with tiny migrations, mirroring the server schema so
  history syncs cleanly. Backup and CSV export are file copy and plain queries.
- Alternatives considered: Postgres plus Timescale (daemon, 128 MB default
  `shared_buffers`, painful major-version upgrades under A/B), VictoriaMetrics
  (extra daemon, PromQL), DuckDB (poor at small frequent writes).

Updated 2026-11-16 in repo: SQLite is accessed through the pure-Go driver
`modernc.org/sqlite`, so `GOARCH=arm64` cross-compiles need no cgo and no
cross C toolchain. See ADR 0004.

## Open

- Data-loss window on power cut (up to one flush interval). Acceptable?
