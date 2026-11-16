# Out of scope for v1

These are not built in v1. Coding agents and contributors should open an issue
rather than start on any of them. Each item says why it is deferred and what
would bring it back in.

## Davis Instruments module (rtldavis)

Why deferred: Davis Vantage stations hop across 902 to 928 MHz and `rtl_433`
cannot decode them. The only RTL-SDR route is rtldavis, which is GPL-3.0, has
an archived upstream and a fork of uncertain activity, reportedly needs an
older Go toolchain, must own its dongle while hopping, and needs transmitter IDs
configured. None of that has been tested on a Pi 3B+ with a real Davis ISS. The
contract already reserves the `davis` driver and the `davis:{model}:{tx_id}`
key format so the module can be added without a contract change.

Revisit when: the pre-build research on the Notion RF capture page is done
(license, activity, CPU load, timing, dongle sharing, transmitter ID
discovery), a Davis ISS and a TCXO dongle have been tested on a Pi 3B+, and the
process boundary (separate GPL process, stdout JSON) is confirmed to work.

## Onboarding wizard

Why deferred: v1 assigns radio roles manually on the Radios page and sets up
stations from the station list. A guided wizard adds UI surface and state
without changing what the daemon can do, and the right flow is not known until
real users have gone through the manual pages.

Revisit when: the Radios and station pages are stable (after M3) and support
questions show where people get stuck.

## Listen mode and protocol capture assist

Why deferred: capture takes over a dongle, uploads data that may contain other
people's RF traffic, and raises retention, license and scrubbing questions that
are all still open. It is a v2 feature by decision. The `capture` role, command
type and driver value are reserved in the contract so nothing has to be renamed
later.

Revisit when: the three open questions (capture retention, license terms for
contributed captures, server-side scrubbing) are answered and the v1 fleet is
stable.

## Non-Pi boards

Why deferred: Raspberry Pi only at launch (3B+, 4, 5, 64-bit). Supporting other
boards means other bootloaders, A/B schemes, GPIO and power quirks. The
platform adapter boundary in `internal/platform/` and the multi-arch containers
exist precisely so a board can be added later without touching the rest.

Revisit when: the Pi image, updater and rollback are proven on 3B+, 4 and 5,
and a specific board with a known A/B story is requested by enough users.

## Flutter dashboard component

Why deferred: the PWS dashboard is web only at launch. The Flutter component
in the WxAlerts mobile app comes after the web pages have settled. This is
server and app side (private repositories) in any case.

Revisit when: the web dashboard pages (M8) have shipped and the contract has
frozen at `v: 1`, so the app is not chasing a moving payload.

## zstd compression

Why deferred: the contract is plain JSON, no compression, in v1. A 15-minute
batch is a few KB per station, which is small even on LTE. Compression adds a
dependency on both sides and makes payloads harder to inspect on the broker.

Revisit when: LTE data use measured in telemetry (`sys.data_used_pct`) becomes
a concern for real users, or a user plan with a very small data cap is
targeted. It would be an additive negotiation (a content-encoding flag), not a
contract version bump.

## Remote shell of any kind

Why deferred: not deferred, excluded. The device executes no shell command from
the server, there is no install command, and updates arrive only through the
signed manifest. Remote support in v1 is a support bundle and a closed
allowlist of `diag` actions, both as signed commands. A time-limited, opt-in
remote session is a later item and is not a shell either; an always-on mesh VPN
is excluded. See ADR 0006 and ground rule 4 in `AGENTS.md`.

Revisit when: never for a general shell. The opt-in remote session is designed
separately, with its own ADR, after launch.
