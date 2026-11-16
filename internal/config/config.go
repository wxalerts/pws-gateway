// Package config owns the gateway configuration file <data-dir>/config.json.
//
// The file is versioned (schema_version), written atomically so a power cut
// never truncates it, validated as a whole before every save, and split into
// typed sections so the web API can replace one section at a time. Secrets
// are never stored here: the uplink section only names the file that holds
// the MQTT credential.
//
// The network section carries the state for the commit-confirm flow (a
// candidate that must be confirmed within a deadline or be rolled back). The
// generic state machine lives in commit.go; applying the settings to the host
// is out of scope for this package.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

// SchemaVersion is the version this build writes. Load migrates older files.
const SchemaVersion = 1

// FileName is the configuration file name inside the data directory.
const FileName = "config.json"

// Radio roles. A radio with no entry in the config is treated as
// RoleRTL433 by the collector so a single-dongle kit works out of the box.
const (
	RoleRTL433   = "rtl_433"
	RoleDisabled = "disabled"
)

// MinHistoryCadenceMinutes is the shortest allowed uplink bucket. WxAlerts
// aggregates on 15 minute buckets; 10 is the floor so a station can be sampled
// a little denser without flooding the broker.
const MinHistoryCadenceMinutes = 10

// Update channels.
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// Config is the whole file. Every section has a JSON name that doubles as the
// section key accepted by Store.Apply and PUT /api/config/{section}.
type Config struct {
	SchemaVersion int                `json:"schema_version"`
	Web           Web                `json:"web"`
	Radios        []Radio            `json:"radios"`
	Stations      map[string]Station `json:"stations"`
	Uplink        Uplink             `json:"uplink"`
	History       History            `json:"history"`
	Network       Network            `json:"network"`
	Updates       Updates            `json:"updates"`
	HomeAssistant HomeAssistant      `json:"homeassistant"`
}

// Web configures the local HTTPS server.
type Web struct {
	// Listen is the HTTPS address, for example ":443".
	Listen string `json:"listen"`
	// HTTPRedirect is the plain HTTP address that only redirects, "" to
	// disable.
	HTTPRedirect string `json:"http_redirect"`
	// CertFile and KeyFile are PEM paths. Empty means the self-signed
	// certificate pwsgw manages under <data-dir>/tls/.
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// Radio assigns a role to one SDR dongle, identified by its USB serial.
type Radio struct {
	Serial  string  `json:"serial"`
	Role    string  `json:"role"`
	BandMHz float64 `json:"band_mhz"`
}

// Station is the owner's annotation of a discovered weather station. The key
// in Config.Stations is the discovery key (model/id/channel) produced by the
// collector.
type Station struct {
	// Selected marks a station whose readings are stored and uplinked. Any
	// number of stations may be selected.
	Selected bool `json:"selected"`
	// Name is a free text label shown in the UI.
	Name string `json:"name"`
	// AltM is the sensor altitude above sea level in meters (SI internally).
	AltM float64 `json:"alt_m"`
	// WindHeightM is the anemometer height above ground in meters.
	WindHeightM float64 `json:"wind_height_m"`
	// Fields maps a canonical field name to whether the owner wants it from
	// this station. A missing key means "use the model default", and a nil
	// map (every config written before field selection) means defaults for
	// all fields. See EffectiveFields.
	Fields map[string]bool `json:"fields,omitempty"`
	// IncludeRaw passes the decoder fields that have no canonical name
	// through under "raw" in the uplink. nil means the default: off for a
	// station that is not selected (a neighbor), on once the owner selects
	// it. An explicit false or true always wins. See RawIncluded.
	IncludeRaw *bool `json:"include_raw,omitempty"`
}

// EffectiveFields returns the canonical fields that are stored and uplinked
// for this station, in registry order. This is the one place the rule lives;
// the collector, the uplink and the web API all call it.
//
// defaults is the model default for the fields this station has reported
// (mapping.DefaultFields). The result starts from defaults, then applies the owner's overrides: a field set to
// false is removed, a field set to true is added even if it has not been seen
// yet, so a sensor that reports it later is picked up. Names that are not
// canonical fields are ignored.
func (s Station) EffectiveFields(defaults []string) []string {
	on := map[string]bool{}
	for _, f := range defaults {
		on[f] = true
	}
	for f, v := range s.Fields {
		if v {
			on[f] = true
		} else {
			delete(on, f)
		}
	}
	out := make([]string, 0, len(on))
	for _, f := range model.Registry {
		if on[f.Name] {
			out = append(out, f.Name)
		}
	}
	return out
}

// RawIncluded reports whether unmapped decoder fields are passed through for
// this station: the explicit IncludeRaw if set, otherwise whether the station
// is selected.
func (s Station) RawIncluded() bool {
	if s.IncludeRaw != nil {
		return *s.IncludeRaw
	}
	return s.Selected
}

// Uplink configures the MQTT connection to WxAlerts.
type Uplink struct {
	BrokerURL string `json:"broker_url"`
	DeviceID  string `json:"device_id"`
	// CredentialFile is the path of the file holding the MQTT password or
	// token. The secret itself never lives in config.json.
	CredentialFile string `json:"credential_file"`
}

// History controls local roll-ups and uplink cadence.
type History struct {
	CadenceMinutes int `json:"cadence_minutes"`
}

// NetworkSettings is the placeholder for the host network configuration that
// goes through commit-confirm. Only the shape is defined today.
type NetworkSettings struct {
	// Mode is "dhcp" or "static".
	Mode    string   `json:"mode"`
	Address string   `json:"address,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
}

// Network holds the active settings plus the commit-confirm state.
type Network struct {
	// Current is what is in effect.
	Current NetworkSettings `json:"current"`
	// Pending is a candidate that has been staged (and, once
	// ConfirmDeadline is set, applied) but not yet confirmed.
	Pending *NetworkSettings `json:"pending,omitempty"`
	// ConfirmDeadline is the instant after which an unconfirmed Pending is
	// rolled back. nil means nothing has been applied.
	ConfirmDeadline *time.Time `json:"confirm_deadline,omitempty"`
}

// Updates controls the signed self-update.
type Updates struct {
	// Window is the local time window in which updates may install, as
	// "HH:MM-HH:MM".
	Window  string `json:"window"`
	Channel string `json:"channel"`
}

// HomeAssistant configures the optional local MQTT discovery publisher.
type HomeAssistant struct {
	Enabled   bool   `json:"enabled"`
	BrokerURL string `json:"broker_url"`
}

// Defaults returns the configuration a fresh install starts from.
func Defaults() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Web: Web{
			Listen:       ":443",
			HTTPRedirect: ":80",
		},
		Radios:   []Radio{},
		Stations: map[string]Station{},
		Uplink: Uplink{
			BrokerURL:      "mqtts://mqtt.wxalerts.org:8883",
			CredentialFile: "uplink.credential",
		},
		History: History{CadenceMinutes: 15},
		Network: Network{Current: NetworkSettings{Mode: "dhcp"}},
		Updates: Updates{Window: "02:00-05:00", Channel: ChannelStable},
	}
}

// Sections lists the valid section names in file order.
var Sections = []string{"web", "radios", "stations", "uplink", "history", "network", "updates", "homeassistant"}

// ValidationError collects every problem found so the UI can show them all.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "config: " + strings.Join(e.Problems, "; ")
}

func (e *ValidationError) add(format string, args ...any) {
	e.Problems = append(e.Problems, fmt.Sprintf(format, args...))
}

var (
	reListen = regexp.MustCompile(`^(\[[0-9a-fA-F:.]+\]|[0-9A-Za-z.-]*)?:[0-9]{1,5}$`)
	reWindow = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]-([01][0-9]|2[0-3]):[0-5][0-9]$`)
	reKey    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
)

// Validate checks the whole configuration and returns a *ValidationError
// listing every problem, or nil.
func (c *Config) Validate() error {
	var ve ValidationError

	if c.SchemaVersion != SchemaVersion {
		ve.add("schema_version must be %d, got %d", SchemaVersion, c.SchemaVersion)
	}
	c.validateWeb(&ve)
	c.validateRadios(&ve)
	c.validateStations(&ve)
	c.validateUplink(&ve)
	c.validateNetwork(&ve)
	c.validateUpdates(&ve)

	if len(ve.Problems) > 0 {
		return &ve
	}
	return nil
}

func (c *Config) validateWeb(ve *ValidationError) {
	if !reListen.MatchString(c.Web.Listen) {
		ve.add("web.listen %q is not host:port", c.Web.Listen)
	}
	if c.Web.HTTPRedirect != "" && !reListen.MatchString(c.Web.HTTPRedirect) {
		ve.add("web.http_redirect %q is not host:port", c.Web.HTTPRedirect)
	}
	if (c.Web.CertFile == "") != (c.Web.KeyFile == "") {
		ve.add("web.cert_file and web.key_file must be set together")
	}
}

func (c *Config) validateRadios(ve *ValidationError) {
	seen := map[string]bool{}
	for i, r := range c.Radios {
		if strings.TrimSpace(r.Serial) == "" {
			ve.add("radios[%d].serial is empty", i)
		} else if seen[r.Serial] {
			ve.add("radios[%d].serial %q is listed twice", i, r.Serial)
		}
		seen[r.Serial] = true
		if r.Role != RoleRTL433 && r.Role != RoleDisabled {
			ve.add("radios[%d].role %q must be %s or %s", i, r.Role, RoleRTL433, RoleDisabled)
		}
		if r.Role == RoleRTL433 && (r.BandMHz < 300 || r.BandMHz > 1000) {
			ve.add("radios[%d].band_mhz %v must be between 300 and 1000", i, r.BandMHz)
		}
	}
}

func (c *Config) validateStations(ve *ValidationError) {
	for key, s := range c.Stations {
		if !reKey.MatchString(key) {
			ve.add("stations key %q is not a valid station key", key)
		}
		if len(s.Name) > 64 {
			ve.add("stations[%s].name longer than 64 characters", key)
		}
		if s.AltM < -500 || s.AltM > 9000 {
			ve.add("stations[%s].alt_m %v out of range", key, s.AltM)
		}
		for f := range s.Fields {
			if _, ok := model.Lookup(f); !ok {
				ve.add("stations[%s].fields has unknown field %q", key, f)
			}
		}
		if s.WindHeightM < 0 || s.WindHeightM > 100 {
			ve.add("stations[%s].wind_height_m %v out of range", key, s.WindHeightM)
		}
	}
}

func (c *Config) validateUplink(ve *ValidationError) {
	if c.Uplink.BrokerURL != "" {
		if err := validateBrokerURL(c.Uplink.BrokerURL); err != nil {
			ve.add("uplink.broker_url: %v", err)
		}
	}
	if strings.ContainsAny(c.Uplink.CredentialFile, "\x00") {
		ve.add("uplink.credential_file contains NUL")
	}

	if c.History.CadenceMinutes < MinHistoryCadenceMinutes {
		ve.add("history.cadence_minutes %d is below the minimum %d", c.History.CadenceMinutes, MinHistoryCadenceMinutes)
	}
	if c.History.CadenceMinutes > 24*60 {
		ve.add("history.cadence_minutes %d is more than a day", c.History.CadenceMinutes)
	}
}

func (c *Config) validateNetwork(ve *ValidationError) {
	if err := c.Network.Current.validate(); err != nil {
		ve.add("network.current: %v", err)
	}
	if c.Network.Pending != nil {
		if err := c.Network.Pending.validate(); err != nil {
			ve.add("network.pending: %v", err)
		}
	}
	if c.Network.ConfirmDeadline != nil && c.Network.Pending == nil {
		ve.add("network.confirm_deadline set without network.pending")
	}
}

func (c *Config) validateUpdates(ve *ValidationError) {
	if !reWindow.MatchString(c.Updates.Window) {
		ve.add("updates.window %q must be HH:MM-HH:MM", c.Updates.Window)
	}
	if c.Updates.Channel != ChannelStable && c.Updates.Channel != ChannelBeta {
		ve.add("updates.channel %q must be %s or %s", c.Updates.Channel, ChannelStable, ChannelBeta)
	}

	if c.HomeAssistant.Enabled {
		if err := validateBrokerURL(c.HomeAssistant.BrokerURL); err != nil {
			ve.add("homeassistant.broker_url: %v", err)
		}
	}
}

func (n NetworkSettings) validate() error {
	switch n.Mode {
	case "dhcp":
		return nil
	case "static":
		if n.Address == "" || n.Gateway == "" {
			return errors.New("static mode needs address and gateway")
		}
		return nil
	default:
		return fmt.Errorf("mode %q must be dhcp or static", n.Mode)
	}
}

func validateBrokerURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "mqtt", "mqtts", "tcp", "ssl", "ws", "wss":
	default:
		return fmt.Errorf("scheme %q is not an MQTT scheme", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("missing host")
	}
	if u.User != nil {
		return errors.New("credentials belong in the credential file, not the URL")
	}
	return nil
}
