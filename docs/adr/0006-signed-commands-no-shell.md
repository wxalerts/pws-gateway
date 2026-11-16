# ADR 0006: All server-to-device commands Ed25519 signed, no shell

Status: accepted 2026-11-16.

## Context

The server can send commands to a device on `pws/{device_id}/cmd/#`. The
broker ACL already restricts who can publish there, but the ACL is a single
control at a single layer: a compromised broker, a leaked service credential,
or a misconfigured rule would let an attacker drive 10,000+ devices sitting
inside people's home networks. A general "run this" command would turn any such
mistake into a fleet-wide remote code execution.

Remote support is still needed: support bundles, restarting a service,
re-running discovery, connectivity tests.

## Decision

- Every command carries `sig`, an Ed25519 signature over the canonical JSON of
  `{id, type, device_id, exp, body}`. The device pins the command-signing
  public key, which is separate from the release-signing key.
- The device verifies, in order: `device_id` equals its own, `exp` is in the
  future, `id` is not in its replay cache, the signature is valid. A command
  that fails any check is not applied and not acked as success.
- Command types are a closed set: `config`, `reboot`, `bundle`,
  `update-check`, `resync`, `diag`, and `capture` (reserved for v2). `diag` is
  a closed allowlist of functions (restart a service, re-run discovery,
  connectivity test, clear outbox), not a string passed to a shell.
- There is no shell command, no install command and no file-write command.
  Software reaches the device only through the signed update manifest.
- Config commands go through the same commit-confirm as the web UI so a bad
  config cannot strand a device either.

## Consequences

- Compromising the broker or a service credential is not enough to run code
  on devices; the attacker also needs the command-signing private key, which
  lives in Infisical and is used only by the command publisher.
- Support staff cannot "just ssh in". Diagnosis goes through the support
  bundle and allowlisted `diag` actions. A time-limited, opt-in remote session
  is a later, separately designed feature and is still not a shell.
- Adding a diagnostic action is a code change on the device with a release,
  not a runtime decision. That is the intended friction.
- The canonical JSON algorithm has to be fixed in the schema work (open item
  in the contract); until it is, no two implementations can interoperate.
- Tests must prove the negative: an undocumented type, a bad signature, a
  wrong device ID, an expired or replayed command each do nothing.
