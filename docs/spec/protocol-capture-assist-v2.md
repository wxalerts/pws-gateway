# Protocol capture assist (v2)

Out of scope for v1. See `docs/out-of-scope-v1.md`. Kept here so the v1 design
leaves room for it (the `capture` radio role, the `capture` command type and
the `driver: capture` telemetry value are reserved in the contract).

## Goal

Help develop decoders for sensors `rtl_433` does not support. The user puts the
SDR in listen mode and sends a capture to WxAlerts for decoding.

## Design

- Explicit user opt-in per capture. Short fixed duration and a size cap.
- Prefer demodulated pulse data over long raw I/Q. The 433 MHz band is full of
  unrelated devices (remotes, key fobs, TPMS, door sensors), and a raw capture
  records other people's traffic.
- Upload over a presigned object-storage URL, referenced from an MQTT message.
- Workflow: user labels the capture (brand, model), WxAlerts or the community
  writes the decoder, and it goes upstream to `rtl_433` as a pull request.
- Listen mode takes over the dongle, so normal decoding pauses during a
  capture. The UI says so.

## Open

- Retention policy for captures.
- License terms for contributed captures.
- Whether to scrub captures server-side.
