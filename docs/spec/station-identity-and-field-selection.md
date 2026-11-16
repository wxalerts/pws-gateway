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
