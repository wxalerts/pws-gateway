# Device claim and MQTT auth

## Goal

A Pi connects to WxAlerts MQTT only with a WxAlerts account, without the user's
password ever living on the device.

## Design

- Device-code flow: the Pi UI shows a short code. The user enters it at
  wxalerts.org while logged in. Authentik authorizes the claim.
- Server issues per-device MQTT credentials (client ID = device ID).
- EMQX ACL: device may publish only to `pws/{device_id}/#` and subscribe only
  to `pws/{device_id}/cmd/#`. Contract v2 adds the same rule under
  `V2/{user_id}/PWS/{device_id}/` (see `docs/contract/mqtt-v2.md`).
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

## Implemented (M5, device side)

Code: `internal/claim/`, `internal/web/handlers_claim.go`,
`internal/app/uplink.go`, `web/src/pages/Account.tsx`.

- Endpoints used (API `https://api.wxalerts.org`): `POST /pws/claim/start`
  with `{hw_id, fw, model}` returns `{device_code, user_code, verification_uri,
  verification_uri_complete, expires_in, interval}`. `POST /pws/claim/poll`
  with `{device_code}` answers 400 `authorization_pending`, `slow_down` (with
  the new `interval`), `expired_token` or `access_denied`, or 200 once with
  `{device_id, user_id, mqtt: {url, host, port, transport, path, username, client_id,
  password}, credential_version}`. `user_id` is the owner's numeric account id and selects
  the V2 topics; a server that does not send it leaves the device on v1. A gateway that predates `user_id` learns it with `GET /pws/device/identity` (HTTP Basic with its own credential); see
  `docs/contract/mqtt-v2.md`. A 409 `already_claimed` from start means
  the server still lists this hardware; the owner removes it on the WxAlerts
  site first. The owner approves the code at `https://wxalerts.org/pws/claim`.
- Hardware id: `hw_id = "hw1_" + hex(SHA-256("wxalerts-pws-hwid-v1" NUL
  source ":" value))`. The value is the board serial on a Raspberry Pi
  (`platform.BoardInfo.Serial`), else the machine id
  (`platform.BoardInfo.MachineID`, read from `/etc/machine-id` by the
  adapter), else a random id generated once and kept in `<data-dir>/hwid`.
  The raw serial never leaves the device. The server mints `device_id` from
  an HMAC of `hw_id`, so changing this derivation changes every device id; a
  test pins a vector.
- State machine, persisted in `<data-dir>/claim.json` (mode 0600, holds the
  poll secret): `unclaimed`, `pending` (user code, expiry) and `claimed`
  (device id). A restart resumes a pending claim. Polling uses the server
  interval, raises it on `slow_down`, retries through outages and stops at the
  local expiry.
- On success the credential file named by `uplink.credential_file` (default
  `<data-dir>/uplink.credential`, mode 0600, `{"username","password"}`) is
  written, and `uplink.device_id` and `uplink.broker_url` are set through
  `config.Store`. The app then builds and runs the uplink without a restart.
- Transport: the broker URL is `wss://pws-mqtt.wxalerts.org/mqtt`. The
  uplink's paho.golang `autopaho` client already dials `ws` and `wss` URLs
  (subprotocol `mqtt`), so no dial code was added; tests connect to mochi-mqtt's
  websocket listener with and without TLS.
- Unlink (`POST /api/claim/unlink`) stops the uplink, deletes the credential
  file and clears `uplink.device_id` and `uplink.broker_url`. It does not
  contact the server.
- Web API: `GET /api/claim`, `POST /api/claim/start`, `POST /api/claim/cancel`,
  `POST /api/claim/unlink`, all behind the session and the CSRF header. The
  UI page is "WxAlerts account"; the dashboard shows a banner while unlinked.

## Open

- mTLS in addition to username/password?
- Device cap for paid tiers. Free tier is one Pi per account (decided).
