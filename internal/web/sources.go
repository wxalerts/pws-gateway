package web

import (
	"context"
	"time"
)

// StationInfo is what the collector knows about one discovered station. The
// web layer merges it with the owner's annotations from the config.
type StationInfo struct {
	Key        string
	Model      string
	ID         int
	Channel    *int
	LastSeen   time.Time
	RSSIdB     float64
	SNRdB      float64
	BatteryOK  bool
	Fields     []string
	NSightings int
}

// StationSource lists the stations seen so far. The collector adapts the
// discovery tracker to this interface.
type StationSource interface {
	Stations(ctx context.Context) ([]StationInfo, error)
}

// Outbox describes the uplink queue.
type Outbox struct {
	Depth   int `json:"depth"`
	OldestS int `json:"oldest_s"`
}

// UplinkStatus is the live MQTT state. Broker and DeviceID may be left empty,
// in which case the handler fills them from the configuration.
type UplinkStatus struct {
	Connected   bool
	Broker      string
	DeviceID    string
	LastPublish *time.Time
}

// UpdateStatus is the self-update state machine as the UI shows it.
type UpdateStatus struct {
	Current string  `json:"current"`
	Target  *string `json:"target"`
	State   string  `json:"state"`
	Error   *string `json:"error"`
	TS      string  `json:"ts"`
}

// StatusSource reports live runtime state owned by other subsystems.
type StatusSource interface {
	Outbox(ctx context.Context) Outbox
	Uplink(ctx context.Context) UplinkStatus
	Update(ctx context.Context) UpdateStatus
	// RadioStatus returns the supervisor state for one dongle ("running",
	// "starting", "idle", ...) or "" when unknown.
	RadioStatus(ctx context.Context, serial string) string
}

// Reading is one stored observation as the charts consume it.
type Reading struct {
	TS time.Time          `json:"ts"`
	F  map[string]float64 `json:"f"`
}

// ReadingsSource returns readings for one station at or after since.
type ReadingsSource interface {
	Readings(ctx context.Context, station string, since time.Time) ([]Reading, error)
}

// NopStatus is the default StatusSource: empty outbox, disconnected uplink
// and an idle updater.
type NopStatus struct {
	// Version is reported as the current firmware in UpdateStatus.
	Version string
}

// Outbox implements StatusSource.
func (NopStatus) Outbox(context.Context) Outbox { return Outbox{} }

// Uplink implements StatusSource.
func (NopStatus) Uplink(context.Context) UplinkStatus { return UplinkStatus{} }

// Update implements StatusSource.
func (n NopStatus) Update(context.Context) UpdateStatus {
	return UpdateStatus{Current: n.Version, State: "idle", TS: time.Now().UTC().Format(time.RFC3339)}
}

// RadioStatus implements StatusSource.
func (NopStatus) RadioStatus(context.Context, string) string { return "" }

// NopStations is the default StationSource.
type NopStations struct{}

// Stations implements StationSource.
func (NopStations) Stations(context.Context) ([]StationInfo, error) { return nil, nil }

// NopReadings is the default ReadingsSource.
type NopReadings struct{}

// Readings implements ReadingsSource.
func (NopReadings) Readings(context.Context, string, time.Time) ([]Reading, error) { return nil, nil }
