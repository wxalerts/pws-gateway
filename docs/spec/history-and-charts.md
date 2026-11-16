# History and charts

## Goal

Multi-day and multi-month history on the web UI.

## Design

- Local: SQLite rollups, user-set cadence (minimum 10 minutes), range picker,
  per-field overlays.
- Server side: the same history appears on the WxAlerts PWS dashboard, limited
  by retention tier.
- Export CSV from both.
- Aggregations follow the same rules as the uplink (vector wind, rain deltas).

Updated 2026-11-16 in repo: local charts use uPlot, served from the Go binary
with the rest of the React SPA (see [Local web UI](local-web-ui.md)).
