# ADR 0004: SQLite on device, raw in RAM, rollups every 15 minutes

Status: accepted 2026-11-16.

## Context

The device must keep months of history on an SD card without wearing it out,
survive A/B rollback of the OS, and run on 1 GB of RAM next to `rtl_433`. SD
wear comes from fsyncs, journaling and logs, not from data volume: a station
produces about 1M rows a year, a few KB per minute raw, and rollups for months
fit well under 100 MB.

Alternatives considered on the spec page: Postgres plus Timescale (a daemon,
128 MB default `shared_buffers`, painful major-version upgrades under A/B),
VictoriaMetrics (another daemon, PromQL), DuckDB (poor at small frequent
writes), tmpfs alone (does not survive power loss, cannot hold months).

With a Go daemon (ADR 0001), the usual SQLite driver needs cgo, which means a
cross C toolchain for every arm64 build.

## Decision

- SQLite is the on-device store. No Influx, no Postgres on the Pi.
- Raw samples stay in RAM in the collector buffer (optionally a small SQLite
  file on tmpfs). Only 5-minute rollups are persisted, in one batched
  transaction every 15 minutes, with `journal_mode=WAL` and
  `synchronous=NORMAL`.
- The driver is `modernc.org/sqlite`, a pure-Go translation of SQLite, so
  `CGO_ENABLED=0 GOARCH=arm64 go build` works from any host.
- The root filesystem is a read-only overlay with volatile journald and no
  swap; the database lives on a writable data partition that survives A/B
  switches.
- Schema is versioned with small forward-only migrations and mirrors the
  server schema closely enough that history syncs.
- History cadence shown to the user is configurable with a minimum of 10
  minutes.

## Consequences

- One file, no daemon, trivial backup (copy the file) and CSV export (a
  query).
- Up to one flush interval (15 minutes) of rollups is lost on a power cut.
  Whether that is acceptable is an open item on the spec page; the outbox and
  the server's gap detection limit the uplink impact.
- The pure-Go driver is somewhat slower than cgo SQLite. At a few hundred rows
  per flush this does not matter; if it ever does, the driver can be swapped
  behind `internal/store/` without changing the schema.
- Tests can use an in-memory or temp-file database with the same driver, so
  storage tests need no special setup.
