# Retention tiers

## Goal

Sell longer data retention.

## Design

- Free tier: short retention, full real-time view.
- Paid tier: months to years of history.
- Retention job enforces limits server-side. Downsample older data instead of
  deleting where possible.
- Fits the existing tier structure and in-app billing.

## Repo notes (2026-11-16)

- Retention is entirely server-side (private repositories). The device keeps
  its own local history regardless of tier, bounded only by the SD card and
  the user-set cadence.

## Open

- Free window length. A paid upgrade for additional storage is agreed, and its
  price is to be determined.
- Free tier is one Pi per account (decided).
