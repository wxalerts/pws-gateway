# WxAlerts PWS Gateway specification

Snapshot of the Notion hub page "WxAlerts PWS Gateway (Personal Weather
Station)" and its 23 feature pages, taken 2026-11-16 (Notion last edited
2026-11-16 16:08 UTC). From this commit on, the copy in this repository is
authoritative and Notion is a historical record. Changes to the spec are pull
requests against `docs/`.

Six decisions made on 2026-11-16 after the Notion text was written are applied
throughout and marked with "Updated 2026-11-16 in repo" notes where they
changed a page. They are listed under Decisions below.

## Purpose

Open-source Raspberry Pi plus RTL-SDR gateway that receives 433/915 MHz
personal weather stations (the kind that report to Weather Underground), stores
data locally, and uplinks to WxAlerts over MQTT every 15 minutes. WxAlerts then
relays to NWS (via CWOP) and other destinations the owner chooses.

## Why

Weather Underground and the vendor clouds do not feed NWS. The only NWS path for
PWS data is CWOP to MADIS (MADIS forwards to NWS every 15 minutes). CWOP is
separate from Weather Underground.

## Feature pages

| Page | One line |
| --- | --- |
| [MQTT contract (v1 draft)](mqtt-contract-v1-draft.md) | Pointer to `docs/contract/mqtt-v1-draft.md`: topics, payloads, signed commands, outbox policy. |
| [Device claim and MQTT auth](device-claim-and-mqtt-auth.md) | Device-code claim through a WxAlerts account, per-device EMQX credentials and ACL. |
| [RF capture and station discovery](rf-capture-and-station-discovery.md) | Supervised `rtl_433`, all 258 decoders, a station is listed after 3 consistent sightings; Davis research. |
| [Radio roles and multi-SDR](radio-roles-and-multi-sdr.md) | Several dongles per Pi, each with a role; powered hub required for multi-device kits. |
| [Station identity and field selection](station-identity-and-field-selection.md) | Station key `model:id:channel`, re-pair flow, per-station field checklist, no ownership attestation. |
| [Units, altitude and sensor config](units-altitude-and-sensor-config.md) | SI internally, per-barometer altitude, per-anemometer height, calibration offsets kept apart from raw. |
| [Direct-attached sensors (DS3231, BMP280)](direct-attached-sensors-ds3231-bmp280.md) | I2C enable and scan from the UI, chip ID detection, BMP280 as a pressure-only station. |
| [Time sync and RTC](time-sync-and-rtc.md) | No observation is stamped or uplinked before NTP, LTE or GPS time; optional RTC. |
| [On-device storage and SD wear protection](on-device-storage-and-sd-wear-protection.md) | SQLite with WAL, raw in RAM, rollups flushed every 15 minutes, read-only root. |
| [Aggregation and 15-minute uplink](aggregation-and-15-minute-uplink.md) | 5-minute buckets, vector wind, rain deltas, persistent outbox. |
| [History and charts](history-and-charts.md) | Local history at a user-set cadence (minimum 10 min), uPlot charts, CSV export. |
| [LTE modem and GPS](lte-modem-and-gps.md) | ModemManager, gpsd and chrony; carrier, RSSI and fix in telemetry; LTE as failover WAN. |
| [Network configuration (failback-safe)](network-configuration-failback-safe.md) | NetworkManager with 90 s commit-confirm, AP fallback hotspot. |
| [Update manifest, slots and state machine](update-manifest-and-slots.md) | M6 implementation: manifest JSON, rejection codes, tryboot A/B, app slot. |
| [Auto-update and rollback](auto-update-and-rollback.md) | A/B root, signed manifest from object storage, staged rollout, approved split build/sign CI. |
| [Local web UI](local-web-ui.md) | React SPA served by the Go binary over HTTPS, admin password, Radios page, support bundle. |
| [Home Assistant integration](home-assistant-integration.md) | mDNS discovery by the wxalerts-homeassistant integration, or MQTT discovery to a local broker. |
| [Relay plugin topology](relay-plugin-topology.md) | Opt-in relay per station and destination; CWOP plugin on both Pi and server; shared spec and test vectors. |
| [Server ingest worker](server-ingest-worker.md) | Rust worker in the private repos on an EMQX shared subscription, writing to PostGIS. |
| [Cross-station QC and averaging](cross-station-qc-and-averaging.md) | Neighbors are for QC and bias, not accuracy; raw is never overwritten. |
| [PWS dashboard (WxAlerts front end)](pws-dashboard-wxalerts-front-end.md) | Owner-private station list and detail, web only at launch, 1 km location fuzz. |
| [Retention tiers](retention-tiers.md) | Short free window, paid longer history, server-side retention job. |
| [Protocol capture assist (v2)](protocol-capture-assist-v2.md) | Listen mode and capture upload for new decoders; v2, out of v1 scope. |
| [Build, release and image pipeline](build-release-and-image-pipeline.md) | GitHub Actions, pi-gen image, MIT with GPL tools as processes, Pi Imager distribution. |

Related documents outside this directory:

- `docs/contract/mqtt-v1-draft.md`: the wire contract. `schemas/` is normative.
- `docs/milestones.md`: M1 to M9 with acceptance tests.
- `docs/out-of-scope-v1.md`: what not to build in v1.
- `docs/hardware.md`: hardware requirements.
- `docs/conventions.md`: engineering conventions.
- `docs/server-integration.md`: checklist for the private WxAlerts repositories.
- `docs/adr/`: architecture decision records.

## Decisions (consolidated, as of 2026-11-16)

### Product and scope

- Hardware: Raspberry Pi only for launch. Pi 3B+ is the floor (64-bit OS,
  1 GB RAM). Pi 4 and 5 are supported. 64-bit only.
- Board-specific code sits behind a platform adapter, and the app ships as
  multi-arch containers, so other boards stay possible later.
- License: MIT, in the public GitHub repo `wxalerts/pws-gateway`, public from
  the first commit. CI and image builds on GitHub Actions. GPL tools
  (`rtl_433`, rtldavis) run as separate processes, and the image ships their
  source.
- Every user-facing setting is configurable from the local web UI. No Linux
  knowledge needed beyond flashing an SD card. Local UI security is required,
  same standard as Waypoint.
- A powered USB hub is required for multi-device kits. No USB spike.
- Expected fleet: 10,000+ stations. One Pi per free account.

### Implementation (added 2026-11-16)

- The Pi daemon is written in Go (`cmd/pwsgw`). The local web UI stays a React
  SPA, served by the Go binary through `go:embed`. Charts use uPlot. (ADR 0001)
- The server-side ingest worker is written in Rust, the first piece of a Rust
  migration of the WxAlerts API. It lives in the private WxAlerts repositories.
  This public repo holds only the device side plus the shared MQTT contract and
  JSON Schemas, which are the single source of truth for both sides. (ADR 0003)
- SQLite on the device through the pure-Go driver `modernc.org/sqlite`, so
  arm64 cross-compiles need no cgo. (ADR 0004)
- The split build/sign CI design is approved and being scaffolded; the sign
  job is disabled until the Infisical machine identity exists. (ADR 0007)
- Test hardware: a Pi 4 with an LTE modem is available on the owner's LAN for
  integration testing; an SDR is plugged in when needed. Hardware-free tests
  (replayed recordings, simulators) remain the baseline and the only thing
  `make test` depends on.

### Radio

- All 258 `rtl_433` protocols at launch.
- Multiple SDRs supported, with a role assigned to each (Radios page in v1,
  onboarding wizard in v2).
- Davis: the user chooses. A Davis dongle is dedicated to Davis, and in a
  single-dongle kit the Pi monitors only Davis. Recommendation: a separate
  optional module after launch, not in v1 scope.
- v2: listen mode for new protocols, and the onboarding wizard.

### Sensors and data

- Per barometer: user sets units and altitude. Per anemometer: user sets units
  and mounting height (normalize to 10 m).
- Averaging nearby sensors is for QC and outlier rejection, not accuracy gains.
  Store raw plus a separate derived value.
- Optional DS3231 RTC and BMP280 on I2C, with web UI help to enable and detect
  them.
- Time: wait for NTP or cellular time sync before stamping and uplinking
  observations.
- On-device storage: SQLite (WAL, batched commits). No Influx or Postgres on
  the Pi. Raw samples stay in RAM, rollups go to disk every 15 minutes, history
  cadence is user-set (minimum 10 minutes).
- No ownership attestation, and no wording of that kind anywhere in the UI or
  docs.

### Uplink and contract

- Devices claim through a WxAlerts account with a device-code flow and get
  per-device MQTT credentials.
- MQTT contract: JSON, no compression. Draft only (`v: 0`). It freezes as
  `v: 1` after verified testing, and nothing ships before that. (ADR 0005)
- All commands are signed (Ed25519, separate key). No shell command. Updates
  only come through the signed manifest. (ADR 0006)

### Updates and support

- Auto-update is mandatory, with automatic rollback and MQTT reporting. The Pi
  fetches from a WxAlerts manifest. The signing secret is held in Infisical.
  LTE links get app-only updates.
- Remote support: support bundle and allowlisted diagnostics at launch. A
  time-limited opt-in remote session later. No always-on mesh VPN. No remote
  shell of any kind.

### Relay

- All data is ingested to WxAlerts regardless of relay choices. Relay is
  opt-in per station, per destination.
- Relay uses a plugin topology. CWOP first, third parties on request.
- The CWOP plugin ships in the Pi image (user's own credentials, isolated mode)
  and in the back end (group relay once approved). The interface, formulas and
  packet format are shared as a specification with golden test vectors, since
  the two sides are Go and Rust.

### Server and dashboard

- Back-end observation store: the existing PostGIS database that already holds
  APRS, METAR and CWOP data.
- PWS dashboard is private to each owner. Web only at launch, Flutter later.
  Station locations on the front end are fuzzed with a 1 km radius.
- Retention: free tier gets a short window. Paid upgrade for additional
  storage, price to be determined.
- Privacy policy addendum is built before launch. Not blocking right now.

## Open items

Carried over from Notion, minus the two resolved today (CI signing design
approved; repo and org name is `wxalerts/pws-gateway`).

- Resolve A/B boot and rollback on a real Pi 3B+ before image work. Tryboot is
  unverified on the 3B+. (A Pi 4 is available for the tryboot path.)
- CWOP/MADIS: is there a bulk or organizational route? Confirm CWOP ingest
  status after the MesoWest gateway sunset. Check APRS-IS login and passcode
  handling for non-ham CWOP IDs.
- Davis module: read the rtldavis fork, and check its license, activity and how
  transmitter IDs are discovered.
- Outbox numbers: validate in the spike.
- Final canonical field list after the sensor test matrix.
- Free retention window length, and the paid storage price.
- v2 capture handling: retention, license terms, scrubbing.
- Is the data-loss window on power cut (up to one flush interval) acceptable?
- Can Infisical KMS sign without exposing the key to the runner?
- Verify Raspberry Pi Imager custom repository support and GitHub arm64 runner
  availability for public repos.
- mTLS in addition to username/password for MQTT?
- Device cap for paid tiers.
- Supported LTE modem list.
- Confirm the Davis dongle lock is per dongle, not per Pi.
- Station naming, photo and notes in the UI.
- Attribution rules per relay destination.
- Backpressure and replay handling in the ingest worker after a broker outage.

Found while converting (see the contract's Open section):

- Replay rate (1 msg/s) versus server rate limit (4 msg/min per device).
- Canonical JSON form for command signatures is not named.
- Per-field quality flags are mentioned on the Aggregation page but absent from
  the contract.

## Build order

1. Repo, CI, JSON Schemas, platform adapter stubs. (M1, in progress)
2. Collector, SQLite and `rtl_433` supervisor, driven by replayed recordings,
   with station discovery. (M2)
3. Local web UI. (M3)
4. Uplink and outbox against a mock broker. (M4)
5. Back-end claim flow and ingest worker. (M5, private repos)
6. Updater, manifest, rollback and network manager (after the A/B question is
   resolved). (M6)
7. Modem, GPS, RTC and BMP280. (M7)
8. CWOP plugin (shared specification and test vectors) and dashboard pages.
   (M8)
9. Retention tiers and Home Assistant integration. (M9)

Acceptance tests for each are in `docs/milestones.md`.

## Before development handoff

Status of the Notion checklist:

- Move the spec into the repo: contract, schemas, conventions file, milestones
  with acceptance tests. Done in this directory (schemas by a separate change).
- State what is out of scope for v1. Done: `docs/out-of-scope-v1.md`.
- Test without hardware: replayed `rtl_433` recordings, a sensor simulator, and
  a fleet simulator for 10,000 MQTT clients. Planned under `testdata/` and
  `tools/` (M2 and M4).
- Publish a hardware list. Done: `docs/hardware.md`.
- Fleet observability: version spread, update failure and deferral rates, halt
  switch. Server side, listed in `docs/server-integration.md`.
- Distribution: flashable image through Raspberry Pi Imager's custom repository
  option (verify). Open.

## Sources

- NWS ILN: CWOP. https://www.weather.gov/iln/cwop
- MADIS CWOP FAQ. https://madis.ncep.noaa.gov/faq_cwop.shtml
- CrowdQC+. https://www.frontiersin.org/journals/environmental-science/articles/10.3389/fenvs.2021.720747/pdf
- Met Office PWS QC (EUMETNET). https://www.eumetnet.eu/wp-content/uploads/2023/06/Crowdsourcing-PWS-Quality-Control-A1.05_MetOffice_Final_2022.pdf
