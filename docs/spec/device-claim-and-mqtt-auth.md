# Device claim and MQTT auth

## Goal

A Pi connects to WxAlerts MQTT only with a WxAlerts account, without the user's
password ever living on the device.

## Design

- Device-code flow: the Pi UI shows a short code. The user enters it at
  wxalerts.org while logged in. Authentik authorizes the claim.
- Server issues per-device MQTT credentials (client ID = device ID).
- EMQX ACL: device may publish only to `pws/{device_id}/#` and subscribe only
  to `pws/{device_id}/cmd/#`.
- LWT on `pws/{device_id}/status` for offline detection. Retained `online`
  message on connect.
- Revoke: owner can unlink a device from the dashboard. Credentials rotate on
  re-claim.
- Rate limits and payload size limits enforced at the broker.

## Repo notes (2026-11-16)

- The device side of this flow (showing the code, polling for approval, storing
  the issued credentials) is in this repo. The API endpoints, the Authentik
  integration and the EMQX configuration are in the private WxAlerts
  repositories; see `docs/server-integration.md`.
- Credentials are stored on the device with owner-only permissions and are
  never included in any MQTT payload, support bundle or log line.

## Open

- mTLS in addition to username/password?
- Device cap for paid tiers. Free tier is one Pi per account (decided).
