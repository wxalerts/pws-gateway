# Guide for coding agents and contributors

This file is read by AI coding assistants (Claude Code, Codex, Copilot, Cursor
and others) and by people. Everything here applies to both.

## What this project is

A Go daemon (`cmd/pwsgw`) plus a React web UI (`web/`) that runs on a
Raspberry Pi, receives personal weather stations through `rtl_433`, stores
readings in SQLite and uplinks to WxAlerts over MQTT. Read `docs/spec/README.md`
before changing behaviour. The MQTT contract in `docs/contract/` and the JSON
Schemas in `schemas/` are the source of truth shared with the server side.

## Ground rules

1. Read the relevant page under `docs/spec/` before starting. If the spec and
   the code disagree, say so in the pull request instead of silently picking one.
2. Stay inside v1 scope. `docs/out-of-scope-v1.md` lists what not to build
   (Davis, onboarding wizard, listen mode, non-Pi boards). Open an issue instead.
3. Tests must run without hardware. Drive the collector with recordings in
   `testdata/` or the simulator in `tools/`. Never require an SDR, a Pi or a
   network to pass `make test`.
4. The device never executes shell commands from the server. There is no
   install command. Updates come only through the signed manifest. Do not add
   escape hatches, even for debugging.
5. No credentials in the repository, in tests, in fixtures or in commit
   messages. Public keys go under `keys/`. Private keys never exist here.
6. Board-specific code lives behind the platform adapter in
   `internal/platform/`. Nothing else may read `/sys`, `/proc`, `vcgencmd` or
   device trees directly.
7. SI units internally. Unit conversion happens only at the display edge.
8. All timestamps are UTC, RFC 3339.
9. GPL tools (`rtl_433`, `rtldavis`) are separate processes, never linked in.
   Keep `NOTICE` accurate when adding one.

## Toolchain

- Go: the version in `go.mod`. Run `gofmt` and `golangci-lint run` before
  committing. CI fails on either.
- Web: Node 20 LTS, `npm ci` inside `web/`. Lint with `npm run lint`.
- `make test` runs Go tests, web tests and schema validation. `make check`
  adds lint.
- SQLite through `modernc.org/sqlite` (no cgo) so cross-compiling for arm64
  stays a plain `GOARCH=arm64 go build`.

## Writing style

These apply to code comments, docs, commit messages and pull request text.

- No em dashes or en dashes anywhere. Use a comma, a colon, a period or
  parentheses.
- No emojis anywhere.
- Plain sentences. Say why, not just what.
- Do not add attribution trailers (`Co-Authored-By`, `Generated-By`, tool
  banners) to commits or pull requests. Contributions from AI assistants are
  welcome and are attributed the same way as any other: the author field of
  the commit.

## Commits and pull requests

- One logical change per commit. Imperative subject under 72 characters, a
  blank line, then a body that says why.
- Commits must pass `make check`.
- Reference the milestone or issue in the pull request, not the commit subject.
- `.github/workflows/` changes need a code-owner review.
- Pull requests from forks never receive secrets. The release signing job runs
  only on tags from `main` in a protected environment.

## Where things go

| Change | Place |
| --- | --- |
| New MQTT field | `schemas/`, `docs/contract/`, `internal/contract/`, an example payload, a test |
| New decoder mapping | `internal/collector/mapping/` plus a `testdata/` recording |
| New web page | `web/src/pages/`, an API route in `internal/web/`, and a doc page |
| New board | `internal/platform/<board>/` only |
| New decision | `docs/adr/NNNN-title.md` |
