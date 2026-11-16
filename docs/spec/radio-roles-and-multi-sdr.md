# Radio roles and multi-SDR

## Goal

Support more than one SDR on a Pi, and let the user say what each one does.

## Decisions

- Multiple SDRs are supported. Each one has an assigned role.
- Choosing Davis equipment dedicates a dongle to Davis. In a single-dongle kit,
  that means the Pi monitors only Davis.
- The onboarding wizard is a v2 item. In v1, roles are assigned manually on a
  Radios page in the web UI.

## Design

- Radios page lists every detected dongle with USB path, serial, tuner and
  assigned role.
- Roles: `rtl_433` (band: 315, 433.92, 868 or 915 MHz), `davis` (region and
  transmitter IDs), `capture` (v2 listen mode), or unassigned.
- Dongle identity is serial plus USB port path. Generic dongles often ship with
  identical serials. The UI offers to write a unique serial (EEPROM write, with
  a warning) and falls back to the USB port path.
- One supervised process per radio. A failure restarts only that radio.
  Per-radio health goes into telemetry (`tel.sdr[]`, with `radio_id` and
  `driver`; see the contract).
- The Davis role owns its dongle while hopping. Other dongles keep their own
  roles.
- Decision: a powered USB hub is required for multi-device kits (two or more
  SDRs, or SDR plus modem or GPS). Unpowered Pi ports flake out under that
  load, so no USB spike is needed.
- Pi 3B+ limits behind this: all USB ports share one USB 2 hub with Ethernet,
  and about 1.2 A total.
- The UI warns when it sees under-voltage flags or USB resets and points the
  user to a powered hub. These checks live in the platform adapter, since they
  are Pi-specific.

## Open

- Confirm the Davis lock is per dongle (assumed) and not for the whole Pi.
