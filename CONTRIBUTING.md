# Contributing

Thanks for helping. This page covers the toolchain, how to test without a Pi
or a radio, where common changes go, and how pull requests flow. Read
[AGENTS.md](AGENTS.md) too: it is written for AI coding assistants but every
rule in it applies to people.

Contributions are licensed under the project's MIT license, the same as the
code they join. There is no CLA and no DCO sign-off requirement; opening a
pull request is your statement that you have the right to contribute the
change under MIT.

## Toolchain

| Tool | Version | Notes |
| --- | --- | --- |
| Go | the `go` line in `go.mod` | Newer Go toolchains build older `go.mod` versions fine. |
| Node | 22 LTS | Only needed for `web/`. |
| golangci-lint | v2 (CI pins the exact version in `.github/workflows/ci.yml`) | Config in `.golangci.yml`. |
| Docker with Buildx | any current release | Only for the `deploy/` images. |

Install golangci-lint with the official script or your package manager, then
check it reads the config:

```sh
golangci-lint config verify
```

The gateway uses `modernc.org/sqlite`, so there is no cgo and no C compiler
to install. Cross-compiling for the Pi is a plain
`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build` (also `make build-arm64`).

## Running the checks

```sh
make check      # gofmt, golangci-lint, go test, schema validation
make test       # tests and schema validation only
make lint       # golangci-lint only
make fmt        # rewrite files with gofmt
```

`make check` is what CI runs, so a green local run means a green pull
request in almost every case. For the web UI:

```sh
cd web && npm ci && npm run lint && npm run build && npm test
```

The CI jobs are described in [docs/ci.md](docs/ci.md), with the same commands
for each.

## Testing without hardware

Nothing in `make test` needs an SDR, a Pi or a network. Two mechanisms make
that possible:

- `testdata/` holds recorded `rtl_433` JSON output, one file per station or
  decoder scenario. Collector and mapping tests replay these recordings
  through the same code path the live stream uses.
- `tools/` holds simulators: a fake `rtl_433` that emits a recording on a
  schedule, and an MQTT broker stub for uplink tests.

If a change can only be verified with a dongle in hand, say so in the pull
request and describe what you ran. Reviewers may ask someone with the
hardware to try it before merging. The platform adapter in
`internal/platform/` is the only package that may touch `/sys`, `/proc`,
`vcgencmd` or device trees; everything above it is tested against a fake
adapter.

To capture a new recording from a real station:

```sh
rtl_433 -F json -M time:utc -M protocol -M level -C si > testdata/<station>.jsonl
```

Trim it to a few minutes, and scrub or rotate the `id` field if you do not
want your sensor's identifier published.

## Adding a decoder mapping

A decoder mapping tells the gateway how one `rtl_433` model's fields become
canonical observation fields.

1. Add a recording under `testdata/` (see above) with at least one line per
   field you map. Rain and wind need several lines because they accumulate.
2. Add the mapping under `internal/collector/mapping/`. Follow the existing
   files for the shape. Units are SI internally: convert at the mapping, not
   downstream.
3. Add a test that replays the recording and asserts the canonical output.
4. If the station is not yet in `docs/hardware.md`, add it with the result.

If you have the station but not the time to write the mapping, open a
"Decoder mapping request" issue with a sample line; that is enough for
someone else to do it.

## Adding an MQTT field

The MQTT contract is shared with the server side, so a new field touches five
places and the pull request should include all of them:

1. `schemas/`: the JSON Schema for the message that carries the field.
2. `docs/contract/`: the human-readable contract page, with units and
   nullability.
3. `internal/contract/`: the Go type.
4. An example payload (next to the schema) that uses the field.
5. A test that encodes the Go type and validates it against the schema.

`make schemas` validates every example against its schema. While the
contract is `v: 0` fields can still change; once it is `v: 1` additive
changes only.

## Commit style

- One logical change per commit.
- Imperative subject under 72 characters ("Add Fineoffset-WH65B mapping",
  not "Added" or "Adds"), a blank line, then a body that explains why.
- No em dashes, no en dashes, no emojis.
- No attribution trailers (`Co-Authored-By`, `Generated-By`, tool banners).
  The author field is the attribution, for people and tools alike.
- Reference issues and milestones in the pull request, not in the commit
  subject.
- Every commit should pass `make check` on its own.

## Pull request flow

1. Fork, branch from `main`, make the change.
2. Run `make check` (and the web checks if you touched `web/`).
3. Open the pull request and fill in the template: what changed, why, which
   spec page it follows, how you tested it without hardware.
4. CI runs on every pull request with no secrets. Forks get the same checks
   as maintainers.
5. A maintainer reviews. Changes under `.github/workflows/` and `keys/`
   additionally need a review from the code owner listed in
   `.github/CODEOWNERS`.
6. Merges are squash or rebase, never merge commits, so `main` stays linear.

If the spec under `docs/spec/` and the code disagree, say so in the pull
request instead of quietly picking one. Changes that fall under
`docs/out-of-scope-v1.md` will be parked until v1 ships, so open an issue
first to avoid wasted work.

## Contributing with AI assistants

Pull requests written with Claude Code, Copilot, Cursor, Codex or similar
tools are welcome, and are reviewed to the same standard as any other.
Three requests:

- Point the assistant at [AGENTS.md](AGENTS.md). It carries the ground rules
  (tests without hardware, no remote command execution, SI units, no
  credentials in the tree, writing style) and most tools pick it up
  automatically.
- Read, run and understand the result before opening the pull request. You
  are the author; the tool is not. `make check` must pass on your machine,
  and any claim in the pull request about what was tested has to be
  something you actually did.
- Say which tool you used in the "AI assistance" section of the pull request
  template. It is not a mark against the contribution. It tells reviewers
  where to look harder, because generated code fails in different places
  than hand-written code (invented APIs, plausible but untested edge cases,
  changes wider than the task).

Attribution works the same as for everyone else: your name in the commit's
author field. Do not add tool trailers or banners.

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md). Security
problems go through [SECURITY.md](SECURITY.md), never public issues.
