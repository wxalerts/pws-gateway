# Security policy

The gateway runs unattended in people's homes, updates itself and holds the
credentials for their MQTT uplink. Security reports are taken seriously and
handled quickly.

## Supported versions

Nothing is supported yet. The project is pre-alpha and the MQTT contract is
still `v: 0`. This table will list the release lines that receive security
fixes once `v1.0.0` ships.

| Version | Supported |
| --- | --- |
| none yet | no |

## Reporting a vulnerability

Report privately through GitHub's vulnerability reporting:

https://github.com/wxalerts/pws-gateway/security/advisories/new

Do not open a public issue, pull request or discussion for anything you think
might be a security problem. If you are unsure whether something qualifies,
report it privately anyway; a false alarm costs nothing.

Include what you can of: the gateway version, the board and OS image, steps
to reproduce, and what an attacker gains. A proof of concept helps but is not
required.

You will get an acknowledgement within 7 days and a first assessment within
14 days.

## Scope

In scope, in rough order of severity:

- Signed update bypass: any way to make a device install a release that was
  not signed by the delegated online key under a pinned root key, including
  rollback to an older signed release, replay of an expired manifest, or
  tampering with `keys/`.
- Command signature bypass: any way to make the device act on a server
  message that was not properly signed, or to make it execute anything beyond
  the fixed set of signed commands. The device never executes shell commands
  from the server by design.
- Local web UI authentication: bypassing the password, session handling,
  rate limiting or HTTPS on the device's web UI, including from another host
  on the same LAN.
- Secret leakage: MQTT credentials, web UI password hashes, TLS private keys
  or the support bundle exposing anything it is meant to strip.
- Anything in the release pipeline (`.github/workflows/`) that would let a
  pull request from a fork reach secrets or influence what gets signed.

Out of scope:

- Attacks that need physical access to the SD card or the Pi's console.
  Full-disk encryption on a headless Pi is not a v1 goal.
- Jamming or spoofing 433 MHz / 915 MHz sensor transmissions. The radio
  protocols have no authentication and the gateway cannot fix that; it does
  mark readings with signal metadata so the server side can filter.
- Vulnerabilities in `rtl_433`, `librtlsdr` or Raspberry Pi OS themselves.
  Report those upstream; tell us too if the gateway's configuration makes
  them worse.
- Denial of service against the local web UI from the LAN beyond what rate
  limiting covers.

## Disclosure

Coordinated disclosure with a 90 day window from the first report. We aim to
ship a fix well inside that window and will publish a GitHub security
advisory with credit to the reporter (unless they prefer otherwise) when the
fix is released. If a fix needs longer, we will say so and agree a new date
with you rather than go silent.

## What the device trusts

Devices trust only the public keys pinned under `keys/` at build time:
`keys/root.pub` and `keys/root-backup.pub`. Everything else, including the
online release signing key, is trusted only because a file signed by one of
those roots says so. There is no "trust on first use", no key fetched from a
server, and no way to add a key from the web UI. See
`docs/release-signing.md` for the full chain and what the device checks before
it installs an update.
