package model

import (
	"sort"
	"strings"
	"time"
)

// Signal is the radio telemetry rtl_433 attaches to a decoded message when
// run with "-M level". All levels are in dB relative to full scale, as
// rtl_433 reports them. FreqMHz is the measured carrier of the pulse train
// (rtl_433 key "freq", or "freq1" for FSK) and is zero when absent.
type Signal struct {
	RSSI    float64 `json:"rssi_db"`
	SNR     float64 `json:"snr_db"`
	Noise   float64 `json:"noise_db"`
	FreqMHz float64 `json:"freq_mhz,omitempty"`
	// HasLevel is false when the message carried no level metadata, which
	// happens when rtl_433 runs without "-M level" or on replayed files that
	// were recorded without it. Zero dB is a valid level, so a flag is needed.
	HasLevel bool `json:"has_level"`
}

// Reading is one decoded rtl_433 message after mapping to canonical fields.
type Reading struct {
	// Model, ID and Channel identify the transmitter. Channel is empty for
	// devices without one. Together they form the station key.
	Model   string `json:"model"`
	ID      string `json:"id"`
	Channel string `json:"channel"`

	// Time is the moment the message was received, in UTC. It is the zero
	// value until the collector has a synced clock; see the collector
	// package for the policy.
	Time time.Time `json:"time"`
	// Received is the local monotonic receipt time used to stamp the reading
	// once the clock syncs. Callers outside the collector should use Time.
	Received time.Time `json:"-"`

	// Radio is the identifier of the radio that heard the message, so that a
	// station heard by two dongles can be attributed to one.
	Radio string `json:"radio"`

	// Fields holds canonical numeric values keyed by registry name.
	Fields map[string]float64 `json:"fields,omitempty"`
	// Flags holds canonical boolean values such as battery_ok.
	Flags map[string]bool `json:"flags,omitempty"`
	// Raw holds every decoder field with no canonical mapping, unconverted
	// and under its rtl_433 name. This is what makes unknown decoders usable
	// on day one.
	Raw map[string]any `json:"raw,omitempty"`

	Signal Signal `json:"signal"`
	// Protocol is the rtl_433 decoder number from "-M protocol", or 0.
	Protocol int `json:"protocol,omitempty"`
}

// StationKey builds the key "model:id:channel". Channel may be empty, in
// which case the key ends with a colon so that keys parse unambiguously.
func StationKey(model, id, channel string) string {
	return model + ":" + id + ":" + channel
}

// SplitStationKey is the inverse of StationKey. It returns ok=false when the
// key does not have exactly three parts. Model names from rtl_433 never
// contain a colon, and ids are integers or short hex strings.
func SplitStationKey(key string) (model, id, channel string, ok bool) {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// StationKey returns the station key for the reading.
func (r Reading) StationKey() string {
	return StationKey(r.Model, r.ID, r.Channel)
}

// FieldNames returns the canonical field and flag names present in the
// reading, sorted.
func (r Reading) FieldNames() []string {
	names := make([]string, 0, len(r.Fields)+len(r.Flags))
	for k := range r.Fields {
		names = append(names, k)
	}
	for k := range r.Flags {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Station is a transmitter that passed the discovery filter.
type Station struct {
	Key     string `json:"key"`
	Model   string `json:"model"`
	ID      string `json:"id"`
	Channel string `json:"channel"`

	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// Sightings counts every accepted message since FirstSeen.
	Sightings int `json:"sightings"`
	// Fields lists the canonical fields and flags ever seen from this
	// station, sorted. A station that only produces Raw data has none.
	Fields []string `json:"fields"`
	// RawFields lists the rtl_433 field names seen under Raw, sorted.
	RawFields []string `json:"raw_fields,omitempty"`

	LastSignal Signal `json:"last_signal"`
	// BatteryOK is nil when the device never reported a battery flag.
	BatteryOK *bool `json:"battery_ok,omitempty"`
	// DecodeRate is messages per minute over the discovery rate window.
	DecodeRate float64 `json:"decode_rate_per_min"`
	// Lost is set when the station has been quiet longer than the configured
	// quiet period. It clears on the next sighting.
	Lost bool `json:"lost"`
	// Radio is the radio that last heard the station.
	Radio string `json:"radio"`
}

// Aggregate is one field's summary within a bucket. Which members are
// meaningful depends on Kind, and the layout mirrors the rollups table so a
// row round trips without translation:
//
//	gauge:     N, Avg, Min, Max, Sum
//	direction: N, Avg (vector mean, 0 <= Avg < 360), Vec (resultant length
//	           divided by N, 1 means every sample agreed)
//	counter:   N (deltas), Sum (total delta, resets handled), Last (last raw
//	           counter value), Min and Max (raw counter range)
//	flag:      N, Last (1 or 0)
type Aggregate struct {
	Kind Kind    `json:"kind"`
	N    int     `json:"n"`
	Avg  float64 `json:"avg"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Sum  float64 `json:"sum"`
	Vec  float64 `json:"vec"`
	Last float64 `json:"last"`
}

// Bucket is one station's readings over one 5-minute window.
type Bucket struct {
	// Start is the aligned bucket start in UTC.
	Start      time.Time `json:"start"`
	StationKey string    `json:"station_key"`
	// N is the number of readings that fell into the bucket, regardless of
	// which fields they carried.
	N      int                  `json:"n"`
	Fields map[string]Aggregate `json:"fields"`
}

// FieldNames returns the bucket's field names, sorted, so output is stable.
func (b Bucket) FieldNames() []string {
	names := make([]string, 0, len(b.Fields))
	for k := range b.Fields {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
