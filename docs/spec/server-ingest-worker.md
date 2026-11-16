# Server ingest worker

## Goal

A new worker monitors connections and ingests station data into WxAlerts.

## Where it lives

Updated 2026-11-16 in repo: the ingest worker is written in Rust and is the
first piece of a Rust migration of the WxAlerts API. It lives in the private
WxAlerts repositories, not in this public repo. This repo holds only the device
side plus the shared MQTT contract (`docs/contract/`) and the JSON Schemas
(`schemas/`), which are the single source of truth for both sides. The Notion
page said "Python asyncio worker"; that is superseded. See ADR 0003 and
`docs/server-integration.md` for the checklist of server-side changes.

## Design

- Rust worker on an EMQX shared subscription (`$share/ingest/pws/+/obs`, plus
  the `tel`, `event`, `update`, `status` and `ack/#` topics).
- EMQX client connect/disconnect events drive presence.
- Validate schema and plausibility, dedupe on `(device_id, seq)` and
  `(station key, bucket start)`, write to the PostGIS observation store and the
  device registry in Postgres/PostGIS.
- Publishes normalized observations to the relay stream and the QC worker.
- Per-device rate limiting and anomaly alerts (Discord ops channel).
- Command channel back to devices on `pws/{device_id}/cmd/#` (config push,
  support bundle request, reboot), every command Ed25519 signed.

## Repo notes (2026-11-16)

Two inconsistencies in the Notion page are corrected above:

- The shared subscription was written as `$share/ingest/pws/+/data`. The
  contract has no `data` topic; observations are on `obs`. The repo copy uses
  `obs`.
- The Design section said "write to Influx and the device registry in
  Postgres/PostGIS", while the Scale section on the same page and the hub
  decisions say the store is the existing PostGIS observation database with no
  Influx. The repo copy follows the PostGIS decision.

## Scale: 10,000+ stations (Nov 16)

- 10,000 stations at one uplink per 15 minutes is 11.1 msg/s on average.
  Unjittered, they all land within seconds of :00/:15, so each device offsets
  its uplink by a hash of its device ID.
- Storage with 5-minute buckets: one wide row per (station, bucket) is 288
  rows/day per station, 2.88M rows/day fleet-wide, about 1.05B rows/year. One
  row per field (10 fields) would be 28.8M rows/day.
- Server store (decided): the existing PostGIS observation database that
  already holds APRS, METAR and CWOP data. Use monthly partitions, a BRIN index
  on time and a btree on (`station_id`, `ts`). Estimate: about 100 bytes/row,
  roughly 105 GB/year raw. Timescale is optional. Retention tiers drop
  partitions or roll up to hourly.
- EMQX: 10,000+ persistent connections. Use built-in database or HTTP authn for
  per-device credentials, and size the cluster from connection count.
- One Pi per free account.

## Open

- Backpressure and replay handling after a broker outage.
- Whether the shared subscription group also covers `tel` and `event`, or those
  go to a separate consumer group. The Notion page only names the observation
  topic.
