package cwop

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/wxalerts/pws-gateway/internal/relay"
)

// PluginOptions are the collaborators of a Plugin.
type PluginOptions struct {
	Config Config
	// Passcode is the loaded validation code. Empty for non-ham IDs.
	Passcode string
	Sender   Sender
	// Version goes into the login line.
	Version string
	Tracker *relay.Tracker
	Now     func() time.Time
	Logger  *slog.Logger
	// TimeSynced reports whether the clock can be trusted. nil means yes.
	// While it says no, Send refuses and nothing leaves the Pi.
	TimeSynced func() bool
}

// Plugin is the CWOP relay.Plugin.
type Plugin struct {
	cfg     Config
	pass    string
	sender  Sender
	version string
	track   *relay.Tracker
	now     func() time.Time
	red     *Redactor
	log     *slog.Logger
	synced  func() bool
}

var _ relay.Plugin = (*Plugin)(nil)

// New builds a plugin.
func New(o PluginOptions) *Plugin {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Tracker == nil {
		o.Tracker = &relay.Tracker{}
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	red := NewRedactor(o.Passcode)
	return &Plugin{
		cfg: o.Config, pass: o.Passcode, sender: o.Sender, version: o.Version,
		track: o.Tracker, now: o.Now, red: red, synced: o.TimeSynced, log: RedactingLogger(o.Logger, red),
	}
}

// Name implements relay.Plugin.
func (p *Plugin) Name() string { return "cwop" }

// Configure implements relay.Plugin.
func (p *Plugin) Configure(raw json.RawMessage) error {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	p.cfg = c
	return nil
}

// Validate implements relay.Plugin.
func (p *Plugin) Validate() []relay.Problem { return p.cfg.Problems() }

// Redactor returns the plugin's redactor, for the support bundle builder.
func (p *Plugin) Redactor() *Redactor { return p.red }

// Login is the APRS-IS login line, without the terminator. It contains the
// passcode and must never be logged: use RedactedLogin for diagnostics.
func (p *Plugin) Login() string { return p.loginWith(p.passcode()) }

// RedactedLogin is the login line with the passcode masked.
func (p *Plugin) RedactedLogin() string { return p.loginWith(Redacted) }

func (p *Plugin) loginWith(pass string) string {
	v := p.version
	if v == "" {
		v = "dev"
	}
	return fmt.Sprintf("user %s pass %s vers pws-gateway %s", p.cfg.ID, pass, v)
}

func (p *Plugin) passcode() string {
	if ClassifyID(p.cfg.ID) == IDNonHam {
		return NonHamPasscode
	}
	return p.pass
}

// Map implements relay.Plugin.
func (p *Plugin) Map(obs relay.Observation) (string, error) {
	if !p.cfg.PositionOptIn {
		return "", relay.Errorf(relay.CodeNoPosition, "the owner has not opted in to publishing a position")
	}
	if ClassifyID(p.cfg.ID) == IDInvalid {
		return "", relay.Errorf(relay.CodeBadID, "invalid CWOP ID")
	}
	return FormatPacket(p.cfg.ID, p.cfg.Lat, p.cfg.Lon, obs)
}

// Send implements relay.Plugin. It records the outcome in the tracker with
// every error text redacted.
func (p *Plugin) Send(ctx context.Context, msg string) error {
	if !p.cfg.Active() {
		return relay.Errorf(relay.CodeNotConfigured, "this station is not relayed by the device")
	}
	at := p.now()
	if p.synced != nil && !p.synced() {
		p.track.Fail(at, relay.CodeTimeUnsynced, "the clock is not synced, nothing is sent", false)
		return relay.Errorf(relay.CodeTimeUnsynced, "the clock is not synced")
	}
	err := p.sender.Send(ctx, p.cfg.Servers(), p.Login(), msg)
	if err != nil {
		code, text := relay.CodeOf(err), p.red.Redact(err.Error())
		p.track.Fail(at, code, text, true)
		p.log.Warn("cwop delivery failed", "code", code, "err", text)
		return relay.Errorf(code, text)
	}
	p.track.Success(at)
	p.log.Info("cwop packet delivered", "id", p.cfg.ID)
	return nil
}

// Health implements relay.Plugin.
func (p *Plugin) Health() relay.Health { return p.track.Health() }

// SupportInfo is what a support bundle may contain about the relay: settings
// without the passcode and the delivery state, with every text redacted.
func (p *Plugin) SupportInfo() map[string]any {
	h := p.Health()
	if h.LastError != nil {
		e := *h.LastError
		e.Msg = p.red.Redact(e.Msg)
		h.LastError = &e
	}
	return map[string]any{
		"owner": p.cfg.Owner, "station": p.cfg.Station, "id": p.cfg.ID,
		"server": p.cfg.Server, "position_opt_in": p.cfg.PositionOptIn,
		"passcode_set": p.pass != "", "health": h,
	}
}
