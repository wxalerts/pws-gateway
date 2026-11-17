// Package update is the signed self-update of the gateway.
//
// The flow is fetch (a manifest, its signature and the key delegation from
// the WxAlerts manifest service), verify (two pinned root keys, one online
// release key, expiry and version rules), download (ranged, resumable, a hash
// per chunk), stage, switch (an A/B slot reboot for an OS image, a restarted
// binary for an app update), health check, then either commit or roll back.
//
// Everything with a side effect is reached through an interface so the tests
// run with fakes and a fake clock: the HTTP source, the A/B boot slots
// (platform.BootSlots), the app slot directory, the health check and the
// event sink. The device never runs a command from the server (AGENTS.md rule
// 4): the only thing it ever executes is the new pwsgw binary after its hash
// has been checked against the signed manifest, with the single argument
// "version".
//
// The signature format is the one in docs/release-signing.md: a raw Ed25519
// signature over the exact bytes of the signed file, base64 on one line.
package update
