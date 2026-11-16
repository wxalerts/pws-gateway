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

## Implemented (M4, device side)

Code: `internal/uplink/`. Tests run against an in-process broker (mochi-mqtt)
and a replay of `testdata/real/acurite-433-20261116.jsonl`.

- Jitter formula. `offset = (big-endian uint64 of the first 8 bytes of
  SHA-256(device_id)) mod 900` seconds. A device publishes `obs` and `tel` at
  every instant `k * 15 min + offset` (UTC). The offset is deterministic and in
  [0, 900) s, so a device keeps its slot across restarts and a fleet spreads
  evenly over the quarter hour.
- Outbox first. A message is serialized, validated against its schema and
  written to the SQLite outbox before anything touches the network. The pump
  publishes QoS 1 and deletes the entry only after PUBACK. The obs cursor
  (`uplink_state` in the store kv: last `seq`, end of the last bucket built) is
  saved right after the enqueue; if the process dies between the two, startup
  adopts the queued message, so it is delivered once with the same
  `(device_id, seq)` and its buckets are not rebuilt.
- Limits. 3 buckets per message normally, at most 20 on a backlog (more than 3
  unsent buckets), at most one outbox publish per second, every message under
  64 KB (split at bucket boundaries, a single oversized bucket drops its `raw`
  first). `seq` is a separate counter from the outbox row id so `tel`, `event`
  and `ack` traffic never creates gaps the server would read as loss. The pump
  uses 1 message per second as the contract says; the contract's open question
  about the server limit of 4 messages per minute is unchanged.
- Time gate. No `obs` is built while the platform reports the clock unsynced.
  One `time_unsynced` event goes out when the clock stays unsynced for more
  than 10 minutes after start; buckets that closed meanwhile are sent in the
  first cycle after sync.
- Selection and content. Only stations with `selected: true` in the config are
  sent. `meta` comes from the station's `alt_m` and `wind_height_m`. Rollups
  hold no raw or radio signal data, so `raw` and `sig` come from the
  collector's in-memory readings for the bucket and are omitted for buckets
  older than that buffer (a long replay or a `resync`).
- Reconnect backoff is exponential from 5 s to 5 min with plus or minus 20%
  jitter.
- Not done yet: remote commit-confirm for `config`, the `bundle`,
  `update-check` and `diag` handlers (they ack `not_implemented`), the
  `fleetsim` tool, and testing against a real EMQX broker.
