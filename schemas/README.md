# MQTT contract schemas

This directory is the machine readable form of the MQTT contract between a
PWS gateway device and the WxAlerts server. The prose contract lives in
`docs/contract/` (and, while in draft, on the Notion page "MQTT Contract (v1
draft)"). When prose and schema disagree, fix one of them in the same pull
request; do not leave them apart.

The Go code in `internal/contract/` embeds these files (`schemas/embed.go`)
and validates every payload it sends or receives against them. The Rust
server worker and any other implementation should load the same files, so
there is exactly one copy of each schema in this repository.

## Files

| File | Message | Topic |
| --- | --- | --- |
| `common.schema.json` | shared `$defs` (device id, station key, timestamp, field types, canonical field list, signature) | none |
| `obs2.schema.json` | contract v2 observation record, one combined record per bucket (`v` 2) | `V2/{user_id}/PWS/{device_id}/obs` |
| `status.schema.json` | connection state, a JSON string `"online"` or `"offline"` | `pws/{device_id}/status` (retained) |
| `obs.schema.json` | observation buckets | `pws/{device_id}/obs` |
| `tel.schema.json` | device health | `pws/{device_id}/tel` |
| `event.schema.json` | immediate events | `pws/{device_id}/event` |
| `update.schema.json` | version and update state | `pws/{device_id}/update` (retained) |
| `cmd.schema.json` | signed server to device command | `pws/{device_id}/cmd/{type}` |
| `ack.schema.json` | command result | `pws/{device_id}/ack/{cmd_id}` |

Contract v2 (see `docs/contract/mqtt-v2.md`) changes only `obs`. The other
schemas are shared by both topic trees. `obs2.schema.json` is distinguished
from `obs.schema.json` by `v` (2 versus 0), not by its `$id` directory: the
`$id` generation segment stays `v0` for the whole file set, and a file for a
new shape gets a new file name.

Every property carries a `description`, so the schemas double as the field
reference. Numeric ranges are only constrained where the contract fixes them
(or where the quantity is bounded by definition, such as compass degrees and
latitude); everything else is unbounded and the description says so.

## `$id` convention

Each schema's `$id` is `https://schemas.wxalerts.org/pws/v{major}/{file}`,
for example `https://schemas.wxalerts.org/pws/v0/obs.schema.json`. The
`{major}` segment is the payload version `v` described below, so a breaking
change produces a new directory of schemas under a new `$id` prefix and the
old one stays published for the `v-1` acceptance window.

Cross file references use relative `$ref`s (`common.schema.json#/$defs/...`),
which resolve against the `$id` without network access. The Go validator
registers every embedded file under its `$id` before compiling, and never
fetches a URL. Other implementations should do the same.

## Versioning

- Every payload that the contract defines with a version carries an integer
  `v`. It is `0` for the whole draft period, during which breaking changes are
  allowed. It becomes `1` when the contract freezes after the test matrix.
- The server accepts `v` and `v-1`. `common.schema.json#/$defs/version` is an
  `enum` of the accepted values, so when `v` bumps to 1 the enum becomes
  `[0, 1]` and a device on the old version still validates.
- Additive changes do not bump `v`. Adding an optional property, a new
  canonical field, a new event type or a new command type is additive. Every
  object the server receives is declared with `additionalProperties: true`
  (or an unknown-field rule for `f`) so an older server accepts a newer
  device.
- Breaking changes bump `v`: removing or renaming a property, changing a
  type, tightening a range, making an optional property required, changing
  what a signature covers.
- The field value objects inside `f` (`gauge`, `direction`, `counter`,
  `flag`) are strict: a typo such as `"median"` is rejected rather than
  silently dropped, because an aggregate that the server cannot interpret is
  data loss.

## Adding a field

Follow the row for "New MQTT field" in `AGENTS.md`. Concretely:

1. Schema. Add the property with a `description` to the right file. If it is
   a new canonical observation field, add it to
   `common.schema.json#/$defs/canonical_field` and to `#/$defs/fields` with
   its field type. Decide whether it is required (it almost never is: a new
   required property is a breaking change).
2. Example. Extend an existing valid example or add
   `examples/valid/{kind}-{what}.json`. If the field has a constraint, add an
   `examples/invalid/{kind}-{what}.json` that violates it together with
   `{kind}-{what}.reason.txt` saying what is wrong. The prefix before the
   first hyphen must be the message kind.
3. Go type. Add the struct field in `internal/contract/types.go` with a JSON
   tag equal to the schema property name. Use a pointer with `omitempty` for
   optional numbers so a legitimate zero is still sent.
4. Test. `go test ./internal/contract` already checks every example against
   the schema, decodes each valid one into the Go type, encodes it back, and
   compares. A property the Go type drops shows up as a failure unless its
   name starts with `x_`, which the examples reserve for forward
   compatibility probes.
5. Prose. Update `docs/contract/` and, while the draft lasts, the Notion page.
6. Run `make schemas` (or `go run ./tools/schemacheck`) and `make check`.

## Examples

`examples/valid/` and `examples/invalid/` hold at least three files per
message kind. The exact payloads from the contract page are included as
`*-notion.json`. Two of them fail the current rules and therefore live under
`invalid/`:

- `cmd-notion-unsigned.json`: the command example predates the signed
  commands decision and lacks `device_id` and `sig`.
- `event-notion-placeholder-ts.json`: the event example uses the literal
  `"..."` as its timestamp.

The valid `cmd-*.json` files are real commands signed with the test key below;
`go test` and `schemacheck` verify them.

## Signing vectors

`examples/signing/ed25519.json` holds the Ed25519 test vectors for command
signing: a published test seed, its public key, and for each command the
unsigned object, its RFC 8785 canonical form, the signature in hex and the
`sig` value as carried on the wire (standard base64 with padding). The seed
`000102...1f` is a well known test value shared by every implementation's
tests. It must never sign a real command; the real command signing key is
generated outside this repository and only its public half is pinned under
`keys/`.

`examples/signing/jcs.json` holds canonicalization vectors (RFC 8785)
including the RFC's own worked examples, so a second implementation can check
its canonicalizer before it checks signatures.

Regenerate both files and the valid `cmd-*.json` examples from the inputs in
`internal/contract/sign_test.go` with:

```
go test ./internal/contract -run 'TestSigningVectors|TestCanonicalizeVectorsFile' -update
```

## Where the schemas interpret or extend the contract

The contract page leaves some things unsaid. The schemas had to pick
something; each choice below is a proposed contract change, not a decision
the contract has made. The descriptions in the schema files say the same.

1. `device_id` pattern `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$` (topic safe, 1 to
   64 characters). The page only shows `d_8f3a`.
2. Station key pattern `^[A-Za-z0-9][A-Za-z0-9_.-]*:[^:]+:[^:]*$`: model
   segment restricted to decoder-name characters, id non-empty, channel may
   be empty for protocols without channels.
3. Timestamps must use the `Z` designator; numeric UTC offsets are rejected.
4. `status` is the JSON string `"online"` or `"offline"`, quotes included,
   so every topic parses as JSON.
5. `sig` encoding is standard base64 with padding (88 characters). The page
   says Ed25519 over canonical JSON but not how the bytes are carried.
6. `update` carries optional `v` and `device_id`; the page's example has
   neither. Proposal: make `v` required when `v` freezes at 1.
7. `event` carries optional `device_id`.
8. `obs` requires `v`, `device_id`, `seq`, `sent`, `interval_s`, `buckets`
   (non-empty); each bucket requires `start` and `stations`; each station
   requires `key`, `n` and `f` (`f` may be `{}`). `meta`, `sig` and `raw`
   are optional.
9. Unknown keys inside `f` are allowed when the value is a well formed field
   object, so a new canonical field does not break an older server. Field
   value objects themselves are strict.
10. `direction.vec` is constrained to `[0, 360)`; `gps.lat`/`gps.lon` to
    their geographic ranges. Everything else numeric is unbounded.
11. `flag.last` is the integer 0 or 1, as in the page example (not boolean).
12. `tel` requires `v`, `device_id`, `ts`, `fw`, `uptime_s`, `time`, `sys`,
    `outbox`, `net`; `os`, `sdr`, `modem`, `gps` are optional. Every key in
    `sys` is optional because boards differ. `sdr[].driver` is the enum
    `rtl_433`, `davis`, `capture` from the Nov 16 draft change.
13. The `ack` payload is not defined by the page. Proposed shape:
    `{v, id, device_id?, type?, ok, error?, result?, ts}`. Commands that fail
    the signature, device id, expiry or replay checks are dropped without an
    ack.
14. Command bodies: `resync` body `{from, to?}` (RFC 3339 bucket range,
    `from` required); `diag` body `{action, service?}` with `action` one of
    `restart_service`, `rediscover`, `connectivity_test`, `clear_outbox`
    (the page's allow list, named here); `config` body left open to the
    device configuration spec. Other types take `{}`.
15. Command `id` pattern `^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$` because it is
    a topic segment.
16. The 64 KB limit is enforced by the Go `Validate` function, not by the
    schema, since JSON Schema cannot express encoded size.
17. `cmd` accepts unknown top level keys (additive rule) but they are
    outside the signature, so a device must ignore them.
