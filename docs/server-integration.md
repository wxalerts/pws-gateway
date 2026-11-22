# Server integration checklist

Everything the private WxAlerts repositories (API, infrastructure, front end)
must change for the gateway to work. This public repo owns only the device
side, the MQTT contract and the JSON Schemas.

**Before anything on this list is published or deployed, the maintainer gets a
written report describing the change, the repositories touched, the data
affected and the rollback path.** Nothing here is done unilaterally by a coding
agent. The report is the gate; this checklist is the scope.

Each item names the spec page it comes from.

## 1. EMQX: per-device authentication and ACL

Source: Device claim and MQTT auth; Server ingest worker (Scale).

- [ ] Per-device credentials: client ID equals device ID, username is the
      device ID, password issued at claim time. Use EMQX built-in database or
      HTTP authn against the API; pick one and size for 10,000+ persistent
      connections.
- [ ] ACL per device: publish allowed only to `pws/{device_id}/#`, subscribe
      allowed only to `pws/{device_id}/cmd/#`. Deny everything else, including
      `$share/...` and other devices' prefixes. Contract v2 adds the same rule
      under `V2/{user_id}/PWS/{device_id}/`, allowed only while `user_id` is the
      device's current owner; both trees are served until v1 is retired.
- [ ] Broker-side limits: max payload 64 KB on `pws/#`, rate limit of 4
      messages per minute per device (resolved in contract v2: the device paces
      replay to one message every 15 seconds, see `docs/contract/mqtt-v2.md`),
      connection rate limiting.
- [ ] LWT handling: `pws/{device_id}/status` retained, `offline` on
      disconnect.
- [ ] Credential revocation and rotation on unlink and re-claim.
- [ ] Connect and disconnect events exported (webhook or rule engine) for
      presence in the registry.
- [ ] Open: mTLS in addition to username/password.

## 2. Shared subscription for the Rust ingest worker

Source: Server ingest worker; ADR 0003.

- [ ] A service identity for the ingest worker with subscribe rights on
      `$share/ingest/pws/+/obs` and the other device-to-server topics (`tel`,
      `event`, `update`, `status`, `ack/#`), and publish rights on
      `pws/+/cmd/#`.
- [ ] Rust worker (first piece of the Rust migration) that validates every
      message against the `schemas/` files from `wxalerts/pws-gateway` vendored
      at a pinned commit, dedupes on `(device_id, seq)` and
      `(station key, bucket start)`, writes to PostGIS and the device registry,
      and publishes normalized observations to the relay and QC streams.
- [ ] Per-device rate limiting and anomaly alerts to the Discord ops channel.
- [ ] Backpressure and replay handling after a broker outage (open item).
- [ ] Decide whether `tel` and `event` share the `ingest` group or get their
      own consumer.

## 3. API: device-code claim flow

Source: Device claim and MQTT auth.

- [ ] Endpoint for the device to request a device code and polling token.
- [ ] Endpoint for the device to poll for approval and receive MQTT credentials
      exactly once.
- [ ] Logged-in web page at wxalerts.org where the user enters the code;
      Authentik authorizes the claim.
- [ ] Device registry: device ID, owner, claim time, credential version,
      presence, last `tel`, last `update` state.
- [ ] Unlink from the dashboard: revoke credentials, mark unclaimed.
- [ ] Enforce one Pi per free account; device cap for paid tiers is open.
- [ ] Fleet observability from `tel` and `update`: version spread, update
      failure and deferral rates, and a halt switch for staged rollouts (hub
      "Before development handoff").

## 4. PostGIS: observation and registry tables

Source: Server ingest worker (Scale); Cross-station QC.

- [ ] PWS observation table in the existing PostGIS observation database (the
      one holding APRS, METAR and CWOP), one wide row per (station, 5-minute
      bucket): about 288 rows/day per station, roughly 105 GB/year at 10,000
      stations.
- [ ] Monthly partitions, a BRIN index on time, a btree on (`station_id`,
      `ts`). Timescale optional.
- [ ] Station table keyed by the server station ID with the current device key
      (`model:id:channel`), re-pair history, owner, selected fields, altitude
      and wind height, relay owner flag (`device` or `server`), exact location
      (server only), and the fuzzed location for the front end.
- [ ] Raw columns are never overwritten; derived consensus and QC flags are
      separate columns or a separate table.
- [ ] Device-side migrations mirror this schema closely enough that history
      syncs cleanly (On-device storage).
- [ ] Retention job: drop or downsample partitions by tier (Retention tiers).

## 5. Update manifest service on object storage

Source: Auto-update and rollback; Build, release and image pipeline.

- [ ] Object storage bucket for update bundles and manifests, fronted so
      10,000+ devices with jittered fetches do not hit GitHub rate limits.
- [ ] Manifest format: expiry, monotonic version, per-channel staged rollout
      percentages (1%, 10%, 100%), bundle URLs, per-chunk hashes, signature
      chain (release key signed under the root delegation), optional signed
      rollback authorization.
- [ ] Rollout controller: advance stages on success, auto-halt on failure
      rate, manual halt switch.
- [ ] Separate channels or flags for app-only bundles (allowed on LTE) and OS
      image bundles (Ethernet or Wi-Fi only).

## 6. Signing key custody in Infisical

Source: Auto-update and rollback (approved CI design); ADR 0007.

- [ ] Infisical machine identity for the `wxalerts/pws-gateway` release
      workflow, OIDC-bound to the GitHub Environment (audience, subject,
      repository and environment claims), short token TTL. Until this exists
      the sign job in the public repo stays disabled.
- [ ] Online release signing key stored in Infisical; alert on any read outside
      the release workflow.
- [ ] Offline root key and backup root key generated and stored offline; their
      public halves committed under `keys/` in the public repo; a signed
      delegation to the release key published with the manifest.
- [ ] Command-signing Ed25519 key, separate from the release key, used only by
      the API or ingest worker when publishing to `pws/{device_id}/cmd/#`; its
      public half committed under `keys/`.
- [ ] Open: can Infisical KMS sign without the key reaching the runner?

## 7. Relay: CWOP plugin and group relay

Source: Relay plugin topology.

- [ ] Rust relay worker implementing the plugin interface from the shared
      specification (`configure`, `validate`, `map`, `send`, `health`) and
      passing the golden test vectors in the public repo.
- [ ] CWOP plugin for server-side group relay once MADIS approval is granted;
      honors the per-station relay owner flag so a device-relayed station is
      never sent twice.
- [ ] Per-station, per-destination opt-in stored encrypted; delivery tracking
      and dead-letter per destination.
- [ ] Open: MADIS bulk route, CWOP status after the MesoWest sunset,
      attribution rules, APRS-IS passcode handling for non-ham IDs.

## 8. QC worker

Source: Cross-station QC and averaging.

- [ ] Worker comparing each PWS against nearby stations of all sources in the
      PostGIS database (km scale) for outlier flags and bias estimates; writes
      derived values, never raw.
- [ ] Weak-RSSI and similar signals surfaced as QC flags (there is no
      ownership attestation).

## 9. Dashboard pages (WxAlerts front end)

Source: PWS dashboard.

- [ ] "Personal Weather Station" nav item, private to the owner.
- [ ] Station list: online/offline, last report, version, update state,
      carrier and RSSI.
- [ ] Station detail: current values, history charts bounded by retention
      tier, sensor list, signal telemetry, GPS, modem stats, relay status per
      destination.
- [ ] Location fuzzed within 1 km on the front end; exact location never
      leaves the server.
- [ ] Claim page (code entry) and unlink action.
- [ ] Retention upsell hook; Flutter component later.

## 10. Policy

- [ ] Privacy policy addendum covering device telemetry, exact location
      handling and relay destinations, built before launch.
