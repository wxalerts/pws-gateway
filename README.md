# WxAlerts PWS Gateway

Open-source Raspberry Pi gateway for personal weather stations. An RTL-SDR
receives the 433 MHz and 915 MHz sensors that normally report only to a vendor
cloud, the Pi stores the readings locally, and every 15 minutes it uplinks to
WxAlerts over MQTT. WxAlerts can then relay the observations to the National
Weather Service through CWOP and to any other destination the owner opts into.

Status: pre-alpha. The MQTT contract is a draft (`v: 0`) and nothing ships to
real stations until it freezes as `v: 1` after verified testing.

## Why

Weather Underground and the vendor clouds do not feed the NWS. The only path
for personal weather station data into NWS systems is CWOP to MADIS, which is
separate from Weather Underground. This gateway gives station owners that path
with no Linux knowledge beyond flashing an SD card.

## What it does

- Receives every protocol `rtl_433` decodes (258 at launch) and lists the
  stations in range after three consistent sightings.
- Stores raw and rolled-up readings in SQLite with SD-card wear protection.
- Uplinks 15-minute buckets over MQTT with an offline outbox, and never stamps
  an observation until time is synced.
- Configures everything from a local web UI served from the Pi, with the same
  security standard as a network appliance: password, HTTPS, rate limits.
- Updates itself in the background with signed releases and automatic rollback.
- Supports several SDR dongles, each assigned a role.

## Hardware

Raspberry Pi 3B+ or newer, 64-bit OS only. Pi 4 and Pi 5 are supported. Multi
dongle kits need a powered USB hub. The full list, including the recommended
TCXO dongle, antenna, SD card and power supply, is in
[docs/hardware.md](docs/hardware.md).

## Repository layout

| Path | What lives there |
| --- | --- |
| `cmd/pwsgw` | The gateway daemon entry point |
| `internal/` | Go packages: platform adapter, collector, store, uplink, web |
| `web/` | React single-page app for the local web UI |
| `schemas/` | JSON Schemas for the MQTT contract and config files |
| `docs/` | Specification, milestones, conventions and decisions |
| `deploy/` | Container, compose and systemd files for the Pi image |
| `testdata/` | Replayed `rtl_433` recordings used by the tests |

## Developing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the toolchain, the conventions and
how to run the test suite without hardware. [AGENTS.md](AGENTS.md) is the
guide for AI coding assistants working in this repository; it applies to human
contributors too.

## License

MIT. See [LICENSE](LICENSE). `rtl_433` and other GPL tools run as separate
processes and keep their own licenses; see [NOTICE](NOTICE).
