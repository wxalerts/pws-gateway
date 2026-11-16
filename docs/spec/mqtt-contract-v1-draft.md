# MQTT contract (v1 draft)

The full contract lives in [`docs/contract/mqtt-v1-draft.md`](../contract/mqtt-v1-draft.md)
and the normative machine-readable form is `schemas/`. This page is kept so
every Notion feature page has a counterpart under `docs/spec/`; it does not
duplicate the contract.

## Summary

- JSON over MQTT, topic prefix `pws/{device_id}/`, QoS 1, `v: 0` while in
  draft, freezes as `v: 1` after verified testing.
- Device publishes `status` (retained, LWT), `obs`, `tel`, `event`, `update`
  (retained) and `ack/{cmd_id}`. It subscribes only to `cmd/#`.
- Observations are 5-minute buckets per station, uplinked every 15 minutes with
  a per-device jitter derived from the device ID.
- All commands are Ed25519 signed. There is no shell or install command.
- Outbox: 14 days or 25 MB, oldest telemetry dropped first, last 24 hours never
  dropped.

## Open

See the Open section of the contract document. The repo copy also records two
internal inconsistencies found while converting (replay rate versus rate limit,
and per-field quality flags).
