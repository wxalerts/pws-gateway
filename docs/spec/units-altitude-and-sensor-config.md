# Units, altitude and sensor config

## Goal

Correct, comparable values from every sensor.

## Design

- Store SI internally. Convert on display and on relay.
- Each barometer: user sets units and altitude. Compute sea-level pressure from
  station pressure.
- Each anemometer: user sets units and mounting height. Normalize to 10 m for
  relay and QC.
- Per-sensor calibration offsets (optional, stored separately from raw).
- Raw values are always preserved.

## Repo notes (2026-11-16)

- "User sets units" means the units the user reads and types in the web UI.
  The stored value and the MQTT payload are always SI (see
  `docs/conventions.md`).
- The device uplinks station pressure with `alt_m` and raw wind with
  `wind_height_m`. The sea-level and 10 m normalizations run on the server for
  the WxAlerts store, and on the device only for local display and the Pi-side
  CWOP plugin. Both implementations are tested against the same golden vectors
  (see [Relay plugin topology](relay-plugin-topology.md)).
