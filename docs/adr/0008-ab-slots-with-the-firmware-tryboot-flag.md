# ADR 0008: A/B slots with the firmware tryboot flag

Status: accepted 2026-11-17.

## Context

The gateway must update its operating system without ever leaving a station
offline. `docs/spec/auto-update-and-rollback.md` left the A/B mechanism open for
the Pi 3B+ (U-Boot with RAUC, Mender or a custom switch). Issue 1 settled it
from the Raspberry Pi documentation ("Fail-safe OS updates (tryboot)"): every
Pi model supports the firmware `tryboot` flag, `autoboot.txt` with
`boot_partition` and a `[tryboot]` section, and `tryboot_a_b=1`.

## Decision

- One partition layout on every supported board: a small FAT partition holding
  only `autoboot.txt`, boot partitions A and B (each with its own `config.txt`,
  kernel and `cmdline.txt` naming its own root), root partitions A and B, and a
  data partition shared by both slots.
- A trial boot is `reboot '0 tryboot'`. The firmware boots the `[tryboot]`
  partition once. Any later boot (power cut, watchdog reset, an ordinary
  restart) goes back to the `[all]` partition, so a new slot that never
  becomes healthy is abandoned without any code running.
- The new slot confirms itself only after the post-boot health check passes,
  by swapping the two `boot_partition` values (`Commit`).
- The board specific parts live behind `platform.BootSlots`
  (`internal/platform/rpiboot` on a Pi, a fake in tests). No U-Boot, RAUC or
  Mender.
- App updates (a new `pwsgw` binary only) use a second, lighter slot under the
  data directory so the read only OS image is untouched; see
  `docs/spec/update-manifest-and-slots.md`.

## Consequences

- The 3B+ needs a firmware recent enough to carry the `[tryboot]` filter. The
  firmware statement is documented, not yet exercised on a 3B+ here, so the
  3B+ stays "unverified" until the rollback run on that board passes.
- The bench Pi 4 is a single partition card, so the rollback acceptance item
  needs an A/B card image that does not exist yet.
- The service needs write access to the autoboot partition and the slot block
  devices, and permission to reboot. That is a deliberate widening of the
  hardened unit, scoped in the image (see the spec page).
