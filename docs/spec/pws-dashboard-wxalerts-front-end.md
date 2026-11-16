# PWS dashboard (WxAlerts front end)

## Goal

A Personal Weather Station nav item showing the owner's connected stations. Tap
one for in-depth detail.

## Design

- Private to each owner. Station locations are fuzzed on the front end.
- Station list: status (online/offline), last report, version, update state,
  carrier and RSSI.
- Station detail: current values, history charts, sensor list, signal telemetry,
  GPS, modem stats, relay status per destination.
- Same components in the Flutter app and the web front end.
- Upsell hook: longer data retention.

## Decisions (Nov 16)

- Web only at launch. A Flutter component in the WxAlerts app comes later.
- Private to each owner.
- Station locations on the front end are fuzzed with a 1 km radius. Exact
  location stays server-side.
- One Pi per free account.

## Repo notes (2026-11-16)

- The dashboard is part of the private WxAlerts front end, not this repo. It is
  listed in `docs/server-integration.md`. The data it shows comes entirely from
  the `tel`, `update`, `status` and `obs` topics defined in the contract, so
  any new dashboard field starts as a contract change here.
