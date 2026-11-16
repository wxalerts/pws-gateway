# Relay plugin topology

## Goal

All data is ingested to WxAlerts regardless. Owners choose where WxAlerts relays
it. CWOP first. Third parties added on request.

## Design

- Server-side relay workers consume normalized observations from the ingest
  stream.
- Plugin interface: `configure(schema)`, `validate(config)`, `map(observation)`,
  `send(batch)`, `health()`. Each plugin declares required fields and rate
  limits.
- Per-station, per-destination opt-in. Config stored encrypted.
- Delivery tracking: last success, last error, retry with backoff,
  per-destination dead-letter.
- CWOP plugin: user enters CWOP ID (or callsign). Data goes to APRS-IS in CWOP
  packet format. Data appears in MADIS after the Wednesday following
  registration. CWOP is separate from Weather Underground.
- Relay uses normalized, unit-corrected values (sea-level pressure, 10 m wind).

## Update (Nov 16)

- The CWOP/MADIS plugin ships in two places: in the Pi image and in the
  WxAlerts back end.
- On the Pi: the user enables it and publishes to CWOP themselves with their
  own credentials. Credentials stay on the device and are never sent to
  WxAlerts. This works with no WxAlerts account, which gives the fully isolated
  mode.
- On the server: for users who have not enabled it locally, WxAlerts relays as
  a group once approval is granted. CWOP accepts stations with their own
  verified credentials regardless.
- Write the plugin interface and the normalization code (sea-level pressure,
  10 m wind) once, as a shared library used by both the Pi agent and the server
  worker.
- Each station has a relay owner flag, `device` or `server`, so the same
  observation is never sent to CWOP twice.
- The Pi-side plugin needs synced time before it sends. Check the APRS-IS login
  details and passcode handling for non-ham CWOP IDs when building it.

Updated 2026-11-16 in repo: the Pi daemon is Go and the server worker is Rust,
so a single shared library is no longer possible. The "write once" goal is kept
by sharing the specification instead of the code: the plugin interface, the
normalization formulas and the CWOP packet format are specified in this repo
together with golden test vectors (input observation, expected sea-level
pressure, expected 10 m wind, expected APRS packet). The Go implementation here
and the Rust implementation in the private repos both run the same vectors in
CI. The Build order item "CWOP plugin (shared library)" should be read this
way.

## Open

- Ask MADIS about bulk or organizational relay support.
- Confirm CWOP ingest status after the MesoWest gateway sunset.
- Attribution rules per destination.
- APRS-IS login and passcode handling for non-ham CWOP IDs.
