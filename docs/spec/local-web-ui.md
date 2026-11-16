# Local web UI

## Goal

Everything configurable from a browser. No Linux commands. Rich telemetry for
people who want to see everything the radio and the Pi are doing.

## Design

- Go backend plus React SPA, served from the Pi by the Go binary. Local admin
  password set at first boot. Works when WxAlerts is down.
- Pages: station list and selection, live readings, per-sensor signal telemetry
  (RSSI, SNR, frequency offset, decode rate, battery), Pi health (CPU temp,
  throttling flags, tmpfs usage, outbox depth), uplink status, update status,
  networking, modem/GPS, Home Assistant, logs.
- Custom charts with uPlot. Skip Grafana: it is AGPL.
- Spectrum/waterfall needs a second dongle or time-sharing. Without one, show
  channel occupancy from decoded packets.
- HTTPS with a self-signed cert. No port forwarding required.

Updated 2026-11-16 in repo: the Notion page said "FastAPI backend plus React
SPA" and "uPlot or ECharts". The backend is the Go daemon itself (`cmd/pwsgw`),
which serves the built SPA from `web/` through `go:embed`, so the Pi runs one
binary and no Python runtime. Charts use uPlot. See ADR 0001.

## Decisions (Nov 16)

- Local UI security is a requirement, same standard as the Waypoint project:
  password, HTTPS, rate limits on authentication, no unauthenticated routes
  other than first-boot setup.
- Radios page: lists detected dongles and assigns each a role (see
  [Radio roles and multi-SDR](radio-roles-and-multi-sdr.md)). The onboarding
  wizard is v2.
- Warns on under-voltage flags and USB resets and points the user to a powered
  hub. These checks live in the platform adapter.
- Web UI only on the Pi. Pi hardware only for launch, with board-specific code
  behind a platform adapter.
- Remote support actions live here: a support bundle button and allowlisted
  diagnostics, both through signed commands. A time-limited remote session
  comes later, opt-in. There is no remote shell of any kind (see
  `docs/out-of-scope-v1.md`).

## Open

- The exact rate-limit numbers for login attempts are not in the spec; pick
  them when building M3 and record them in `docs/conventions.md`.
