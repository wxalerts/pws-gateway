# Milestones

Nine milestones following the build order in `docs/spec/README.md`. Each
acceptance test is a checkable statement. A milestone is done when every test
passes in CI or, for the ones marked hardware, on the test Pi with the result
recorded in the pull request.

Legend:

- **Hardware**: needs the Pi 4 (with LTE modem) on the owner's LAN, an SDR, or
  an I2C sensor. Everything else runs in `make test` with no hardware.
- **Private repos**: needs changes in the private WxAlerts repositories (see
  `docs/server-integration.md`). Those parts are not blocked on this repo, but
  the milestone is not done until both sides pass.

| Milestone | Hardware | Private repos |
| --- | --- | --- |
| M1 Repo, CI, schemas, platform stubs | no | no |
| M2 Collector, SQLite, supervisor, discovery | optional (one SDR for a smoke run) | no |
| M3 Local web UI | no | no |
| M4 Uplink and outbox | no | no |
| M5 Claim flow and ingest worker | no | yes |
| M6 Updater, manifest, rollback, network | yes (Pi 4; Pi 3B+ when A/B is resolved) | yes (manifest service) |
| M7 Modem, GPS, RTC, BMP280 | yes (Pi 4 with LTE modem; GPS, DS3231, BMP280 when attached) | no |
| M8 CWOP plugin and dashboard pages | no | yes (server plugin, dashboard) |
| M9 Retention tiers and Home Assistant | no | yes (retention job) |

## M1: Repo, CI, JSON Schemas, platform adapter stubs

Being built today (2026-11-16).

Goal: a public repository that builds, lints and tests on every pull request
with no secrets, carries the contract as JSON Schemas, and fixes the platform
adapter boundary before any board-specific code exists.

Scope: repository skeleton, `go.mod`, `Makefile`, `.github/workflows/`,
`schemas/`, `internal/platform/` interface plus a `fake` implementation,
`internal/contract/` types generated or hand-written from the schemas, the
`docs/` tree, LICENSE, NOTICE, CONTRIBUTING, AGENTS.md.

Deliverables: green CI on `main`; `schemas/*.json` for `obs`, `tel`, `update`,
`event`, `cmd`, `ack` and the device config file; example payloads under
`schemas/examples/`; a disabled sign job; `docs/` as listed in
`docs/spec/README.md`.

Acceptance tests:

- [ ] `make test` passes on a clean checkout with only Go and Node installed,
      with no SDR, no Pi and no network access beyond module download.
- [ ] `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/pwsgw` succeeds on
      an amd64 host and the result is a static binary (`file` reports
      "statically linked").
- [ ] Every example payload in `docs/contract/mqtt-v1-draft.md` has a copy
      under `schemas/examples/` and validates against its schema; a copy of the
      `obs` example with `seq` removed fails validation; a copy with an
      additional unknown top-level field `x_future` passes (additive changes
      are free).
- [ ] A `cmd` example with `type: "shell"` fails schema validation, because
      `type` is an enum of the seven documented command types.
- [ ] `internal/platform` exposes an interface covering at least: board
      identity, under-voltage and throttling flags, USB device enumeration,
      I2C enable, boot slot and rollback marker, network manager handle. A
      `fake` implementation satisfies it and is the one used by all tests.
- [ ] A test (or lint rule) fails if any Go file outside `internal/platform/`
      references `/sys/`, `/proc/`, `vcgencmd` or `/boot/`.
- [ ] The build workflow runs on pull requests from forks with
      `permissions: contents: read` and no `secrets.*` reference. The sign
      workflow references a GitHub Environment with required reviewers, is
      restricted to tags, and is disabled (`if: false` or equivalent) with a
      comment pointing at the Infisical identity that must exist first. All
      actions are pinned by commit SHA.
- [ ] `gofmt -l .` prints nothing and `golangci-lint run` reports no issues.

## M2: Collector, SQLite, rtl_433 supervisor, station discovery

Goal: turn `rtl_433` JSON lines into canonical SI samples, discover stations,
roll samples up into 5-minute buckets and persist rollups to SQLite, all driven
by recordings.

Scope: `internal/collector/` (supervisor, line parser, mapping, discovery,
aggregation), `internal/store/` (SQLite schema, migrations, batched flush),
`testdata/*.jsonl`, `tools/simsensor`.

Deliverables: at least five decoder mappings with recordings (Acurite 5n1,
Ambient/Fine Offset WH-series, LaCrosse, Oregon Scientific, Bresser or
similar); the discovery filter; the aggregation functions; the SQLite schema
version 1.

Acceptance tests:

- [ ] Given `testdata/acurite-5n1.jsonl` replayed at 10x, the station list
      shows `Acurite-5n1:1234:A` after the third consistent sighting and not
      before.
- [ ] Given a recording where one line reports an implausible value
      (temperature 250 C) for an otherwise unseen station, no station is
      listed for it.
- [ ] A `temperature_F` field from `rtl_433` is stored as `temp_c` in degrees
      Celsius; `wind_avg_km_h` becomes `wind_speed_ms`; a decoder field with no
      canonical mapping appears unchanged under `raw`.
- [ ] Given seven samples in one 5-minute bucket, `temp_c` carries `avg`,
      `min` and `max` equal to the arithmetic values; `wind_dir_deg` samples of
      350 and 10 yield `vec: 0` (not 180); a rain counter going 12.3, 12.7,
      0.4 yields `sum: 0.8` (reset handled, never negative); `wind_gust_ms`
      is the maximum; `battery_ok` is the last value; `n` is 7.
- [ ] Raw samples are never written to the on-disk database. With a fake
      clock, exactly one write transaction happens per 15 minutes, and
      `PRAGMA journal_mode` is `wal` and `PRAGMA synchronous` is `NORMAL` on
      the open connection.
- [ ] Before time sync (fake time source reports unsynced), samples are held
      with monotonic timestamps and nothing is persisted with a wall-clock
      time; after the fake source reports synced with a known offset, the
      held samples are stamped using that offset and persisted.
- [ ] Opening a database created at schema version 0 migrates it to the
      current version and records the version; opening a database with a
      higher version than the binary knows refuses to start with a clear
      error.
- [ ] A station that is not selected produces no rollup rows; selecting it
      starts rollups from the next bucket.
- [ ] Killing the fake `rtl_433` process makes the supervisor restart only
      that radio with exponential backoff, and the restart is counted in the
      per-radio health that will feed `tel.sdr[]`.

## M3: Local web UI

Goal: every v1 setting and view available in a browser from the Pi, with
appliance-grade security, served by the Go binary.

Scope: `web/` (React SPA, uPlot), `internal/web/` (HTTPS server, auth, API
routes), first-boot setup, station and radios pages, live readings, history
with CSV export, Pi health, logs.

Deliverables: the pages listed in `docs/spec/local-web-ui.md` except
networking, modem/GPS, update and Home Assistant (those come with M6, M7, M9
and show "not configured" placeholders until then).

Acceptance tests:

- [ ] The built SPA is embedded with `go:embed`; `GET /` over HTTPS returns the
      SPA with a self-signed certificate on first boot; plain HTTP is either
      redirected or refused.
- [ ] Before the admin password is set, every API route except the setup route
      returns 401 and the SPA shows only the setup page; after it is set, the
      setup route returns 410.
- [ ] Repeated failed logins from one client are rate limited (the numbers are
      recorded in `docs/conventions.md` when chosen) and the limit is
      covered by a test.
- [ ] The station page lists discovered stations with model, ID, channel,
      last seen, RSSI, battery flag and reported fields, and toggling a
      station or a field persists across a daemon restart.
- [ ] The Radios page lists dongles from the fake platform adapter with USB
      path, serial, tuner and role; assigning the role `rtl_433` with band
      915 persists; two dongles with the same serial show the duplicate
      serial warning and are identified by USB path.
- [ ] The history endpoint returns rollups at the configured cadence, rejects a
      cadence under 10 minutes with 400, and the CSV export returns a header
      row plus one row per bucket for the requested range.
- [ ] When the fake platform adapter reports an under-voltage flag or a USB
      reset, the UI shows the powered hub warning.
- [ ] `npm run lint` and the web test suite pass; the dependency list contains
      uPlot and no Grafana, ECharts or AGPL component.

## M4: Uplink and outbox against a mock broker

Goal: a device that publishes contract-valid messages, survives broker
outages, and accepts only correctly signed commands.

Scope: `internal/uplink/` (MQTT client, jitter, outbox, replay, command
verification, ack), `tools/mockbroker` or an embedded broker in tests,
`tools/fleetsim` skeleton for 10,000 simulated clients.

Deliverables: all device-to-server topics implemented; command handling for
`config`, `reboot`, `bundle`, `update-check`, `resync`, `diag`; outbox with the
proposed caps; schema validation of every published message in tests.

Acceptance tests:

- [x] On connect the device publishes retained `online` on `status`, sets an
      LWT of `offline`, and every message it publishes validates against the
      matching schema in `schemas/`.
- [ ] The uplink offset for a device ID is deterministic, lies in [0, 900) s,
      and two different IDs from a sample of 1,000 are spread with no more than
      1% landing in any one 10 s slot.
      Not ticked: the offset is deterministic and within [0, 900) s (tested),
      but 1,000 ids over 90 ten-second slots average 11.1 per slot, so "no
      more than 1% in any slot" cannot hold for any mapping. The test asserts
      at most 2.5% per slot. Decide the intended bound, then tick.
- [x] With the fake time source unsynced, no `obs` message is published and a
      `time_unsynced` event is published once; after sync, buffered buckets go
      out.
- [x] With the broker unreachable for a simulated 2 hours, the outbox holds
      every 5-minute bucket for each selected station; on reconnect, replay
      sends at most 20 buckets per message, no faster than the rate decided in
      the spike (see the contract's Open section), with `seq` strictly
      increasing.
- [x] When the outbox exceeds its cap, the oldest `tel` and `event` entries
      are dropped first, then the oldest `obs` buckets, and no bucket from the
      last 24 hours is ever dropped; an `outbox_overflow` event is published
      once per overflow episode.
- [x] A backlog larger than 64 KB is split into several messages at bucket
      boundaries and each message is under 64 KB.
- [ ] A `config` command with a valid signature, matching `device_id`, future
      `exp` and unseen `id` is applied through commit-confirm and acked on
      `ack/{id}` with `ok: true`. Commands with a bad signature, another
      device's ID, a past `exp`, or a replayed `id` are each rejected, not
      applied, and never acked as success.
      Not ticked: bad signature, other device, past `exp` and replayed `id`
      are tested and the valid command is applied and acked (over a real MQTT
      connection), but the patch goes in through `config.Store.ApplySections`
      (atomic, same validation as the web API) without the commit-confirm
      rollback. Remote commit-confirm needs the reconnect confirm path.
- [x] A message on `cmd/shell` or any undocumented type is rejected before
      signature verification and logged; no code path exists that executes a
      string from a command body (asserted by a test that the `diag` allowlist
      is a closed enum).

## M5: Back-end claim flow and ingest worker

Private repos. The device side of the claim flow is in this repo; the API,
Authentik integration, EMQX configuration and the Rust ingest worker are in the
private WxAlerts repositories.

Goal: a real device claims through a WxAlerts account, receives per-device
credentials, connects to EMQX, and its observations land in PostGIS.

Scope here: `internal/claim/` (device code display, polling, credential
storage), the first-boot claim page in the UI. Scope in the private repos:
see `docs/server-integration.md`.

Acceptance tests:

- [ ] Against a mock claim API, the device displays a short code, polls at the
      interval the API specifies, and on approval stores the MQTT credentials
      in a file with mode 0600 owned by the daemon user; the user's WxAlerts
      password never appears in any request the device makes.
- [ ] Credentials never appear in any MQTT payload, log line or support
      bundle (a test greps the bundle and the captured log output for the
      credential value).
- [ ] After the owner revokes the device, the next connect attempt is refused
      and the UI shows the device as unclaimed with a new code.
- [ ] (Private) With two devices' credentials, a publish to the other device's
      `pws/{id}/obs` is refused by the EMQX ACL, and a subscribe to the other
      device's `cmd/#` is refused.
- [ ] (Private) The Rust worker on `$share/ingest/pws/+/obs` validates against
      the `schemas/` files from this repository (vendored at a pinned commit),
      rejects an invalid payload with a metric increment, and writes a valid
      one to PostGIS; sending the same `(device_id, seq)` twice stores one
      row.
- [ ] (Private) EMQX connect and disconnect events update the device presence
      in the registry within 10 s.

## M6: Updater, manifest, rollback and network manager

Hardware (Pi 4 now; Pi 3B+ once the A/B mechanism is resolved) and private
repos (manifest service). Starts only after the A/B question for the 3B+ has a
decision, per the build order.

Goal: a device that updates itself from a signed manifest, rolls back on a
failed health check, and never strands itself through a network change.

Scope: `internal/update/` (manifest fetch, signature chain, ranged download,
staging, health check, state reporting), `internal/netcfg/` (NetworkManager
over D-Bus through the platform adapter, commit-confirm, AP fallback), the
networking and update pages in the UI.

Acceptance tests:

- [ ] A manifest signed by the release key under a valid root delegation is
      accepted; a manifest with a past expiry, a lower version than the
      installed one (without a rollback authorization), or a signature from an
      unknown key is rejected, and each rejection is a distinct `update`
      state or event.
- [ ] The device pins two root public keys; rotating to a release key
      delegated by the backup root works without any other change on the
      device.
- [ ] A download interrupted at 50% resumes with a ranged request from the
      byte it stopped at; a chunk whose hash does not match is re-fetched and
      the download succeeds; after the configured number of failures the
      state becomes `deferred`, not `failed`.
- [ ] When `tel.net.link` is `lte`, an OS image update is deferred and an app
      update proceeds.
- [ ] (Hardware, Pi 4) A staged update whose post-boot health check fails
      (MQTT connect blocked in the test) rolls back automatically, the
      `update` topic shows `rolled_back`, and a `rollback` event is published.
- [ ] A network change applied through the UI without the browser confirming
      within 90 s is reverted (fake NetworkManager and fake clock in unit
      tests; confirmed once on hardware by changing the Pi's static IP from
      a browser and not confirming).
- [ ] (Hardware) With no known network reachable, the Pi brings up the setup
      hotspot and the setup page is reachable on it.
- [ ] An update outside the configured update window is not started; one
      inside the window is.

## M7: Modem, GPS, RTC and BMP280

Hardware: the Pi 4 with LTE modem on the owner's LAN; GPS, DS3231 and BMP280
when attached. Unit tests use fakes for ModemManager, gpsd and the I2C bus.

Goal: LTE and GPS telemetry on the dashboard, GPS and modem time as chrony
sources, and I2C sensors usable from the UI.

Scope: `internal/platform/` modem, GPS and I2C implementations;
`internal/collector/` BMP280 station source; the modem/GPS and I2C pages in
the UI; chrony configuration.

Acceptance tests:

- [ ] With the fake ModemManager reporting a registered LTE modem, `tel.modem`
      carries `carrier`, `mcc_mnc`, `reg`, `tech` and `rssi_dbm`; with no
      modem, `tel` has no `modem` key.
- [ ] (Hardware) On the Pi 4 the real modem's carrier and RSSI appear in `tel`
      and on the local modem page, and `tel.net.link` switches to `lte` when
      the Ethernet cable is pulled.
- [ ] With the fake gpsd reporting a 3D fix, `tel.gps` carries `fix: 3`,
      `sats`, `lat`, `lon` and `alt_m`; `tel.time.src` becomes `gps` when
      chrony selects it.
- [ ] The I2C scan page, against a fake bus with devices at `0x68` and `0x76`,
      identifies a DS3231 and distinguishes BMP280 (chip ID `0x58`) from
      BME280 (chip ID `0x60`).
- [ ] A detected BMP280 appears as a station with its own key and reports
      `pressure_station_hpa` only; no `temp_c` is produced for it.
- [ ] Enabling I2C from the UI goes through the same commit-confirm,
      rollback-safe boot config path as other boot changes and is covered by
      the same test.
- [ ] With a DS3231 present, fake-hwclock is disabled and the oscillator-stop
      flag is shown on the time page and reported in telemetry.

## M8: CWOP plugin and dashboard pages

Private repos for the server-side plugin and the dashboard. The Pi-side plugin,
the shared specification and the golden test vectors are in this repo.

Goal: owners can relay to CWOP either from the Pi with their own credentials or
through WxAlerts, never both for the same station, and see their stations on
the WxAlerts dashboard.

Scope here: `docs/relay/` specification (plugin interface, sea-level pressure,
10 m wind, CWOP packet format) with `testdata/relay/*.json` golden vectors;
`internal/relay/cwop/`; the relay page in the UI. Scope in the private repos:
Rust plugin against the same vectors, dashboard list and detail pages.

Acceptance tests:

- [ ] Given a golden vector with station pressure 1008.3 hPa at 31 m altitude
      and 18.2 C, the Go implementation returns the expected sea-level
      pressure within 0.1 hPa; the same vector file passes in the Rust
      implementation.
- [ ] Given wind 2.1 m/s measured at 4.5 m, the 10 m normalization matches the
      golden vector in both implementations.
- [ ] Given the sample `obs` bucket from the contract, the generated CWOP APRS
      packet string equals the golden string byte for byte.
- [ ] The Pi-side plugin sends nothing while time is unsynced, and the CWOP
      credentials never appear in any MQTT payload, log line or support bundle.
- [ ] A station with relay owner `device` is never relayed by the server, and
      one with relay owner `server` is never sent by the Pi (asserted on both
      sides against the same fixture).
- [ ] Relay delivery state (last success, last error) is visible on the local
      relay page for the device-owned case.
- [ ] (Private) The dashboard lists the owner's devices with status, last
      report, version, update state, carrier and RSSI, and the station detail
      page shows the location fuzzed within 1 km; a request for another
      owner's device returns 404.

## M9: Retention tiers and Home Assistant integration

Private repos for the retention job. Home Assistant is device-side.

Goal: the Pi appears in Home Assistant on the local network without WxAlerts,
and the server enforces retention by tier.

Scope here: mDNS advertisement, optional MQTT discovery publisher to a
user-configured broker, the Home Assistant page in the UI. Scope in the
private repos: retention job, tier checks, downsampling.

Acceptance tests:

- [ ] The daemon advertises an mDNS service whose type matches the zeroconf
      entry in the wxalerts-homeassistant integration's manifest (name agreed
      before this milestone starts), and the advertisement includes the device
      ID and firmware version as TXT records.
- [ ] With an HA MQTT broker configured in the UI, the daemon publishes a
      Home Assistant discovery config for each selected field of each selected
      station, and a state update per bucket; disabling a field removes its
      discovery config.
- [ ] Both paths work with the WxAlerts uplink unreachable (mock broker down).
- [ ] (Private) The retention job drops partitions older than the free window
      for free-tier owners and keeps them for paid owners; where downsampling
      is configured, hourly rollups exist before the raw partition is dropped.
- [ ] (Private) The dashboard shows the retention limit for the owner's tier
      and the upgrade hook.
