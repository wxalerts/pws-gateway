# Continuous integration and release pipeline

What each workflow under `.github/workflows/` does, why the runners were
chosen, the branch protection `main` should have, and how to run the same
checks on your machine.

## Workflows

### ci.yml (pull requests and pushes to main)

Runs with `permissions: contents: read` and no secrets, so pull requests from
forks get identical treatment. One concurrency group per ref with
cancel-in-progress, so pushing a fix to a branch cancels the stale run.

| Job | Runner | What it does |
| --- | --- | --- |
| `go` | `ubuntu-24.04` | `gofmt -l`, `golangci-lint run` (pinned v2 version from the workflow `env`), `go vet ./...`, `go build ./...`, `go test -race ./...` |
| `go-arm64` | `ubuntu-24.04-arm` | `go build ./...` and `go test -race ./...` natively on arm64, the architecture the gateway ships to |
| `web` | `ubuntu-24.04` | `npm ci`, `npm run lint`, `npm run build`, `npm test` inside `web/`. Every step is guarded with `hashFiles('web/package.json') != ''` so CI passes before the UI lands |
| `schemas` | `ubuntu-24.04` | `make schemas`, guarded on `tools/schemacheck/*.go` existing |
| `govulncheck` | `ubuntu-24.04` | `govulncheck ./...` against the Go vulnerability database |
| `deploy` | `ubuntu-24.04` | `docker build --check` on both Dockerfiles (lint only, no stages run) and `docker compose config` on the Pi compose file |

Steps that would fail on an empty module (golangci-lint, govulncheck) are
guarded with `hashFiles('**/*.go') != ''`; `go build`, `go vet` and `go test`
tolerate "matched no packages" on their own. Remove the guards once the first
Go code is in.

Go module and build caches come from `actions/setup-go` (enabled once
`go.sum` exists); npm caches come from `actions/setup-node` keyed on
`web/package-lock.json`.

### release.yml (tags `v*`)

Three jobs plus a status job. The full signing design, the key hierarchy and
the one-time setup are in [release-signing.md](release-signing.md).

- `build` (no secrets, `contents: read` plus `packages: write` for ghcr.io):
  validates the tag shape `vMAJOR.MINOR.PATCH[-rc.N]`, derives a monotonic
  `version_number`, builds the web UI (or a placeholder), cross-compiles
  `pwsgw` for linux/arm64 and linux/amd64 with `CGO_ENABLED=0`, packages
  reproducible tarballs, builds and pushes multi-arch images
  `ghcr.io/wxalerts/pws-gateway` and `ghcr.io/wxalerts/pws-rtl433` with
  buildx, generates an SPDX SBOM with syft (`anchore/sbom-action`), writes
  `manifest.json` and `SHA256SUMS`, uploads `dist/` as the `release-dist`
  artifact.
- `sign-status`: writes one plain sentence into the run summary saying
  whether signing is enabled. It never enters the protected environment, so
  it never pages reviewers.
- `sign` (`environment: release-signing`, `id-token: write`): skipped unless
  the repository variable `RELEASE_SIGNING_ENABLED` is exactly `true`. Checks
  the delegation chain in `keys/`, authenticates to Infisical with the job's
  OIDC token, asks Infisical KMS to sign `manifest.json` server-side with the
  non-exportable Ed25519 release key, self-checks the signature with the KMS
  verify endpoint and locally with OpenSSL against `keys/release.pub`, and
  uploads `manifest.json.sig`. The private key never reaches the runner. The
  Infisical steps are placeholders until the LAN bridge exists.
- `publish` (`contents: write`): runs when `build` succeeded and `sign`
  succeeded or was skipped, never when `sign` failed. Attaches the bundles,
  raw binaries, manifest, signature and key files, SBOM, `SHA256SUMS`, the
  exact rtl_433 source tarball (checksum verified against the value in
  `deploy/rtl433/Dockerfile`) and the build recipe to the GitHub Release.
  Unsigned releases are created as drafts. A placeholder step describes the
  object storage upload (`RELEASE_BUCKET` variable, no credentials yet).

### image.yml (manual)

`workflow_dispatch` only. Validates the `deploy/` files the SD image will bake
in and runs `docker compose config`. The pi-gen job is a commented placeholder
until the A/B boot question (tryboot unverified on Pi 3B+) is settled; the
reasoning is in the header comment of the workflow.

## Runner choices and what was verified

- All jobs use GitHub hosted runners. The repository is public, so standard
  hosted runners are free and unlimited.
- Native arm64 is used for `go-arm64`. Verified on 2026-11-16 against
  https://docs.github.com/en/actions/reference/runners/github-hosted-runners:
  the public repository table lists Linux arm64 runners with labels
  `ubuntu-24.04-arm`, `ubuntu-22.04-arm` and `ubuntu-26.04-arm` (4 vCPU,
  16 GB RAM, 14 GB SSD), and the page states "Use of the standard
  GitHub-hosted runners is free and unlimited on public repositories." No
  preview or availability caveat is attached to the Linux arm64 labels (the
  only arm64 limitations listed are for macOS). `ubuntu-24.04-arm` was chosen
  to match the x86 jobs; move both to 26.04 together when the time comes.
- Multi-arch container images are built with buildx and QEMU on the x86
  runner. The Go stage cross-compiles natively (`--platform=$BUILDPLATFORM`),
  so QEMU only runs the tiny distroless final stage and the rtl_433 Debian
  stages. If the rtl_433 arm64 build becomes slow, split the image build into
  a matrix over `ubuntu-24.04` and `ubuntu-24.04-arm` and merge manifests.
- The `sign` job may later need a self-hosted runner on the LAN if a tunnel to
  Infisical is not acceptable. If so, label it and select it for that job
  only. Everything else stays hosted: a self-hosted runner on a public
  repository executes pull request code and must never be used by `ci.yml`.

## Pinned actions

Every `uses:` is pinned to a 40-character commit SHA with the tag in a
trailing comment, resolved with
`gh api repos/OWNER/REPO/git/ref/tags/TAG` and dereferenced through
`git/tags/SHA` when the tag is annotated. Dependabot (`github-actions`
ecosystem, weekly) updates both the SHA and the comment.

| Action | Tag | Commit |
| --- | --- | --- |
| actions/checkout | v7.0.1 | 3d3c42e5aac5ba805825da76410c181273ba90b1 |
| actions/setup-go | v7.0.0 | b7ad1dad31e06c5925ef5d2fc7ad053ef454303e |
| actions/setup-node | v7.1.0 | 949feb2413d6458794dcd2491c4babbbce0c15c1 |
| actions/upload-artifact | v7.0.2 | cf430e030ddbb5b0abf93d22962f4752f3646cd9 |
| actions/download-artifact | v8.0.2 | 9000827ccba6bdab643e8b6fd33ac0654aef8333 |
| golangci/golangci-lint-action | v9.3.0 | ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a |
| docker/setup-qemu-action | v4.4.0 | 99012661954931238ded8c8b007157a8430204e1 |
| docker/setup-buildx-action | v4.4.1 | f87e5991a6d7451dcb8d9637bfbc97413f497069 |
| docker/login-action | v4.8.0 | 8aa5831fb8d48d8f76594c7ddde61ae58019f550 |
| docker/metadata-action | v6.2.0 | dc802804100637a589fabce1cb79ff13a1411302 |
| docker/build-push-action | v7.4.0 | c3c9e263c25d99ce0380d002d59b67737d91b0dc |
| anchore/sbom-action | v0.24.3 | 66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c |
| softprops/action-gh-release | v3.0.4 | 8a0cd651e5b2365bd640854a3fa5dfc70e4875f3 |
| tailscale/github-action | v4.4.0 | 064b10558093f38325e838ddea35486bdc6b1d5c |

Other pinned versions: golangci-lint `v2.6.2` (workflow `env`, matches the
version `.golangci.yml` was verified with), govulncheck `v1.9.0`, rtl_433
`25.12` with sha256
`d283ec7a41a02d398e8918b20b65df3bf684cf4478371830662004005dadcdd2`
(`deploy/rtl433/Dockerfile`).

## Recommended branch protection for main

Set under Settings, Rules (a ruleset) or Branches. The intent is a linear,
reviewed history where the pipeline itself cannot be changed quietly.

- Require a pull request before merging; at least 1 approval; dismiss stale
  approvals on new commits.
- Require review from Code Owners. `.github/CODEOWNERS` covers
  `.github/workflows/` and `keys/`.
- Require status checks to pass before merging, with "require branches to be
  up to date" on. Required checks: `Go (amd64)`, `Go (arm64)`, `Web UI`,
  `Schemas`, `govulncheck`, `Deploy files` (the job `name` values from
  `ci.yml`).
- Require linear history. Allow squash and rebase merges only; disable merge
  commits in the repository merge settings.
- Require conversation resolution before merging.
- Block force pushes and deletions.
- Signed commits: optional. Nice to have, but do not require it until every
  active contributor has signing set up; the signing that matters for devices
  is the release manifest, not commits.
- Do not allow bypassing the above for administrators, except through a
  documented break-glass that is turned off again afterwards.
- Tags: add a tag ruleset for `v*` that restricts who can create tags to
  maintainers. The release workflow trusts tags, so tag creation is a
  privileged act.

The `release-signing` environment additionally restricts deployments to tags
matching `v*` and requires reviewers; see
[release-signing.md](release-signing.md).

## Running the same checks locally

```sh
# Go, all of ci.yml's go job
gofmt -l $(find . -name '*.go' -not -path './web/*')
golangci-lint run
go vet ./...
go build ./...
go test -race ./...

# Or simply
make check

# arm64 cross-compile (what the Pi runs)
make build-arm64

# Web UI
cd web && npm ci && npm run lint && npm run build && npm test

# Schemas
make schemas

# Vulnerabilities
go install golang.org/x/vuln/cmd/govulncheck@v1.9.0 && govulncheck ./...

# Deploy files
docker build --check -f deploy/Dockerfile .
docker build --check -f deploy/rtl433/Dockerfile deploy
docker compose -f deploy/docker-compose.yml config --quiet

# Workflow files themselves
actionlint .github/workflows/*.yml
golangci-lint config verify

# Full container builds, as the release does for one platform
docker build -f deploy/Dockerfile --build-arg VERSION=dev -t pws-gateway:dev .
docker build -f deploy/rtl433/Dockerfile -t pws-rtl433:dev deploy
```

`actionlint` is a single binary from https://github.com/rhysd/actionlint; it
catches wrong input names, bad expressions and unpinned actions before a push.
