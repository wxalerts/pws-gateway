# Network configuration (failback-safe)

## Goal

Configure networking in the web UI without ever stranding a remote station.

## Design

- NetworkManager for Ethernet, multiple Wi-Fi networks, and LTE.
- Commit-confirm: apply a change, then revert automatically in 90 seconds unless
  the browser confirms.
- AP-mode fallback hotspot for setup when no known network is reachable.
- Connectivity check drives failover between WAN links.
- Static IP, DNS, and hostname options.
- Network state reported over MQTT (`tel.net`).

## Repo notes (2026-11-16)

- The commit-confirm pattern is the same one used for config pushed by signed
  `config` commands (see the contract) and for boot config changes such as
  enabling I2C. One implementation, three callers.
- NetworkManager is reached over D-Bus through `internal/platform/`. Tests use
  a fake that records the apply and revert calls and a fake clock to drive the
  90 s timer.
