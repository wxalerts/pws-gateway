# Engineering conventions

Rules that apply across the codebase. Each one comes from a spec page or an
ADR; the source is named so the rule can be traced. `AGENTS.md` carries the
short version.

## Units

- SI internally, always. Temperature in degrees Celsius, pressure in hPa, wind
  in m/s, rain in mm, distance in m, irradiance in W/m2, illuminance in lux.
  Field names carry the unit suffix (`temp_c`, `wind_speed_ms`) so a value can
  never be misread.
- Unit conversion happens only at the display edge (web UI, dashboard) and on
  relay to a destination that needs other units (CWOP). Nothing in `internal/`
  below the web and relay layers holds a non-SI value.
- The user's unit preference is a UI setting, not a data attribute.
- Source: Units, altitude and sensor config; MQTT contract conventions.

## Time

- All timestamps are UTC in RFC 3339 (`2026-11-16T14:15:03Z`). No local time
  in storage, payloads or logs.
- No observation is stamped with wall-clock time or uplinked until a trusted
  time source (NTP, LTE network time, GPS) has synced. Until then samples carry
  monotonic timestamps and are converted after sync using the measured offset.
- An RTC is provisional time, not a sync source for uplink.
- Bucket boundaries are aligned to the UTC 5-minute grid. The uplink cadence is
  15 minutes offset by a deterministic hash of the device ID.
- Source: Time sync and RTC; MQTT contract.

## Station keys and field names

- Station key format: `model:id:channel`, using the `rtl_433` model name, the
  decoded ID and the channel (empty channel allowed, the colons stay). Example:
  `Acurite-5n1:1234:A`. Davis stations (post-v1) use `davis:{model}:{tx_id}`.
- A re-pair maps a new key to an existing station on the server; the device
  never rewrites history under a different key.
- Canonical field names (v1): `temp_c`, `humidity_pct`, `pressure_station_hpa`,
  `wind_speed_ms`, `wind_gust_ms`, `wind_dir_deg`, `rain_mm`, `uv_index`,
  `light_lux`, `solar_wm2`, `lightning_count`, `lightning_km`, `battery_ok`.
  Adding one is a contract change (schema, contract doc, mapping, example,
  test).
- Field types and their aggregates:

| Type | Aggregates | Fields |
| --- | --- | --- |
| gauge | `avg`, `min`, `max` | `temp_c`, `humidity_pct`, `pressure_station_hpa`, `wind_speed_ms`, `uv_index`, `light_lux`, `solar_wm2`, `lightning_km` |
| gauge (max only) | `max` | `wind_gust_ms` |
| direction | `vec` (vector mean, degrees) | `wind_dir_deg` |
| counter | `sum` (delta of a cumulative counter, reset-safe, never negative) | `rain_mm`, `lightning_count` |
| flag | `last` | `battery_ok` |

- Decoder fields without a canonical mapping pass through under `raw`,
  unconverted, and are never aggregated.
- Source: MQTT contract; Station identity and field selection.

## Platform adapter boundary

- Everything board-specific or OS-specific lives in `internal/platform/`:
  reading `/sys`, `/proc`, `vcgencmd`, device trees, `/boot` config, USB
  enumeration, I2C, ModemManager, gpsd, NetworkManager over D-Bus, the boot
  slot and rollback marker.
- Nothing outside that package touches those paths or daemons. A test or lint
  rule enforces it.
- Every platform capability has a `fake` implementation that tests use. `make
  test` never needs a Pi.
- Source: hub decisions (platform adapter); Radio roles; Auto-update.

## Commands and remote access

- The device never executes a shell command from the server. There is no
  install command. The `diag` command is a closed allowlist (restart a service,
  re-run discovery, connectivity test, clear outbox) implemented as functions,
  not as strings passed to anything.
- Every server-to-device command carries an Ed25519 signature over the
  canonical JSON of `{id, type, device_id, exp, body}`. The device checks the
  `device_id`, the expiry, a replay cache of seen IDs, and the signature, in
  that order, and applies nothing that fails.
- The command-signing key is separate from the release-signing key. Public keys
  live under `keys/`; private keys never exist in this repo.
- Source: MQTT contract (Signed commands); ADR 0006.

## Signed everything

- Releases and update manifests are signed. Devices pin two root public keys
  and verify the chain to the online release key. Manifests carry an expiry
  and a monotonic version; stale or lower-version manifests are rejected unless
  a signed rollback authorization is present.
- Update bundles are fetched from object storage behind the manifest, with
  ranged resumable downloads and per-chunk hashes.
- CI: the build job gets no secrets; the sign job runs only on tags from the
  protected branch in a GitHub Environment with required reviewers, with
  Infisical OIDC. Actions are pinned by commit SHA.
- Source: Auto-update and rollback; ADR 0007.

## Storage

- SQLite through `modernc.org/sqlite` (pure Go, no cgo). One database file for
  rollups and config; `journal_mode=WAL`, `synchronous=NORMAL`.
- Raw samples stay in RAM. Rollups are written in one batched transaction every
  15 minutes. Nothing else writes to the SD card on a schedule.
- Schema is versioned; migrations are small, forward-only files, and the daemon
  refuses to open a database newer than it understands.
- The outbox is on disk, with the caps in the contract (14 days or 25 MB,
  oldest telemetry first, last 24 hours never dropped).
- Source: On-device storage; ADR 0004.

## Logging

- Structured logging with the standard library `log/slog`, JSON handler in
  production, text handler in development. Levels: `debug`, `info`, `warn`,
  `error`.
- Every log line that concerns a station or a radio carries `station` or
  `radio_id` as an attribute. Lines about a command carry `cmd_id`.
- Never log credentials, MQTT passwords, CWOP passcodes, exact GPS coordinates
  or IP addresses at `info` or above. A test greps captured output for the
  credential fixtures.
- Logs are volatile (journald in RAM) by the storage design; the support bundle
  is the way to collect them.

## Configuration changes: commit-confirm

- Any change that can strand the device (network, boot config, the config
  pushed by a signed `config` command) is applied with commit-confirm: apply,
  start a 90 s timer, revert unless confirmed. The web UI confirms when the
  browser can still reach the device; a command confirms with an explicit
  follow-up or is reverted.
- One implementation of commit-confirm, used by all three callers.
- Source: Network configuration (failback-safe); MQTT contract (Commands).

## Code and repository

- Go: version in `go.mod`; `gofmt` and `golangci-lint` clean. Web: Node 20 LTS,
  `npm run lint` clean.
- GPL tools (`rtl_433`, rtldavis) run as separate processes talking JSON over
  stdout. They are never linked in. `NOTICE` lists them.
- Tests never need hardware, a network or credentials. Recordings in
  `testdata/`, simulators in `tools/`.
- Writing style for comments, docs and commits: no em dashes, no en dashes, no
  emojis, no attribution trailers. Imperative commit subjects under 72
  characters with a body that says why.
- Source: AGENTS.md; Build, release and image pipeline.

## Values still to be fixed

These numbers are implementation choices the spec leaves open. Record them
here when they are chosen so they are not picked twice.

- Login rate limit for the local web UI (attempts per window, lockout).
- Reconnect backoff is specified (5 s to 5 min, exponential with jitter); the
  replay pacing after reconnect is open (see the contract).
- Canonical JSON algorithm for command signatures.
