# MQTT contract v2

Status: draft, additive to [the v1 draft](mqtt-v1-draft.md). Nothing in v1 is
removed. A gateway on old firmware keeps working unchanged; the server accepts
both trees until the last v1 gateway has updated.

The machine readable form is `schemas/`. Obs v2 is `schemas/obs2.schema.json`;
every other message keeps its v1 schema.

## What changed, and what did not

| Area | v1 | v2 | Breaking? |
| --- | --- | --- | --- |
| Topic tree | `pws/{device_id}/{leaf}` | `V2/{user_id}/PWS/{device_id}/{leaf}` | Yes for a subscriber: a new tree. Additive for the server: both are served. |
| Leaves | status, obs, tel, event, update, ack/{cmd_id}, cmd/{type} | the same leaves | No |
| Credential | username = client id = device id, server issued password | unchanged | No |
| `obs` payload | `v: 0`, `buckets[].stations[]` (one entry per radio sensor) | `v: 2`, `buckets[]` is one combined record per bucket | Yes, new shape. Not valid on the other tree. |
| `tel`, `event`, `update`, `status`, `ack`, `cmd` | v1 schemas, `v: 0` | identical payloads | No |
| Command signature | Ed25519 over canonical `{id, type, device_id, exp, body}` | unchanged. The topic is not signed, so a command is valid on either tree. | No |

`{user_id}` is the owner's numeric account id, returned by the claim flow
(`POST /pws/claim/poll` carries `user_id` next to `device_id`). It is written in
canonical decimal: no sign, no leading zeros.

## How the two versions coexist

- Topic tree decides the rules. On `pws/...` the server accepts exactly the v1
  payloads. On `V2/...` an `obs` must be `v: 2`; the other kinds are unchanged.
- The `v` field decides the obs shape. `v: 0` is the stations array, `v: 2` is
  the combined record. A `v: 2` obs on a `pws/` topic, or a `v: 0` obs on a
  `V2/` topic, is rejected, so a mixed up client fails loudly instead of being
  half understood.
- The gateway picks the tree from what the claim response gave it. A credential
  with a `user_id` means V2 topics and obs v2. A credential without one (a
  gateway claimed before the server returned it, or a server that predates v2)
  stays on v1. There is no negotiation and no fallback in the middle of a run.
- Authorisation: the device may use the V2 namespace only while its owner is
  the user in the topic. A device whose owner changes loses the old namespace
  (the new owner's id is a different topic). The old `pws/{device_id}` namespace
  stays allowed for the device until the server stops serving v1.
- Outbox: a message already queued when a gateway moves to V2 is published to
  the topic it was queued for. Nothing is rewritten.

## The gateway is the weather station

In v1 the gateway uplinked every sensor it heard, and the server mapped sensors
to a composite station. In v2 the aggregation moves to the gateway:

- The gateway sends ONE record per bucket. The server does not need to know the
  individual sensors in range, and no longer creates a station row for a
  sensor key that appears in a v2 obs.
- The owner still chooses which sensor supplies which field, on the device,
  with the same seven groups the server composite used: temperature, humidity,
  pressure, wind (speed, gust and direction together), rain, solar_uv
  (uv_index, light_lux, solar_wm2) and lightning (count and distance). The
  setting is `composite` in the gateway config: group name to sensor key.
- With no setting a group is supplied by the only selected sensor that reports
  it; with several candidates and no setting the group is left out. That is the
  rule the server composite had, kept so nothing changes silently.
- `battery_ok` is the worst case over the sensors that supplied a field.
- `meta.alt_m` comes from the pressure source and `meta.wind_height_m` from the
  wind source. The server still derives sea level pressure and 10 m wind.
- `src` (field name to sensor key) says who supplied what. It is for support
  and display and is only sent when more than one sensor contributed.
- `raw` keeps the unmapped decoder fields of every sensor heard in the bucket,
  keyed by sensor key. These are the kept raw samples.
- `n` is the packet count over every sensor heard in the bucket. `sig` is the
  mean radio signal over the sensors heard.

## `obs` v2 payload

```json
{
  "v": 2,
  "device_id": "d_8f3a",
  "seq": 1234,
  "sent": "2026-11-16T14:15:03Z",
  "interval_s": 300,
  "buckets": [
    {
      "start": "2026-11-16T14:00:00Z",
      "live": true,
      "synced": true,
      "n": 19,
      "meta": { "alt_m": 31.0, "wind_height_m": 4.5 },
      "f": { "temp_c": { "avg": 18.2, "min": 17.9, "max": 18.6 }, "humidity_pct": { "avg": 64 } },
      "src": { "temp_c": "Acurite-5n1:1234:A", "uv_index": "Fineoffset-WH24:77:" },
      "sig": { "rssi_db": -61.2, "snr_db": 19.4 },
      "raw": { "Acurite-5n1:1234:A": { "sequence_num": 2 } }
    }
  ]
}
```

- Records are oldest first and carry the station's own timestamps (`start`).
- `live`: the record was built in the normal cycle shortly after its bucket
  closed. `false` for a backfill (a backlog the gateway built long after the
  bucket, or an answer to a `resync` command). The gateway cannot see how long
  a record waited in its outbox, so the server also compares its own receive
  time with the bucket end and treats a late record as backfilled whatever the
  flag says.
- `synced`: the gateway clock was synchronized while the bucket was recorded. A
  gateway never uplinks observations while unsynced, so a conforming gateway
  sends `true`; the flag exists so a server can distrust a record if that ever
  stops being true.
- Small payloads: one record per bucket, no per sensor repetition, `src` and
  `raw` optional. Max 64 KB per message and at most 20 records in a backlog
  message, as in v1.

### Idempotency

A record is identified by the device and its bucket start rounded down to
`interval_s` (UTC). The server stores the first copy and ignores later ones, so
a retry, a resync and a restart that re-sends are all safe. `(device_id, seq)`
still dedupes whole messages.

## Rate limit and replay (resolves the v1 open item)

v1 said the server rejects more than 4 messages a minute per device while the
outbox policy replays one message a second. v2 resolves it on the device: the
replay pace is one message every 15 seconds (4 a minute), so a replay never
trips the limit and the server needs no exemption. A 14 day backlog is about
4000 records, 200 messages, and drains in under an hour. Live messages queue
behind a backlog in the outbox; that delay is accepted because the records are
idempotent and oldest first.

## Server side station link: APRS and CWOP

An owner may declare the APRS or CWOP id of the same physical station (for
example `CW8180`) on the device in the dashboard. It is stored on the server
only, never in the payload. The server uses it to keep one canonical observation
per station and timestamp when the station is also heard over APRS: the direct
upload wins, and the first copy to arrive is kept until the direct one does.
See the server documentation for the rules and their limits.

## Moving a gateway to V2 without a re-claim

A gateway claimed before the server returned `user_id` holds a credential and no
user id. It learns it by calling `GET /pws/device/identity` with HTTP Basic
(`device_id` and the same password it uses for the broker). The server answers
`{device_id, user_id, credential_version}`, or the same 401 for any bad
credential. The gateway asks once before it first connects after an update and
every 6 hours; when the id differs from the stored one it rewrites its credential
file (mode 0600, other fields kept) and restarts its uplink on the V2 topics.
A signed `config` command was rejected for this: it must be queued and signed
per device and can only arrive while the gateway is already connected, and it
would need a trigger, whereas this reuses the credential check the broker
already does and needs no new secret.

Fallback: if the broker refuses the V2 tree as not authorized (the connect, the
command subscription or a publish, reason 0x87), the gateway switches to the v1
topics, reconnects with the v1 will and subscription, drops what it had queued
for V2 and rebuilds those buckets in the v1 shape (nothing is lost), and
remembers the refusal for 24 hours for that user id. A device whose owner
changes gets a new credential and user id; the server stops authorizing the old
owner's namespace at once, so a stale device falls back instead of publishing
into it.

## Choosing the sensor for each group

The Stations page shows "Which station supplies each reading" when two or more
selected stations report the same group. The choice is the `composite` config
section; unchosen ambiguous groups are left out and flagged.

## Rollout

1. Server first: the API returns `user_id` and authorizes both trees, the broker
   ACL for the ingest account is widened, the ingest worker subscribes to both.
   Nothing changes for a v1 gateway.
2. Then the gateway firmware. A gateway uses V2 once its credential carries
   `user_id`: from a fresh claim, or, for the gateway already in the field, from
   the identity call above on its first start after the update.
3. When the last v1 gateway has updated, v1 can be retired.
