package web

import (
	"context"
	"errors"
	"time"
)

// StationInfo is what the collector knows about one discovered station. The
// web layer merges it with the owner's annotations from the config.
type StationInfo struct {
	Key      string
	Model    string
	ID       int
	Channel  string
	LastSeen time.Time
	RSSIdB   float64
	SNRdB    float64
	// BatteryOK is nil when the station never reported a battery flag.
	BatteryOK *bool
	// Fields lists the canonical fields seen, RawFields the unmapped decoder
	// field names seen.
	Fields     []string
	RawFields  []string
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

// ClaimStatus is the device-claim state as the UI shows it. The pointer
// fields are null in the JSON while they do not apply.
type ClaimStatus struct {
	// State is unclaimed, pending or claimed.
	State                   string  `json:"state"`
	UserCode                *string `json:"user_code"`
	VerificationURI         *string `json:"verification_uri"`
	VerificationURIComplete *string `json:"verification_uri_complete"`
	ExpiresAt               *string `json:"expires_at"`
	DeviceID                *string `json:"device_id"`
	// Error is why the last claim ended: expired, denied or failed.
	Error *string `json:"error"`
}

// Errors a ClaimSource returns from ClaimStart, mapped to HTTP statuses.
var (
	// ErrClaimLinked means the gateway is already linked here.
	ErrClaimLinked = errors.New("web: gateway is already linked")
	// ErrClaimTaken means the server still lists this hardware as linked.
	ErrClaimTaken = errors.New("web: server already lists this gateway as linked")
	// ErrClaimBusy means the server asked the gateway to wait.
	ErrClaimBusy = errors.New("web: the server asked the gateway to wait")
	// ErrClaimUnreachable means the WxAlerts API could not be reached or
	// refused the request.
	ErrClaimUnreachable = errors.New("web: the WxAlerts API is not reachable")
)

// ClaimSource drives the device-code claim. The claim package adapts to it.
type ClaimSource interface {
	Claim() ClaimStatus
	ClaimStart(ctx context.Context) (ClaimStatus, error)
	ClaimCancel() ClaimStatus
	ClaimUnlink() error
}

// NopClaim is the default ClaimSource: always unclaimed, and starting a
// claim reports that the WxAlerts API is not available.
type NopClaim struct{}

// Claim implements ClaimSource.
func (NopClaim) Claim() ClaimStatus { return ClaimStatus{State: "unclaimed"} }

// ClaimStart implements ClaimSource.
func (NopClaim) ClaimStart(context.Context) (ClaimStatus, error) {
	return ClaimStatus{}, ErrClaimUnreachable
}

// ClaimCancel implements ClaimSource.
func (NopClaim) ClaimCancel() ClaimStatus { return ClaimStatus{State: "unclaimed"} }

// ClaimUnlink implements ClaimSource.
func (NopClaim) ClaimUnlink() error { return nil }
