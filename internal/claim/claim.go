package claim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/atomicfile"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

// File names inside the data directory.
const (
	StateFile = "claim.json"
	// DefaultCredentialFile matches the default of config.Uplink.CredentialFile.
	DefaultCredentialFile = "uplink.credential" //nolint:gosec // a file name, not a credential
)

// State is where the claim stands.
type State string

// The three states persisted in claim.json.
const (
	Unclaimed State = "unclaimed"
	Pending   State = "pending"
	Claimed   State = "claimed"
)

// Reasons a claim ended without credentials, in Status.Error.
const (
	ReasonExpired = "expired"
	ReasonDenied  = "denied"
	ReasonFailed  = "failed"
)

// ErrClaimed is returned by Start while the gateway is already linked.
var ErrClaimed = errors.New("claim: already linked, unlink first")

// Status is what the web page shows.
type Status struct {
	State State `json:"state"`
	// UserCode is shown as the server formats it, for example ABCD-EFGH.
	UserCode                string     `json:"user_code,omitempty"`
	VerificationURI         string     `json:"verification_uri,omitempty"`
	VerificationURIComplete string     `json:"verification_uri_complete,omitempty"`
	ExpiresAt               *time.Time `json:"expires_at,omitempty"`
	DeviceID                string     `json:"device_id,omitempty"`
	// Error is the reason the last claim ended: expired, denied or failed.
	Error string `json:"error,omitempty"`
}

// persisted is claim.json. DeviceCode is the poll secret, so the file is 0600.
type persisted struct {
	State                   State     `json:"state"`
	UserCode                string    `json:"user_code,omitempty"`
	VerificationURI         string    `json:"verification_uri,omitempty"`
	VerificationURIComplete string    `json:"verification_uri_complete,omitempty"`
	DeviceCode              string    `json:"device_code,omitempty"`
	ExpiresAt               time.Time `json:"expires_at,omitzero"`
	Interval                int       `json:"interval,omitempty"`
	DeviceID                string    `json:"device_id,omitempty"`
	Error                   string    `json:"error,omitempty"`
}

// Options configures a Manager.
type Options struct {
	DataDir  string
	Config   *config.Store
	Platform platform.Platform
	Logger   *slog.Logger
	// APIBase is the WxAlerts API root. Default DefaultAPI.
	APIBase string
	// HTTP is the client for the claim calls. Default has a timeout.
	HTTP *http.Client
	// FW is the firmware version sent with start.
	FW string
	// OnClaimed runs after credentials and config are written. It starts the
	// uplink. OnUnlink runs before they are removed and stops it.
	OnClaimed func()
	OnUnlink  func()
	// Now is the clock. TimeUnit is what one server "second" lasts: tests set
	// it to a millisecond so interval and expiry run fast.
	Now      func() time.Time
	TimeUnit time.Duration
}

// Manager owns the claim state machine and the poll loop.
type Manager struct {
	opts Options
	log  *slog.Logger
	api  *client
	now  func() time.Time
	unit time.Duration

	mu     sync.Mutex
	st     persisted
	gen    uint64 // bumped by every Start, Cancel and Unlink; a poller with an older one stops
	cancel context.CancelFunc
	runCtx context.Context
}

// New loads claim.json and reconciles it with the config. It starts nothing.
func New(opts Options) (*Manager, error) {
	if opts.DataDir == "" || opts.Config == nil || opts.Platform == nil {
		return nil, errors.New("claim: DataDir, Config and Platform are required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.APIBase == "" {
		opts.APIBase = DefaultAPI
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: requestTimeout}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.TimeUnit <= 0 {
		opts.TimeUnit = time.Second
	}
	m := &Manager{
		opts: opts, log: opts.Logger, now: opts.Now, unit: opts.TimeUnit,
		api: &client{base: trimSlash(opts.APIBase), http: opts.HTTP},
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func (m *Manager) path() string { return filepath.Join(m.opts.DataDir, StateFile) }

// CredentialPath resolves the credential file named in cfg. A relative name
// lives in dataDir. It returns "" when cfg names none.
func CredentialPath(dataDir string, cfg config.Uplink) string {
	p := cfg.CredentialFile
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dataDir, p)
	}
	return p
}

func (m *Manager) load() error {
	raw, err := os.ReadFile(m.path())
	switch {
	case err == nil:
		if jerr := json.Unmarshal(raw, &m.st); jerr != nil {
			// A damaged state file is not worth refusing to start for: the
			// claim can simply be started again.
			m.log.Warn("ignoring unreadable claim state", "path", m.path())
			m.st = persisted{}
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("claim: read %s: %w", m.path(), err)
	}
	up := m.opts.Config.Get().Uplink
	switch {
	case m.st.State == Pending && !m.st.ExpiresAt.After(m.now()):
		m.st = persisted{State: Unclaimed, Error: ReasonExpired}
	case m.st.State == Claimed && up.DeviceID == "":
		// Unlinked by editing the config: the link is gone.
		m.st = persisted{State: Unclaimed}
	case m.st.State != Claimed && m.st.State != Pending && up.DeviceID != "" && fileExists(CredentialPath(m.opts.DataDir, up)):
		// Provisioned by hand (bench use): show it as linked.
		m.st = persisted{State: Claimed, DeviceID: up.DeviceID}
	case m.st.State == "":
		m.st.State = Unclaimed
	}
	return nil
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// save persists the state. The caller holds m.mu.
func (m *Manager) save() error {
	raw, err := json.MarshalIndent(m.st, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(m.path(), append(raw, '\n'), 0o600)
}

// Status returns the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) statusLocked() Status {
	s := Status{State: m.st.State, Error: m.st.Error}
	switch m.st.State {
	case Pending:
		s.UserCode, s.VerificationURI, s.VerificationURIComplete = m.st.UserCode, m.st.VerificationURI, m.st.VerificationURIComplete
		t := m.st.ExpiresAt
		s.ExpiresAt = &t
	case Claimed:
		s.DeviceID = m.st.DeviceID
	case Unclaimed:
	}
	return s
}

// Run resumes a pending claim and then waits for ctx. Start and Cancel work
// before Run, but polling only happens under a Run context.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.runCtx = ctx
	if m.st.State == Pending {
		m.startPollerLocked()
	}
	m.mu.Unlock()
	<-ctx.Done()
	m.mu.Lock()
	m.stopPollerLocked()
	m.mu.Unlock()
}

// Start asks the server for a new code and begins polling. Starting over
// while a claim is pending supersedes it (the server does the same).
func (m *Manager) Start(ctx context.Context) (Status, error) {
	m.mu.Lock()
	if m.st.State == Claimed {
		m.mu.Unlock()
		return Status{}, ErrClaimed
	}
	m.mu.Unlock()

	board := m.opts.Platform.Board()
	hw, source, err := HardwareID(board, m.opts.DataDir)
	if err != nil {
		return Status{}, err
	}
	rep, _, err := m.api.start(ctx, startRequest{HWID: hw, FW: clip(m.opts.FW, 64), Model: clip(board.Model, 64)})
	if err != nil {
		return Status{}, err
	}
	if !safeLink(rep.VerificationURI) {
		return Status{}, fmt.Errorf("%w: unusable verification address", ErrRejected)
	}
	m.log.Info("claim started", "hw_source", source, "expires_in_s", rep.ExpiresIn)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.State == Claimed {
		return Status{}, ErrClaimed
	}
	m.stopPollerLocked()
	complete := rep.VerificationURIComplete
	if !safeLink(complete) {
		complete = ""
	}
	m.st = persisted{
		State: Pending, UserCode: rep.UserCode, VerificationURI: rep.VerificationURI, VerificationURIComplete: complete,
		DeviceCode: rep.DeviceCode, ExpiresAt: m.now().Add(time.Duration(rep.ExpiresIn) * m.unit), Interval: rep.Interval,
	}
	if err := m.save(); err != nil {
		m.st = persisted{State: Unclaimed}
		return Status{}, err
	}
	m.startPollerLocked()
	return m.statusLocked(), nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// safeLink accepts only web addresses, so a hostile reply cannot plant a
// javascript: link in the page.
func safeLink(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// Cancel abandons a pending claim. The server code simply expires.
func (m *Manager) Cancel() Status {
	m.mu.Lock()
	if m.st.State == Pending {
		m.stopPollerLocked()
		m.st = persisted{State: Unclaimed}
		m.saveLogLocked()
	}
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) saveLogLocked() {
	if err := m.save(); err != nil {
		m.log.Error("save claim state", "err", err)
	}
}

// Unlink stops the uplink, deletes the credential file and clears the uplink
// config. The server keeps the registration until the account holder removes
// the gateway on the WxAlerts site.
func (m *Manager) Unlink() error {
	m.mu.Lock()
	m.stopPollerLocked()
	m.mu.Unlock()

	if m.opts.OnUnlink != nil {
		m.opts.OnUnlink()
	}
	up := m.opts.Config.Get().Uplink
	if p := CredentialPath(m.opts.DataDir, up); p != "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("claim: remove credential file: %w", err)
		}
	}
	if _, err := m.opts.Config.Update(func(c *config.Config) error {
		c.Uplink.DeviceID, c.Uplink.BrokerURL = "", ""
		return nil
	}); err != nil {
		return fmt.Errorf("claim: clear uplink config: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st = persisted{State: Unclaimed}
	return m.save()
}

// stopPollerLocked cancels the running poll loop, if any. A result that
// arrives from it afterwards is dropped by the generation check.
func (m *Manager) stopPollerLocked() {
	m.gen++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

func (m *Manager) startPollerLocked() {
	m.stopPollerLocked()
	parent := m.runCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	go m.pollLoop(ctx, m.gen, m.st.DeviceCode, m.st.Interval, m.st.ExpiresAt)
}

func (m *Manager) pollLoop(ctx context.Context, gen uint64, deviceCode string, interval int, expires time.Time) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	wait := time.Duration(interval) * m.unit
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if !m.now().Before(expires) {
			m.finish(gen, ReasonExpired)
			return
		}
		kind, rep, retry := m.api.poll(ctx, deviceCode)
		if ctx.Err() != nil {
			return
		}
		switch kind {
		case pollOK:
			m.succeed(gen, rep)
			return
		case pollExpired:
			m.finish(gen, ReasonExpired)
			return
		case pollDenied:
			m.finish(gen, ReasonDenied)
			return
		case pollFatal:
			m.log.Warn("claim poll refused by the server")
			m.finish(gen, ReasonFailed)
			return
		case pollSlowDown:
			if rep.Interval > interval {
				interval = rep.Interval
			} else {
				interval += slowDownStep
			}
		case pollRetry:
			m.log.Warn("claim poll failed, will retry")
		case pollPending:
		}
		wait = time.Duration(interval) * m.unit
		if retry > wait {
			wait = retry
		}
		timer.Reset(wait)
	}
}

// finish ends a claim without credentials.
func (m *Manager) finish(gen uint64, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gen != m.gen {
		return
	}
	m.log.Info("claim ended", "reason", reason)
	m.st = persisted{State: Unclaimed, Error: reason}
	m.saveLogLocked()
}

// succeed stores the credentials, points the config at the broker and tells
// the app to start the uplink.
func (m *Manager) succeed(gen uint64, rep pollReply) {
	m.mu.Lock()
	if gen != m.gen {
		m.mu.Unlock()
		return
	}
	err := m.storeCredentials(rep)
	if err != nil {
		m.log.Error("claim approved but the credentials could not be stored", "err", err)
		m.st = persisted{State: Unclaimed, Error: ReasonFailed}
		m.saveLogLocked()
		m.mu.Unlock()
		return
	}
	m.st = persisted{State: Claimed, DeviceID: rep.DeviceID}
	m.saveLogLocked()
	m.stopPollerLocked()
	m.mu.Unlock()
	m.log.Info("gateway linked", "device_id", rep.DeviceID)
	if m.opts.OnClaimed != nil {
		m.opts.OnClaimed()
	}
}

func (m *Manager) storeCredentials(rep pollReply) error {
	cfg := m.opts.Config.Get().Uplink
	if cfg.CredentialFile == "" {
		cfg.CredentialFile = DefaultCredentialFile
	}
	path := CredentialPath(m.opts.DataDir, cfg)
	user := rep.MQTT.Username
	if user == "" {
		user = rep.DeviceID
	}
	body := map[string]any{"username": user, "password": rep.MQTT.Password}
	if rep.UserID > 0 {
		body["user_id"] = rep.UserID
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	_, err = m.opts.Config.Update(func(c *config.Config) error {
		c.Uplink.DeviceID, c.Uplink.BrokerURL, c.Uplink.CredentialFile = rep.DeviceID, rep.MQTT.URL, cfg.CredentialFile
		return nil
	})
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("update uplink config: %w", err)
	}
	return nil
}
