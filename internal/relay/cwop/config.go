package cwop

import (
	"net"
	"regexp"
	"strings"

	"github.com/wxalerts/pws-gateway/internal/relay"
)

// Default servers. Non-ham identifiers use the CWOP servers, which need no
// passcode; ham callsigns use the APRS core servers and need a validation code.
const (
	ServerNonHam = "cwop.aprs.net:14580"
	ServerHam    = "rotate.aprs.net:14580"
	// NonHamPasscode is sent for CW/DW/EW/FW/GW identifiers.
	NonHamPasscode = "-1"
)

var (
	reNonHam     = regexp.MustCompile(`^[CDEFG]W[0-9]{4}$`)
	reLooksNonHm = regexp.MustCompile(`^[A-Z]W[0-9]+$`)
	reHam        = regexp.MustCompile(`^[A-Z0-9]{1,2}[0-9][A-Z]{1,4}(-[0-9]{1,2})?$`)
)

// Config is the relay.cwop section of config.json. The passcode is never part
// of it: CredentialFile names the file that holds it.
type Config struct {
	// Owner is "device", "server" or "" (nobody). The Pi relays only for
	// "device".
	Owner string `json:"owner"`
	// Station is the discovery key of the station to relay.
	Station string `json:"station"`
	// ID is the CWOP identifier, a CWxxxx style ID or a ham callsign.
	ID string `json:"id"`
	// CredentialFile holds the passcode of a ham callsign. Empty for CWxxxx.
	CredentialFile string `json:"credential_file"`
	// Server overrides the default host:port.
	Server string `json:"server"`
	// PositionOptIn is the owner's explicit consent to publish Lat and Lon
	// on CWOP, where they are public and exact.
	PositionOptIn bool    `json:"position_opt_in"`
	Lat           float64 `json:"lat"`
	Lon           float64 `json:"lon"`
}

// IDKind classifies an identifier: non-ham, ham, or invalid.
type IDKind int

// Identifier kinds.
const (
	IDInvalid IDKind = iota
	IDNonHam
	IDHam
)

// ClassifyID reports what kind of CWOP identifier id is.
func ClassifyID(id string) IDKind {
	switch {
	case reNonHam.MatchString(id):
		return IDNonHam
	case reLooksNonHm.MatchString(id):
		return IDInvalid
	case reHam.MatchString(id):
		return IDHam
	}
	return IDInvalid
}

// Active reports whether this Pi should relay: owner device and a station.
func (c Config) Active() bool {
	return relay.Owner(c.Owner).PiSends() && c.Station != ""
}

// Problems lists every validation finding. A disabled config (owner not
// "device") is valid whatever else it holds, except for an unknown owner.
func (c Config) Problems() []relay.Problem {
	var out []relay.Problem
	add := func(code, msg string) { out = append(out, relay.Problem{Code: code, Msg: msg}) }
	switch c.Owner {
	case "", string(relay.OwnerDevice), string(relay.OwnerServer):
	default:
		add(relay.CodeBadOwner, "owner must be device, server or empty")
	}
	if c.Owner != string(relay.OwnerDevice) {
		return out
	}
	if c.Station == "" {
		add(relay.CodeNotConfigured, "choose the station to relay")
	}
	kind := ClassifyID(c.ID)
	switch kind {
	case IDInvalid:
		add(relay.CodeBadID, "the CWOP ID must look like CW1234 or be an amateur callsign")
	case IDHam:
		if c.CredentialFile == "" {
			add(relay.CodeBadID, "an amateur callsign needs its validation code in a credential file")
		}
	}
	if !c.PositionOptIn {
		add(relay.CodeNoPosition, "CWOP publishes the station position publicly: opt in to send it")
	} else if c.Lat < -90 || c.Lat > 90 || c.Lon < -180 || c.Lon > 180 || (c.Lat == 0 && c.Lon == 0) {
		add(relay.CodeNoPosition, "latitude and longitude are required and must be in range")
	}
	if c.Server != "" {
		if _, port, err := net.SplitHostPort(c.Server); err != nil || port == "" || strings.ContainsAny(c.Server, " \r\n\x00") {
			add(relay.CodeBadServer, "server must be host:port")
		}
	}
	return out
}

// Servers returns the host:port candidates in order. The default port 14580
// falls back to port 23, as CWOP documents.
func (c Config) Servers() []string {
	s := c.Server
	if s == "" {
		s = ServerNonHam
		if ClassifyID(c.ID) == IDHam {
			s = ServerHam
		}
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil || port != "14580" {
		return []string{s}
	}
	return []string{s, net.JoinHostPort(host, "23")}
}
