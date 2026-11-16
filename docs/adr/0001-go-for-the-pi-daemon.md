# ADR 0001: Go for the Pi daemon

Status: accepted 2026-11-16.

## Context

The spec's Local web UI page described a FastAPI (Python) backend plus a React
SPA. The daemon has to run on a Pi 3B+ with 1 GB RAM alongside `rtl_433`,
supervise several child processes, hold raw samples in RAM, write SQLite in
batches, keep an MQTT session with an outbox, verify Ed25519 signatures, serve
HTTPS, and update itself atomically on an A/B root. It ships to 10,000+
devices that nobody logs into.

A Python runtime means a dependency tree on the image, slower startup, a
larger memory footprint, and a harder story for a single verifiable artifact
to sign and roll back. The team's other services are moving toward compiled
languages (see ADR 0003).

## Decision

The Pi daemon (`cmd/pwsgw`) is written in Go. It is one static binary,
cross-compiled with `CGO_ENABLED=0 GOARCH=arm64`. The local web UI stays a
React SPA; the Go binary serves it from `web/` through `go:embed`, so the
device runs one process for the application. Charts in the SPA use uPlot.

Board-specific code stays behind the platform adapter in `internal/platform/`
so the choice of language does not leak Pi assumptions into the rest.

## Consequences

- One artifact to build, sign, ship and roll back. No interpreter, no
  virtualenv, no package manager on the device.
- Cross-compiling for arm64 is a plain `go build`, which is why SQLite uses a
  pure-Go driver (ADR 0004).
- The relay normalization code can no longer be a library shared with the
  server (Rust). It is shared as a specification with golden test vectors
  instead (see Relay plugin topology).
- rtldavis is also Go, which makes linking it tempting. ADR 0002 forbids it;
  it stays a separate process.
- Contributors need Go and Node 20. The web build happens before the Go build
  so the embedded assets are current.
