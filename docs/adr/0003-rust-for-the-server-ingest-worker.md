# ADR 0003: Rust for the server ingest worker in the private repos

Status: accepted 2026-11-16.

## Context

The spec described the ingest worker as a Python asyncio service on an EMQX
shared subscription. At 10,000+ stations it handles about 11 messages per
second on average with bursts, validates every payload, dedupes and writes
wide rows to PostGIS. The WxAlerts API is beginning a migration to Rust, and
this worker is a small, well-bounded service with a contract already written
down, which makes it a good first piece.

The worker touches private infrastructure: EMQX credentials, the PostGIS
observation database, Authentik, Discord alerting. None of that belongs in a
public repository.

## Decision

The ingest worker is written in Rust and lives in the private WxAlerts
repositories. The public repo `wxalerts/pws-gateway` holds only the device
side plus the shared MQTT contract (`docs/contract/`) and the JSON Schemas
(`schemas/`). Those schema files are the single source of truth for both
sides: the Rust worker vendors them at a pinned commit and validates every
message against them in tests and at runtime.

## Consequences

- Two languages across the wire. The contract and the schemas are the only
  shared code, so they must be exact and tested on both sides. Example
  payloads in the public repo double as fixtures for the Rust tests.
- The relay normalization (sea-level pressure, 10 m wind, CWOP packet) is
  specified with golden vectors in the public repo and implemented twice.
- Changes to the contract are pull requests in the public repo first; the
  private repo bumps its pinned commit. A breaking change is visible to
  everyone before it reaches the server.
- Server-side work is tracked as a checklist in `docs/server-integration.md`
  and requires a written report to the maintainer before deployment.
- The first Rust service in production sets the pattern (build, deploy,
  observability) for the rest of the API migration; keep it boring.
