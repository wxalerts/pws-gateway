# Claude Code project notes

Read `AGENTS.md`. It is the full guide for this repository and applies here.

Quick reminders that trip up tooling most often:

- Go is not on PATH on every machine. Use the version in `go.mod`.
- `make check` before every commit.
- No em dashes, no en dashes, no emojis, no attribution trailers.
- Tests never need hardware. Use `testdata/` recordings and `tools/` simulators.
