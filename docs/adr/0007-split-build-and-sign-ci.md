# ADR 0007: Split build and sign CI with Infisical OIDC and offline root key

Status: accepted 2026-11-16 (was "recommended, pending approval" in the spec).

## Context

Auto-update is mandatory and devices verify release signatures, so the release
signing key is the most valuable secret in the project: whoever holds it can
push software to 10,000+ devices. CI runs on GitHub Actions in a public
repository that accepts pull requests from forks. If the signing key were a
plain repository secret, a CI compromise, a malicious workflow change or a
leaky third-party action could sign a malicious update. The key is held in
Infisical.

## Decision

- Build and sign are separate jobs. The build job runs on every pull request,
  including from forks, with read-only permissions and no secrets. The sign
  job runs only on tags pushed to the protected `main` branch.
- The sign job runs in a GitHub Environment with required reviewers.
- It authenticates to Infisical with GitHub OIDC through a machine identity
  whose audience, subject, repository and environment claims are bound to that
  environment, so forks and pull requests cannot obtain a token. Token TTL is
  short.
- Two-key scheme: an offline root key (plus a backup root) signs a delegation
  to an online release key held in Infisical. Devices pin both root public
  keys. A leaked release key is rotated by publishing a new delegation, with
  no reflash.
- Manifests carry an expiry and a monotonic version; devices reject stale or
  lower-version manifests unless a signed rollback authorization is present.
- All GitHub Actions are pinned by commit SHA. `.github/workflows/` changes
  require code-owner review.
- Any read of the signing secret outside the release workflow raises an alert
  in Infisical.
- Command signing uses a separate Ed25519 key (ADR 0006).

The design is being scaffolded in M1 with the sign job disabled. It is enabled
only once the Infisical machine identity exists and the maintainer has
received the written report described in `docs/server-integration.md`.

## Consequences

- A pull request cannot sign anything, and a merged malicious workflow change
  still needs a tag on `main` plus a human approval in the environment plus a
  valid OIDC token.
- Rotation of the online key is routine; rotation of a root key means a
  firmware release that pins the new root, which is why there are two roots
  from day one.
- Releases are slower than a plain push-to-deploy: a tag, a reviewer, a signed
  manifest. That is acceptable for firmware.
- Open: whether Infisical KMS can perform the signature without the private
  key ever reaching the runner. If it can, the runner never holds the key at
  all; if not, the key is in runner memory for the duration of the sign step.
- Open: GitHub arm64 runner availability for public repos; QEMU is the
  fallback and makes the build job slower but does not change this design.
