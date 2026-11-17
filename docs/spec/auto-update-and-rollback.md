# Auto-update and rollback

## Goal

Automatic, background, fully reliable updates. Never leave a station offline or
failed.

## Design

- A/B root partitions with automatic rollback. Pi 4/5 bootloaders support
  tryboot natively. Verify the equivalent for Pi 3B+ (likely U-Boot or a
  partition-switching scheme).
- App layer in containers for cheap rollback, OS layer on A/B.
- Post-boot health check (services up, MQTT connected, SDR decoding) before the
  update is marked good. Hardware watchdog.
- Signed releases (minisign or cosign), verified on the device. Unsigned
  auto-update is a supply-chain risk.
- Staged rollout: 1%, 10%, then 100%, with auto-halt on failure rate.
- Version and any failed upgrade reported over MQTT (`update` topic, retained).
  Update windows configurable.

## Decisions (Nov 16)

- Pi fetches updates from a WxAlerts manifest. Signing secret is held in
  Infisical.
- LTE links get app-only updates. OS image updates stay on Ethernet or Wi-Fi.
- Weak LTE signal: resumable ranged downloads, per-chunk hashes, retry with
  backoff, and give up and defer when latency is too high. A deferred update is
  reported as `deferred`, not `failed`.
- Fetches are staggered with per-device jitter. Bundles are hosted on object
  storage, not GitHub release URLs, to avoid rate limits at 10,000+ devices.

## CI signing design (approved 2026-11-16)

Updated 2026-11-16 in repo: this design was "recommended, pending approval" in
Notion. It is now approved and is being scaffolded in `.github/workflows/`. The
sign job is present but disabled until the Infisical machine identity exists.
See ADR 0007.

- Split build and sign. The build job runs on PRs and gets no secrets. The sign
  job runs only on tags from a protected branch.
- The sign job runs in a GitHub Environment with required reviewers.
- Authenticate to Infisical with OIDC through a machine identity. Bind the
  audience, subject and claims to that environment, so forks and PRs cannot
  authenticate. Use a short token TTL.
- Alert on any read of the signing secret outside the release workflow.
- Two keys. An offline root key signs delegations. An online release key in
  Infisical signs releases. Devices pin the root key and a backup root, so a
  leaked release key is rotatable without reflashing.
- Manifest carries an expiry and a monotonic version. Devices reject stale
  manifests and lower versions, unless a signed manifest authorizes a rollback.
- Pin GitHub Actions by commit SHA. Require code-owner review on
  `.github/workflows`.
- Check whether Infisical KMS can sign without exposing the key to the runner.
- Command signing uses a separate Ed25519 key (see the MQTT contract).

## A/B decision (Nov 17)

Resolved by [ADR 0008](../adr/0008-ab-slots-with-the-firmware-tryboot-flag.md):
the firmware `tryboot` flag on every Pi model, one layout for all boards. The
Pi 3B+ rollback run is still owed on real hardware. Implementation notes:
[Update manifest, slots and state machine](update-manifest-and-slots.md).

## Before image work

- Resolve A/B boot and rollback on a real Pi 3B+. This sets the partition
  layout. Tryboot is documented for newer Pi bootloaders and is unverified on
  the 3B+. Fallbacks: U-Boot with RAUC, or a custom partition switch.
- Board-specific update code goes behind the platform adapter.

## Repo notes (2026-11-16)

- The Build pipeline page says signed update bundles are published on GitHub
  Releases; the decision above says bundles are hosted on object storage to
  avoid GitHub rate limits at fleet scale. The repo follows the object storage
  decision. GitHub Releases may still carry the flashable image and the GPL
  source tarballs, which are fetched by people, not by 10,000 devices.
- A Pi 4 is available for tryboot testing. The Pi 3B+ A/B question stays open.

## Open

- Final A/B mechanism for Pi 3B+ (RAUC, Mender, or custom).
- CI access to the signing secret: the approved design answers this with OIDC,
  a protected environment and the two-key scheme. What remains open is whether
  Infisical KMS can sign without the key ever reaching the runner; if it can,
  the online release key never leaves Infisical.
