# LTE modem and GPS

## Goal

Support USB GPS units and USB LTE modems. Feed GPS, time sync, carrier ID and
RSSI back via MQTT for the PWS dashboard.

## Design

- ModemManager (D-Bus) for LTE: signal, carrier name and MCC-MNC, registration
  state, data usage, network time.
- gpsd for USB GPS, chrony for time (GPS and modem network time as sources).
- LTE as failover WAN with route metrics. Small 15-minute batches keep data use
  low.
- Location from GPS or manual entry. Public display is fuzzed on the front end.
- Telemetry published each cycle: fix state, satellites, carrier, RSSI, time
  offset (`tel.modem`, `tel.gps`, `tel.time`).

## Repo notes (2026-11-16)

- A Pi 4 with an LTE modem is available on the owner's LAN for integration
  testing (milestone M7). Unit tests use a fake ModemManager and a fake gpsd
  feed; nothing in `make test` needs the modem.
- ModemManager and gpsd are talked to through `internal/platform/`, since they
  are OS-specific.

## Open

- Supported modem list (Quectel EC25 and Sierra are the likely starting
  points).
- Resolved: a powered USB hub is required for multi-device kits (SDR plus modem
  or GPS).

## Implementation notes (M7)

- ModemManager is read over the system D-Bus (`internal/platform/modemmanager`).
  `rssi_dbm` comes from the Signal interface, which the daemon switches on at
  the first read; the first figure appears one refresh period later.
- gpsd is read over its JSON socket on localhost. With no receiver attached,
  `tel` has no `gps` key; with no modem, no `modem` key.
