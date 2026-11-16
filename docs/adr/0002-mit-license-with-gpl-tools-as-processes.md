# ADR 0002: MIT license with GPL tools as separate processes

Status: accepted 2026-11-16.

## Context

The gateway depends on `rtl_433` (GPL-2.0) for every decoder and, later, on
rtldavis (GPL-3.0) for Davis stations. The project wants a permissive license
so station owners, integrators and other weather projects can reuse the device
code, the contract and the schemas without copyleft obligations. The repo
`wxalerts/pws-gateway` is public from the first commit.

## Decision

The repository is licensed MIT. GPL tools are never linked into the Go binary
and never vendored as source in this repo. They run as separate processes,
started and supervised by the daemon, communicating over stdout JSON (and
stdin or flags for configuration). The SD card image ships their binaries, and
CI publishes the corresponding source tarballs with each release to satisfy
the GPL. The `NOTICE` file lists every such tool and its license.

## Consequences

- Clean license boundary: the GPL applies to `rtl_433` and rtldavis as
  distributed, not to the daemon, the SPA, the contract or the schemas.
- A process boundary per radio also gives the failure isolation the Radio
  roles page wants (a crash restarts one radio).
- Slight overhead and a JSON parsing step per decoded packet; negligible at
  weather-station rates.
- rtldavis being Go does not change this. Linking a GPL-3.0 Go module into an
  MIT binary would relicense the binary; the process boundary avoids that.
- Release automation must produce and publish GPL source alongside every image
  that contains a GPL binary. This is a CI requirement from M1 onward even
  though images come later.
