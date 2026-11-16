# MQTT contract (v1 draft)

Snapshot of the Notion page "MQTT Contract (v1 draft)" taken 2026-11-16. This
copy is authoritative from now on; Notion is no longer updated.

The normative machine-readable form of this contract is the set of JSON Schemas
under `schemas/` in this repository. The schemas are written from this page and
both the Go daemon in this repo and the Rust ingest worker in the private
WxAlerts repositories validate against the same files. If this document and a
schema disagree, fix one of them in the same pull request; do not let them
drift.

## Status

Draft. Payloads carry `v: 0` during development and breaking changes are
allowed. The contract freezes as `v: 1` only after it has been verified in
testing. Nothing ships to real stations before that.

## Conventions

- JSON, UTF-8. No compression in v1. zstd only if LTE data use becomes a
  concern (see `docs/out-of-scope-v1.md`).
- All timestamps are UTC, RFC 3339. A device never uplinks observations until
  time is synced (see `docs/spec/time-sync-and-rtc.md`).
- SI units everywhere. Unit conversion is display only.
- Additive changes (new optional fields) do not bump `v`. The server ignores
  unknown fields and accepts `v` and `v-1`.
- At-least-once delivery. The server dedupes on `(device_id, seq)` and on
  `(station key, bucket start)`.
- Max message size 64 KB. A larger backlog is split by bucket.
- Server rate limit: reject more than 4 messages/min per device.

## Topics

Prefix: `pws/{device_id}/`

| Topic | Direction | QoS | Retained | Purpose |
| --- | --- | --- | --- | --- |
| `status` | device to server | 1 | yes | `online` on connect. LWT publishes `offline`. |
| `obs` | device to server | 1 | no | Observation buckets, every 15 min with jitter. |
| `tel` | device to server | 1 | no | Device health, every 15 min. |
| `event` | device to server | 1 | no | Immediate events: update failed, time unsynced, sensor lost. |
| `update` | device to server | 1 | yes | Current version and update state. |
| `cmd/{type}` | server to device | 1 | no | Signed commands. Types are listed under Commands. |
| `ack/{cmd_id}` | device to server | 1 | no | Command result. |

Uplink time is offset by a hash of the device ID so 10,000+ devices do not land
at :00 and :15 together.

ACL: the device publishes only to `pws/{device_id}/#` and subscribes only to
`pws/{device_id}/cmd/#`. The broker enforces this per device (see
`docs/spec/device-claim-and-mqtt-auth.md`).

## `obs` payload

```json
{
  "v": 0,
  "device_id": "d_8f3a",
  "seq": 1234,
  "sent": "2026-11-16T14:15:03Z",
  "interval_s": 300,
  "buckets": [
    {
      "start": "2026-11-16T14:00:00Z",
      "stations": [
        {
          "key": "Acurite-5n1:1234:A",
          "n": 7,
          "meta": { "alt_m": 31.0, "wind_height_m": 4.5 },
          "f": {
            "temp_c": { "avg": 18.2, "min": 17.9, "max": 18.6 },
            "humidity_pct": { "avg": 64 },
            "pressure_station_hpa": { "avg": 1008.3 },
            "wind_speed_ms": { "avg": 2.1 },
            "wind_gust_ms": { "max": 4.8 },
            "wind_dir_deg": { "vec": 212 },
            "rain_mm": { "sum": 0.0 },
            "battery_ok": { "last": 1 }
          },
          "sig": { "rssi_db": -61.2, "snr_db": 19.4 },
          "raw": {}
        }
      ]
    }
  ]
}
```

- `key` is `model:id:channel`. A re-pair maps a new key to the same station on
  the server.
- Davis station keys use `davis:{model}:{tx_id}` (Davis is a post-launch
  module; the key format is reserved now so it is not invented twice).
- Field types: gauge (`avg`, `min`, `max`), direction (`vec`, vector mean),
  counter delta (`sum`, with reset handling), flag (`last`).
- Canonical fields (v1): `temp_c`, `humidity_pct`, `pressure_station_hpa`,
  `wind_speed_ms`, `wind_gust_ms`, `wind_dir_deg`, `rain_mm`, `uv_index`,
  `light_lux`, `solar_wm2`, `lightning_count`, `lightning_km`, `battery_ok`.
- Fields from protocols with no canonical mapping pass through under `raw`,
  unconverted, so all 258 decoders work on day one.
- The device sends station pressure plus `alt_m`, and raw wind plus
  `wind_height_m`. The server computes sea-level pressure and 10 m wind, so a
  formula fix never needs a firmware update.
- Only stations and fields the user selected are included.
- `n` is the number of raw samples that went into the bucket.
- `sig` here is the radio signal block for the station (RSSI, SNR). It is
  unrelated to the `sig` field on commands, which is a signature.

## `tel` payload

```json
{
  "v": 0,
  "device_id": "d_8f3a",
  "ts": "2026-11-16T14:15:03Z",
  "fw": "0.1.0",
  "os": "0.1.0",
  "uptime_s": 86400,
  "time": { "src": "ntp", "offset_ms": 3, "synced": true },
  "sys": { "cpu_temp_c": 52.1, "throttled": 0, "mem_free_mb": 410, "data_used_pct": 4 },
  "outbox": { "depth": 0, "oldest_s": 0 },
  "sdr": [
    { "radio_id": "r_01", "driver": "rtl_433", "band_mhz": 433.92, "decoders": 258, "packets_15m": 212 }
  ],
  "net": { "link": "lte" },
  "modem": { "carrier": "AT&T", "mcc_mnc": "310410", "reg": "home", "tech": "lte", "rssi_dbm": -83 },
  "gps": { "fix": 3, "sats": 9, "lat": 30.63, "lon": -87.03, "alt_m": 31 }
}
```

- `modem` and `gps` are present only when that hardware is attached.
- No IP addresses are sent.
- Exact GPS location goes server-side only. The front end shows the fuzzed
  location (1 km radius).
- Multiple radios (draft change, 2026-11-16, folded in here): each `sdr[]`
  entry carries `radio_id` and `driver`. `driver` is one of `rtl_433`, `davis`
  or `capture`. This is an additive change with no version bump. `radio_id` is
  the stable identity from the Radios page (serial plus USB port path, see
  `docs/spec/radio-roles-and-multi-sdr.md`).
- `time.src` is one of `ntp`, `lte`, `gps`, `rtc`, `none`.

## `update` payload

```json
{ "current": "0.1.0", "target": "0.1.1", "state": "deferred", "error": null, "ts": "2026-11-16T14:15:03Z" }
```

States: `idle`, `downloading`, `staged`, `rebooting`, `good`, `rolled_back`,
`failed`, `deferred`. A deferred update (for example weak LTE signal) is not a
failure.

## `event` payload

```json
{ "v": 0, "ts": "2026-11-16T14:15:03Z", "type": "time_unsynced", "detail": {} }
```

Types (v1): `time_unsynced`, `sensor_lost`, `sensor_found`, `update_failed`,
`rollback`, `sdr_missing`, `outbox_overflow`.

## Commands

All server-to-device commands are signed. This was an open item and is now
decided.

```json
{
  "id": "c_01",
  "type": "config",
  "device_id": "d_8f3a",
  "exp": "2026-11-16T15:00:00Z",
  "body": {},
  "sig": "base64url(ed25519 signature)"
}
```

- Every command has an `id` and an expiry (`exp`). The device acks on
  `ack/{id}` and ignores expired commands.
- `sig` is an Ed25519 signature over the canonical JSON of
  `{id, type, device_id, exp, body}`.
- The device pins the command-signing public key. It is a separate key from the
  release signing key (see `docs/spec/auto-update-and-rollback.md`).
- The device checks, in order: the `device_id` matches its own, the expiry has
  not passed, the `id` is not in its replay cache of seen IDs, and the
  signature verifies. A command that fails any check is not applied and is not
  acked as success.
- There is no shell command and no install command. Updates only come through
  the signed manifest.
- Config changes use the same commit-confirm safety as the web UI.

Command types:

| Type | Body | Notes |
| --- | --- | --- |
| `config` | Config patch | Commit-confirm, same as the web UI. |
| `reboot` | none | |
| `bundle` | none | Produce and upload a support bundle. |
| `update-check` | none | Check the manifest now instead of at the next window. |
| `resync` | `{ "from": ts, "to": ts }` | Re-send observation buckets for a range from local SQLite history. |
| `diag` | `{ "action": ... }` | Allowlist only: restart a service, re-run discovery, connectivity test, clear outbox. |
| `capture` | capture parameters | v2 only (listen mode). Not accepted in v1. |

The topics table on the Notion page listed only `config`, `reboot`, `bundle`,
`update-check` and `capture`; the later Signed commands section added `resync`
and `diag`. The table above is the merged list.

## `ack` payload

The Notion page did not give an example. Minimum content, to be fixed by the
schema:

```json
{ "v": 0, "id": "c_01", "ok": true, "error": null, "ts": "2026-11-16T14:15:04Z", "detail": {} }
```

## Outbox policy (proposed, validate in the spike)

- Estimate: about 3 KB per 5-minute bucket.
- Cap: 14 days or 25 MB, whichever comes first.
- Drop order: oldest events and telemetry first, then oldest observation
  buckets. Never drop the last 24 hours.
- Local SQLite history stays intact. The server detects gaps from `seq` and
  bucket starts, and requests a `resync` range.
- Replay on reconnect: at most 20 buckets per message and 1 message per second.
- Reconnect backoff: exponential with jitter, from 5 s to 5 min.

## Open

- Final canonical field list once the first sensor test matrix is done.
- Outbox overflow numbers are proposed above. Validate them in the spike.
- The replay rate (1 message per second) exceeds the server rate limit (4
  messages per minute per device). The Notion page states both without
  reconciling them. Either the broker exempts replay bursts, or replay must
  pace itself to the rate limit, or the rate limit applies to a rolling window
  larger than a minute. Decide in the M4 spike.
- The canonical JSON form used for the command signature is not named. The
  schema work needs one (RFC 8785 JCS is the obvious candidate); the Notion page
  does not pick it.
- `ack` payload shape is a repo proposal (above), not from Notion.
- Per-field quality flags: the Aggregation page says the payload carries
  per-field quality flags, but this page defines none. Decide whether QC flags
  are device-side (in `f`) or server-side only.

## Changes applied in the repo copy (2026-11-16)

- Folded the "Draft change: multiple radios" section into the `tel` payload and
  the `obs` key notes.
- Folded the "Signed commands (decided)" section into Commands and marked the
  former open item resolved.
- Added `device_id` and `sig` to the command example so the example matches the
  signed form.
- Added the `ack` example and merged the two command type lists.
- Named `schemas/` as the normative form.
