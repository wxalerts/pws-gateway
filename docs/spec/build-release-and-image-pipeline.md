# Build, release and image pipeline

## Goal

Free CI via GitHub, reproducible SD card images.

## Design

- Public repo with GitHub Actions. Verify arm64 runner availability for public
  repos. QEMU is the fallback (slower).
- pi-gen based image build. Output: flashable image plus signed update bundles.
- `rtl_433` is GPL-2.0: run it as a separate process and publish source as
  required. Pick the project license accordingly.
- Update manifest service on the WxAlerts side drives staged rollouts.
- Image includes first-boot setup (AP hotspot, claim code).

## Decisions (Nov 16)

- License: MIT, in a new public GitHub repo.
- GPL tools (`rtl_433`, rtldavis) run as separate processes. CI publishes their
  corresponding source with each release, and the repo carries a `NOTICE`
  file.
- Raspberry Pi only at launch. Board-specific code sits behind a platform
  adapter. Build multi-arch containers (arm64 and amd64) so Tier 2 boards stay
  possible later.
- 64-bit only. Pi Zero W, Pi 2 and Pi 1 are not supported.
- Signing secret is held in Infisical. See
  [Auto-update and rollback](auto-update-and-rollback.md) for the CI signing
  design.
- Distribution through Raspberry Pi Imager's custom repository option (verify).
- Specs move into the repo for development: contract, JSON Schemas, conventions
  file, milestones with acceptance tests. (Done: this directory.)

Updated 2026-11-16 in repo:

- The repo is `wxalerts/pws-gateway`, public from the first commit, MIT. The
  "Repo and org name" open item is resolved.
- The CI signing design is approved. The build job (no secrets, runs on PRs)
  and a disabled sign job (tags from `main`, protected environment, Infisical
  OIDC) are being scaffolded in M1. The sign job stays disabled until the
  Infisical machine identity exists.
- The daemon is a single static Go binary cross-compiled with
  `CGO_ENABLED=0 GOARCH=arm64`; SQLite uses `modernc.org/sqlite` so no cross C
  toolchain is needed. The React SPA is built with Node 20 and embedded with
  `go:embed`.
- Update bundles are hosted on object storage behind the WxAlerts manifest
  service (per the Auto-update decisions), not on GitHub Releases as the
  original Design bullet said. GitHub Releases carry the flashable image and
  the GPL source tarballs.

## Open

- GitHub arm64 runner availability for public repos; QEMU fallback otherwise.
- Raspberry Pi Imager custom repository support (verify).
- rtldavis needs an older Go toolchain according to reports; pin it in CI when
  the Davis module is built (post-v1).
