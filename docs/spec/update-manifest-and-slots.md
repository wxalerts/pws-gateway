# Update manifest, slots and the update state machine

Implementation notes for M6. The design decisions are in
[Auto-update and rollback](auto-update-and-rollback.md), the trust chain is in
[Release signing](../release-signing.md) and the A/B choice is
[ADR 0008](../adr/0008-ab-slots-with-the-firmware-tryboot-flag.md). Code:
`internal/update/`, `internal/platform/rpiboot/`.

## What the device fetches

The manifest service (private repos, out of scope here) serves four files per
channel under `<base>/<channel>/` (default base
`https://updates.wxalerts.org/pws/`, override with `--update-url`):

| File | Content |
| --- | --- |
| `manifest.json` | The signed release description below. |
| `manifest.json.sig` | Raw Ed25519 signature over the exact bytes, base64, one line. |
| `delegation.json` | Names the online release keys (release signing page). |
| `delegation.json.sig` | Signature by one of the two pinned root keys. |

Artifact URLs in the manifest are absolute https, or relative to the channel
directory. A staged rollout is the service deciding which devices get a
manifest with a higher `version_number`; the device only checks. Fetches are
spread over devices with a stable per-device delay of up to 30 minutes.

## manifest.json

The fields of the release signing page, plus per-artifact download data:

```json
{
  "schema": 1,
  "version": "1.3.0",
  "version_number": 1030099,
  "os_version_number": 5,
  "channel": "stable",
  "created": "2026-11-17T12:00:00Z",
  "expires": "2027-02-15T12:00:00Z",
  "commit": "abcdef0",
  "artifacts": [
    {
      "name": "pwsgw-linux-arm64", "kind": "app", "os": "linux", "arch": "arm64",
      "sha256": "<64 hex>", "size": 18874368,
      "url": "pwsgw-linux-arm64",
      "chunk_size": 1048576, "chunks": ["<sha256 of each 1 MiB>", "..."]
    },
    {
      "name": "os-boot.img.gz", "kind": "os-boot", "os": "linux", "arch": "arm64",
      "sha256": "...", "size": 0, "url": "os-boot.img.gz",
      "chunk_size": 1048576, "chunks": ["..."], "raw_size": 268435456
    },
    { "name": "os-root.img.gz", "kind": "os-root", "...": "as os-boot" }
  ],
  "rollback": { "from_version_numbers": [1020399], "reason": "1.2.3 breaks the modem" }
}
```

- `kind` is `app` (one `pwsgw` executable), `os-boot` or `os-root` (gzip of the
  raw partition image for the inactive slot, `raw_size` the decompressed size).
- `chunks` has one SHA-256 per `chunk_size` bytes (the last chunk is shorter).
  It makes a download resumable and repairable chunk by chunk on a weak link.
- `os_version_number` numbers the OS image revision. The device installs the OS
  images only when it is higher than the revision it runs. Omit it, and the
  OS artifacts, for an app only release.
- `rollback` is optional. A manifest whose `version_number` is lower than the
  installed one is accepted only when the installed number is in
  `from_version_numbers`. It is part of the signed bytes, so only the release
  signer can authorize one. A rollback never touches the OS image.

## Checks, in order

`update.Trust.Verify`; the first failure stops, nothing is downloaded.

| Code | Meaning |
| --- | --- |
| `delegation_invalid` | Delegation not signed by either pinned root. |
| `signing_key_revoked` | Manifest signed by a key the delegation revokes. |
| `signing_key_expired` | Signed by a key past its `not_after`. |
| `unknown_signing_key` | Signature matches no delegated key. |
| `manifest_invalid` | Structure, version string and version_number disagree, bad chunk list. |
| `manifest_expired` | Past `expires`, on the device clock. |
| `version_lower` | Lower than installed without a matching rollback authorization. |

A rejection is state `failed` with `error` starting with the code, and an
`update_failed` event whose detail has `error` and `code`. The contract is
unchanged. The clock must be NTP synced first: an unsynced device does not
check.

## State machine

States are the contract's: `idle`, `downloading`, `staged`, `rebooting`,
`good`, `rolled_back`, `failed`, `deferred`. State is persisted in
`<data>/update/state.json` and published retained on the `update` topic on
every change.

- Update window (`updates.window`, local time, may wrap midnight). A check runs
  at any time; downloading and switching start only inside the window, else the
  state is `deferred` (the page says why) and the scheduler wakes when the
  window opens. "Install now" in the UI skips the window, never the signature,
  expiry or version rules.
- LTE (`tel.net.link` is `lte`). An OS image is never downloaded. If the
  manifest also has a newer app, the app proceeds and the image is remembered
  as `os_deferred`; if only the image is new, the state is `deferred`.
  Off LTE, a manifest with a newer OS image installs the image, which carries
  the app too.
- Downloads use range requests into a `.part` file, resume from the byte they
  stopped at (a partial file from a previous run is checked chunk by chunk
  first), re-fetch a chunk whose hash is wrong, and back off 2 s doubling to 5
  min. An attempt that made no verified progress counts as a failure; after
  five the state is `deferred` (not `failed`) and the retry waits six hours.
  Response headers slower than 30 s count as a failure (high latency).
- OS image: both partitions of the inactive slot are written and read back,
  `[tryboot]` is pointed at it, the Pi reboots with `0 tryboot`. The new system
  waits up to five minutes for the health check (MQTT connected when claimed,
  a configured radio decoder running), then commits. A failed check, more than
  three starts without a verdict, or a trial that never booted all end in the
  default slot running again; it reports `rolled_back` and a `rollback` event
  and skips that version afterwards.
- App update: the binary is copied to `<data>/update/app/<version_number>/`,
  asked for `version`, and an `active` symlink is switched. The service binary
  hands over to the active slot at start when its version number is higher
  (`cmd/pwsgw/m6.go`). Failure repoints the link and restarts.

## Hardware notes

- The service user needs write access to the autoboot mount and the four slot
  partitions, and permission for `systemctl reboot`, which the current hardened
  unit does not grant. The A/B image carries a drop-in for that; it is
  untested until the image exists.
- Enable the hardware watchdog (`RuntimeWatchdogSec=` in `system.conf`) so a
  hung trial is reset and the firmware falls back to the old slot.
- Not implemented here: shipping new root keys inside a transition release
  (release signing page, "Root key"). Today a root change needs a reflash or
  keys placed in `<data>/keys/`.
