<!--
Thanks for the pull request. Short answers are fine. Delete sections that do
not apply. Milestone or issue references go here, not in commit subjects.
-->

## What changed

## Why

## Spec page touched

<!-- Which page under docs/spec/ (or docs/contract/, schemas/) this follows or
changes. If the spec and the code disagreed, say which one you followed and
why, per AGENTS.md rule 1. Write "none" if this is tooling only. -->

## How it was tested without hardware

<!-- Which testdata/ recordings or tools/ simulators exercised the change.
`make check` is required; mention anything beyond it. If a change can only
be verified on a Pi with a dongle, say so and describe what you ran. -->

## Checklist

- [ ] `make check` passes locally (gofmt, golangci-lint, tests, schemas).
- [ ] No credentials, no em dashes, no en dashes, no emojis, no attribution trailers (`Co-Authored-By`, `Generated-By`) in code, comments, docs or commits.
- [ ] New MQTT fields come with schema, contract doc, Go type, example payload and test.
- [ ] New decoder mappings come with a `testdata/` recording.
- [ ] `NOTICE` is still accurate if a third-party tool was added or changed.

## AI assistance

<!-- Contributions written with AI coding assistants are welcome. Please say
which tool was used (for example "Claude Code", "Copilot", "Cursor", "none")
so reviewers know where to look harder: generated code tends to be wrong in
different places than hand-written code (invented APIs, plausible but
untested edge cases, over-broad changes). The human author remains
responsible for having read, run and understood every line. -->

Tool used:
