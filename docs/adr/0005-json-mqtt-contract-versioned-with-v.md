# ADR 0005: JSON MQTT contract versioned with `v`, additive changes free

Status: accepted 2026-11-16.

## Context

Device and server are built by different people in different languages (Go in
public, Rust in private) and will be upgraded at different times. A fleet of
10,000+ devices will never all run the same version. The wire format must be
easy to inspect on the broker, easy to validate on both sides, and easy to
extend without a coordinated upgrade. Bandwidth is modest: a 15-minute batch is
a few KB per station, so compression is not needed in v1.

## Decision

- Payloads are JSON, UTF-8, uncompressed, with SI units and RFC 3339 UTC
  timestamps.
- Every payload carries an integer `v`. During development `v` is 0 and
  breaking changes are allowed. The contract freezes as `v: 1` only after
  verified testing, and nothing ships to real stations before that.
- Additive changes (new optional fields, new enum values the receiver can
  ignore) do not bump `v`. Receivers ignore unknown fields. The server accepts
  `v` and `v-1`.
- Anything that removes or renames a field, changes a type or a unit, or
  changes the meaning of an existing field bumps `v`.
- The normative form is the JSON Schemas in `schemas/`; `docs/contract/` is the
  prose. Both sides validate against the same schema files.
- Delivery is at-least-once (QoS 1); the server dedupes on `(device_id, seq)`
  and `(station key, bucket start)`.

## Consequences

- A device can add telemetry (for example `radio_id` and `driver` on
  `tel.sdr[]`) and old servers keep working; a server can start reading a new
  field as soon as devices send it.
- Schemas must allow additional properties, which means a typo in a field name
  is not caught by validation; tests on example payloads cover that instead.
- The server must carry two versions of parsing code during any transition.
  Bumps should be rare.
- Compression (zstd) is deferred; if added, it is negotiated additively, not
  as a version bump (see `docs/out-of-scope-v1.md`).
- Human-readable payloads make the broker, the support bundle and the mock
  broker in tests easy to debug.
