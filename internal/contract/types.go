// Package contract holds the Go form of the WxAlerts PWS MQTT contract: one
// type per message, validation against the JSON Schemas in schemas/, RFC 8785
// canonical JSON, and Ed25519 signing and verification of commands.
//
// The JSON tags on these types match the schemas exactly. Timestamps are
// time.Time values that must be in UTC when a message is marshalled; the
// schema rejects numeric offsets, so Marshal fails loudly on a local time.
// Optional numeric fields are pointers so that a legitimate zero is sent and
// an absent value is omitted.
package contract

import (
	"encoding/json"
	"time"
)

// Version is the payload version this package produces in the v field.
const Version = 0

// MinAcceptedVersion is the oldest payload version a receiver accepts. The
// contract says a server accepts v and v-1; while v is 0 there is no older
// version.
const MinAcceptedVersion = 0

// MaxMessageBytes is the largest MQTT payload the contract allows (64 KB). A
// larger backlog is split by bucket before publishing.
const MaxMessageBytes = 64 * 1024

// AcceptsVersion reports whether a receiver built from this package accepts
// a payload that carries version v.
func AcceptsVersion(v int) bool {
	return v >= MinAcceptedVersion && v <= Version
}

// Kind names one message type of the contract. The string value is the
// topic suffix under pws/{device_id}/ (for cmd and ack, the first segment)
// and the schema file prefix.
type Kind string

// Message kinds.
const (
	KindObs    Kind = "obs"
	KindTel    Kind = "tel"
	KindUpdate Kind = "update"
	KindEvent  Kind = "event"
	KindStatus Kind = "status"
	KindCmd    Kind = "cmd"
	KindAck    Kind = "ack"
)

// Kinds lists every message kind in a stable order.
var Kinds = []Kind{KindObs, KindTel, KindUpdate, KindEvent, KindStatus, KindCmd, KindAck}

// Message is implemented by every payload type so that Marshal and Unmarshal
// can pick the right schema.
type Message interface {
	Kind() Kind
}

// Obs is one observation message: one or more buckets of aggregated station
// readings. Published on pws/{device_id}/obs.
type Obs struct {
	V         int       `json:"v"`
	DeviceID  string    `json:"device_id"`
	Seq       uint64    `json:"seq"`
	Sent      time.Time `json:"sent"`
	IntervalS int       `json:"interval_s"`
	Buckets   []Bucket  `json:"buckets"`
}

// Kind implements Message.
func (*Obs) Kind() Kind { return KindObs }

// Bucket holds the aggregates of every selected station for one interval
// starting at Start. Stations must be non-nil (an empty slice is fine); a nil
// slice marshals as null, which the schema rejects.
type Bucket struct {
	Start    time.Time `json:"start"`
	Stations []Station `json:"stations"`
}

// Station holds the aggregated readings of one station in one bucket.
type Station struct {
	// Key is model:id:channel, see common.schema.json#/$defs/station_key.
	Key string `json:"key"`
	// N is the number of decoded packets that went into the bucket.
	N    int          `json:"n"`
	Meta *StationMeta `json:"meta,omitempty"`
	// F holds the canonical fields. It is always emitted, as {} when empty.
	F   Fields  `json:"f"`
	Sig *Signal `json:"sig,omitempty"`
	// Raw passes through decoder fields with no canonical mapping. It must be
	// a JSON object when present.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// StationMeta carries the site metadata the server needs to derive sea level
// pressure and 10 m wind.
type StationMeta struct {
	AltM        *float64 `json:"alt_m,omitempty"`
	WindHeightM *float64 `json:"wind_height_m,omitempty"`
}

// Signal is the radio link quality for a station over the bucket.
type Signal struct {
	RSSIdB *float64 `json:"rssi_db,omitempty"`
	SNRdB  *float64 `json:"snr_db,omitempty"`
}

// Gauge is a sampled quantity summarised by avg, min and max. At least one
// statistic must be set.
type Gauge struct {
	Avg *float64 `json:"avg,omitempty"`
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

// Direction is a vector mean in compass degrees, 0 <= Vec < 360.
type Direction struct {
	Vec float64 `json:"vec"`
}

// Counter is the sum of increments during the bucket after reset handling.
type Counter struct {
	Sum float64 `json:"sum"`
}

// Flag is the last value seen in the bucket, 1 or 0.
type Flag struct {
	Last int `json:"last"`
}

// Fields holds the canonical v1 observation fields of one station. The field
// type of each name is fixed by the contract. Non-canonical decoder fields go
// in Station.Raw, not here.
type Fields struct {
	TempC              *Gauge     `json:"temp_c,omitempty"`
	HumidityPct        *Gauge     `json:"humidity_pct,omitempty"`
	PressureStationHPa *Gauge     `json:"pressure_station_hpa,omitempty"`
	WindSpeedMS        *Gauge     `json:"wind_speed_ms,omitempty"`
	WindGustMS         *Gauge     `json:"wind_gust_ms,omitempty"`
	WindDirDeg         *Direction `json:"wind_dir_deg,omitempty"`
	RainMM             *Counter   `json:"rain_mm,omitempty"`
	UVIndex            *Gauge     `json:"uv_index,omitempty"`
	LightLux           *Gauge     `json:"light_lux,omitempty"`
	SolarWM2           *Gauge     `json:"solar_wm2,omitempty"`
	LightningCount     *Counter   `json:"lightning_count,omitempty"`
	LightningKM        *Gauge     `json:"lightning_km,omitempty"`
	BatteryOK          *Flag      `json:"battery_ok,omitempty"`
}

// CanonicalFields lists the canonical v1 field names in contract order.
var CanonicalFields = []string{
	"temp_c", "humidity_pct", "pressure_station_hpa", "wind_speed_ms",
	"wind_gust_ms", "wind_dir_deg", "rain_mm", "uv_index", "light_lux",
	"solar_wm2", "lightning_count", "lightning_km", "battery_ok",
}

// Tel is the device health message published on pws/{device_id}/tel.
type Tel struct {
	V        int       `json:"v"`
	DeviceID string    `json:"device_id"`
	TS       time.Time `json:"ts"`
	FW       string    `json:"fw"`
	OS       string    `json:"os,omitempty"`
	UptimeS  int64     `json:"uptime_s"`
	Time     TimeSync  `json:"time"`
	Sys      Sys       `json:"sys"`
	Outbox   Outbox    `json:"outbox"`
	SDR      []SDR     `json:"sdr,omitempty"`
	Net      Net       `json:"net"`
	Modem    *Modem    `json:"modem,omitempty"`
	GPS      *GPS      `json:"gps,omitempty"`
}

// Kind implements Message.
func (*Tel) Kind() Kind { return KindTel }

// TimeSync is the clock state. Observations are only uplinked while Synced.
type TimeSync struct {
	Src      string  `json:"src"`
	OffsetMS float64 `json:"offset_ms"`
	Synced   bool    `json:"synced"`
}

// Sys is board health. Every reading is optional because boards differ.
type Sys struct {
	CPUTempC    *float64 `json:"cpu_temp_c,omitempty"`
	Throttled   *int     `json:"throttled,omitempty"`
	MemFreeMB   *float64 `json:"mem_free_mb,omitempty"`
	DataUsedPct *float64 `json:"data_used_pct,omitempty"`
}

// Outbox is the store and forward queue state.
type Outbox struct {
	Depth   int   `json:"depth"`
	OldestS int64 `json:"oldest_s"`
}

// SDR describes one attached radio.
type SDR struct {
	RadioID    string   `json:"radio_id,omitempty"`
	Driver     string   `json:"driver,omitempty"`
	BandMHz    *float64 `json:"band_mhz,omitempty"`
	Decoders   *int     `json:"decoders,omitempty"`
	Packets15m *int     `json:"packets_15m,omitempty"`
}

// SDR driver names.
const (
	DriverRTL433  = "rtl_433"
	DriverDavis   = "davis"
	DriverCapture = "capture"
)

// Net is the uplink summary. No addresses are sent.
type Net struct {
	Link string `json:"link"`
}

// Modem is the cellular modem state, present only when a modem is attached.
type Modem struct {
	Carrier string   `json:"carrier,omitempty"`
	MCCMNC  string   `json:"mcc_mnc,omitempty"`
	Reg     string   `json:"reg,omitempty"`
	Tech    string   `json:"tech,omitempty"`
	RSSIdBm *float64 `json:"rssi_dbm,omitempty"`
}

// GPS is the GNSS receiver state, present only when a receiver is attached.
type GPS struct {
	Fix  *int     `json:"fix,omitempty"`
	Sats *int     `json:"sats,omitempty"`
	Lat  *float64 `json:"lat,omitempty"`
	Lon  *float64 `json:"lon,omitempty"`
	AltM *float64 `json:"alt_m,omitempty"`
}

// Update is the retained version and update state message published on
// pws/{device_id}/update. V and DeviceID are optional in the current draft
// because the contract example omits them.
type Update struct {
	V        *int      `json:"v,omitempty"`
	DeviceID string    `json:"device_id,omitempty"`
	Current  string    `json:"current"`
	Target   *string   `json:"target"`
	State    string    `json:"state"`
	Error    *string   `json:"error"`
	TS       time.Time `json:"ts"`
}

// Kind implements Message.
func (*Update) Kind() Kind { return KindUpdate }

// Update states.
const (
	UpdateIdle        = "idle"
	UpdateDownloading = "downloading"
	UpdateStaged      = "staged"
	UpdateRebooting   = "rebooting"
	UpdateGood        = "good"
	UpdateRolledBack  = "rolled_back"
	UpdateFailed      = "failed"
	UpdateDeferred    = "deferred"
)

// Event is an immediate notification published on pws/{device_id}/event.
type Event struct {
	V        int             `json:"v"`
	DeviceID string          `json:"device_id,omitempty"`
	TS       time.Time       `json:"ts"`
	Type     string          `json:"type"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}

// Kind implements Message.
func (*Event) Kind() Kind { return KindEvent }

// Event types.
const (
	EventTimeUnsynced   = "time_unsynced"
	EventSensorLost     = "sensor_lost"
	EventSensorFound    = "sensor_found"
	EventUpdateFailed   = "update_failed"
	EventRollback       = "rollback"
	EventSDRMissing     = "sdr_missing"
	EventOutboxOverflow = "outbox_overflow"
)

// Status is the retained connection state on pws/{device_id}/status. On the
// wire it is a JSON string, quotes included. The Message methods use a
// pointer receiver so that New can return an addressable value to decode
// into; pass &status to Marshal.
type Status string

// Kind implements Message.
func (*Status) Kind() Kind { return KindStatus }

// Status values.
const (
	StatusOnline  Status = "online"
	StatusOffline Status = "offline"
)

// Command is a signed server to device command published on
// pws/{device_id}/cmd/{type}. Sig covers the RFC 8785 canonical JSON of
// {id, type, device_id, exp, body}; see SignCommand and VerifyCommand.
type Command struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	DeviceID string          `json:"device_id"`
	Exp      time.Time       `json:"exp"`
	Body     json.RawMessage `json:"body"`
	Sig      string          `json:"sig"`

	// signingInput caches the canonical signing input derived from the exact
	// bytes a command was parsed from, so verification does not depend on
	// this package re-serialising the fields identically. Set by
	// ParseCommand.
	signingInput []byte
}

// Kind implements Message.
func (*Command) Kind() Kind { return KindCmd }

// Command types.
const (
	CmdConfig      = "config"
	CmdReboot      = "reboot"
	CmdBundle      = "bundle"
	CmdUpdateCheck = "update-check"
	CmdResync      = "resync"
	CmdDiag        = "diag"
	CmdCapture     = "capture"
)

// ResyncBody is the body of a resync command.
type ResyncBody struct {
	From time.Time  `json:"from"`
	To   *time.Time `json:"to,omitempty"`
}

// DiagBody is the body of a diag command.
type DiagBody struct {
	Action  string `json:"action"`
	Service string `json:"service,omitempty"`
}

// Diag actions, the full allow list.
const (
	DiagRestartService   = "restart_service"
	DiagRediscover       = "rediscover"
	DiagConnectivityTest = "connectivity_test"
	DiagClearOutbox      = "clear_outbox"
)

// Ack is the command result published on pws/{device_id}/ack/{cmd_id}.
type Ack struct {
	V        int             `json:"v"`
	ID       string          `json:"id"`
	DeviceID string          `json:"device_id,omitempty"`
	Type     string          `json:"type,omitempty"`
	OK       bool            `json:"ok"`
	Error    *string         `json:"error,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	TS       time.Time       `json:"ts"`
}

// Kind implements Message.
func (*Ack) Kind() Kind { return KindAck }

// New returns an empty message of the given kind, or nil for an unknown
// kind. It lets callers that dispatch on a topic unmarshal into the right
// type.
func New(kind Kind) Message {
	switch kind {
	case KindObs:
		return &Obs{}
	case KindTel:
		return &Tel{}
	case KindUpdate:
		return &Update{}
	case KindEvent:
		return &Event{}
	case KindStatus:
		return new(Status)
	case KindCmd:
		return &Command{}
	case KindAck:
		return &Ack{}
	}
	return nil
}
