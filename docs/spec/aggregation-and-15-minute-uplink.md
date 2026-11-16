# Aggregation and 15-minute uplink

## Goal

Reliable offload to WxAlerts every 15 minutes.

## Design

- 5-minute base buckets so 15-minute uplinks align.
- Aggregation rules: wind direction uses vector averaging. Gust is the max in
  the interval. Rain is the delta of a cumulative counter with reset handling.
  Temperature, humidity and pressure use mean plus min/max.
- Persistent outbox on disk. QoS 1. Replays after outages, with a size cap and
  oldest-first drop policy (numbers in the contract's Outbox policy section).
- Payload is versioned JSON with device ID, station keys, units, and per-field
  quality flags.
- Telemetry (version, uptime, queue depth, modem stats) rides the same cadence,
  plus an immediate message for failures.

## Repo notes (2026-11-16)

- This page and the contract disagree in two places. The contract payload has
  no per-field units (everything is SI, so units are implicit) and defines no
  per-field quality flags. The contract is the source of truth for the wire
  format; this page's "units" and "quality flags" are recorded as an open item
  there.
- Aggregation functions are implemented once in `internal/` and are reused by
  the local history charts, so the local view and the uplink never disagree.
