# Pinned public keys

Public keys only. Private keys never exist in this repository.

## command-signing

The device verifies every server command against the command-signing public
key. It is separate from the release-signing key (see `docs/release-signing.md`).

- `command-signing.pub`: the production key. It does not exist yet. The real
  key is pinned at release time, when the release build adds the file before
  `go build` so it is embedded in the binary. Until then a device loads the key
  from `<data-dir>/keys/command-signing.pub`, and a device with neither file
  ignores every command.
- `command-signing.test.pub`: the public half of the published test vector
  seed in `schemas/examples/signing/ed25519.json`. It is a placeholder
  generated for tests. Its private key is public, so it must never be used
  as the pinned key of a real device. The binary embeds it only so tests can
  load it through the same code path.

File format: the 32 byte Ed25519 public key as 64 hex characters (or standard
base64), one line, surrounding whitespace ignored.
