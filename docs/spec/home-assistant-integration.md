# Home Assistant integration

## Goal

The Pi shows up in Home Assistant, at minimum as a new device on the same
network.

## Design

- Preferred: the Pi advertises over mDNS. The existing wxalerts-homeassistant
  HACS integration auto-discovers it (zeroconf manifest entry) and creates a
  device with all sensors.
- Alternative: UI field for an HA MQTT broker, and the Pi publishes MQTT
  discovery topics. `rtl_433` already ships an HA discovery script to borrow
  from.
- Local-only path works without WxAlerts connectivity.

## Repo notes (2026-11-16)

- The mDNS service name and the HA entity naming are not fixed by the spec.
  They must match whatever the wxalerts-homeassistant integration (a separate
  repository) declares in its zeroconf manifest; coordinate the name there
  before building M9.
- Home Assistant integration is the last build-order item (M9).
