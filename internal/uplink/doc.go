// Package uplink is the device side of the WxAlerts MQTT contract: it
// connects to the broker, publishes obs, tel, event and update messages,
// accepts signed commands and acknowledges them.
//
// The shape of the package follows three rules from the specs.
//
// Outbox first. Every message that has to arrive (obs, tel, event, ack) is
// written to the SQLite outbox before anything touches the network, and is
// deleted only after the broker answers PUBACK. A crash between build and
// publish therefore never loses a message; at worst it is sent twice, and the
// server dedupes on (device_id, seq).
//
// Time gate. No obs message is built while the platform reports the clock as
// unsynced, because bucket starts would be meaningless.
//
// Commands are data, not code. A command is parsed, checked against a closed
// type list, verified with the pinned Ed25519 key, and dispatched to a typed
// handler. There is no shell command and no path from a command body to an
// exec call (docs/adr/0006-signed-commands-no-shell.md).
package uplink
