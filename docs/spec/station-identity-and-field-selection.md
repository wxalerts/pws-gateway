# Station identity and field selection

## Goal

User picks which stations to ingest, and which fields from each. Different
stations report different values (temperature, humidity and pressure on one,
wind, rain and temperature on another).

## Design

- Stable station key = model + ID + channel (`model:id:channel`), with a
  re-pair flow. Many sensors re-randomize their ID on battery change.
- Per-station field checklist, with sensible defaults per model.
- Two Pis hearing the same sensor: server-side dedupe on station key plus
  timestamp window.
- Neighbor stations may appear in the list. Default to unselected. Only
  selected stations are stored or uplinked.

## Decision (Nov 16)

- No ownership attestation, and no wording of that kind in the UI or docs. Only
  the stations and fields the user selects are stored, uplinked or relayed.

## Open

- Naming and photo/notes per station in the UI.

## Implemented

- Any number of stations may be selected. Each selected station has a field
  checklist (`stations[key].fields`, canonical name to on or off). A missing
  key means the model default (`mapping.DefaultFields`), and a config written
  before field selection has no map at all, so it keeps working unchanged.
  `config.Station.EffectiveFields` is the single place the rule lives.
- `include_raw` passes decoder fields that have no canonical name through
  under `raw` in the uplink. When it is not set, it is off for a station that
  is not selected and on once the station is selected. An explicit value
  always wins.
- Data kept for stations that are not selected: discovery tracking (key,
  model, fields seen, signal, last seen) and the recent readings the live
  charts use. The recent readings stay in RAM only and are lost at restart.
  Nothing is written as a rollup, uplinked or relayed for an unselected
  station. The station list itself is saved so a known station is still
  listed after a restart.
- For a selected station only the effective fields are aggregated and stored.
  Deselecting a station stops new rollups; history already stored is not
  deleted. A field unticked later is dropped from uplink messages even if it
  was stored earlier.
- The API (`GET /api/stations`, `PUT /api/stations/{key}`) carries
  `fields_available`, `raw_fields`, `fields_default`, `fields` (effective) and
  `include_raw`. The station page has a "Data to use" card for it.
