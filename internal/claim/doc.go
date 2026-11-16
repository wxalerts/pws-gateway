// Package claim links the gateway to a WxAlerts account with the device code
// flow (RFC 8628 shaped).
//
// The gateway asks the WxAlerts API for a short code (Start), shows it on its
// own web page, and polls (Poll) until the account holder enters the code at
// the WxAlerts site. The poll that follows the approval returns the MQTT
// credentials exactly once. The gateway never holds account credentials.
//
// The hardware id sent to the server is derived on the device from the board
// serial (Raspberry Pi) or the machine id (other hosts), hashed with a fixed
// domain string, so the raw serial never leaves the device. The server mints
// the device id from it; see HardwareID.
//
// State lives in <data-dir>/claim.json so a restart resumes a pending claim.
// On success the credential file the uplink reads is written, uplink.device_id
// and uplink.broker_url are set in the config, and the OnClaimed hook starts
// the uplink without a restart.
package claim
