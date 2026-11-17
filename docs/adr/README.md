# Architecture decision records

Short records in the Nygard form: context, decision, consequences. One file per
decision, numbered, never edited after acceptance except to add a status line
pointing at a superseding record.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-go-for-the-pi-daemon.md) | Go for the Pi daemon | Accepted 2026-11-16 |
| [0002](0002-mit-license-with-gpl-tools-as-processes.md) | MIT license with GPL tools as separate processes | Accepted 2026-11-16 |
| [0003](0003-rust-for-the-server-ingest-worker.md) | Rust for the server ingest worker in the private repos | Accepted 2026-11-16 |
| [0004](0004-sqlite-on-device-raw-in-ram-rollups-every-15-minutes.md) | SQLite on device, raw in RAM, rollups every 15 minutes | Accepted 2026-11-16 |
| [0005](0005-json-mqtt-contract-versioned-with-v.md) | JSON MQTT contract versioned with `v`, additive changes free | Accepted 2026-11-16 |
| [0006](0006-signed-commands-no-shell.md) | All server-to-device commands Ed25519 signed, no shell | Accepted 2026-11-16 |
| [0007](0007-split-build-and-sign-ci.md) | Split build and sign CI with Infisical OIDC and offline root key | Accepted 2026-11-16 |
| [0008](0008-ab-slots-with-the-firmware-tryboot-flag.md) | A/B slots with the firmware tryboot flag | Accepted 2026-11-17 |

To add one: copy the shape of an existing file, take the next number, open a
pull request. `AGENTS.md` says new decisions go here.
