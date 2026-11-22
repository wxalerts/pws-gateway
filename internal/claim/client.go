package claim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPI is the production WxAlerts API.
const DefaultAPI = "https://api.wxalerts.org"

const (
	maxResponseBytes = 64 << 10
	requestTimeout   = 20 * time.Second
	// DefaultInterval is the poll interval when the server names none.
	DefaultInterval = 5
	// slowDownStep is the server's own increment, used when a slow_down
	// reply carries no interval.
	slowDownStep = 5
)

// Errors returned by Start. The web layer maps them to HTTP statuses.
var (
	// ErrAlreadyClaimed means the server still has this hardware linked to
	// an account. The account holder removes it on the WxAlerts site.
	ErrAlreadyClaimed = errors.New("claim: this gateway is already linked on the server")
	// ErrRateLimited means the server asked the gateway to wait.
	ErrRateLimited = errors.New("claim: the server asked for a pause")
	// ErrUnavailable covers network failures and 5xx answers.
	ErrUnavailable = errors.New("claim: the WxAlerts server could not be reached")
	// ErrRejected is any other refusal, for example a 422.
	ErrRejected = errors.New("claim: the WxAlerts server rejected the request")
)

// Poll outcomes that end a claim. They are the server's own error codes.
const (
	errPending    = "authorization_pending"
	errSlowDown   = "slow_down"
	errExpired    = "expired_token"
	errAccessDeny = "access_denied"
)

// startRequest is the body of POST /pws/claim/start.
type startRequest struct {
	HWID  string `json:"hw_id"`
	FW    string `json:"fw,omitempty"`
	Model string `json:"model,omitempty"`
}

// startReply is the 200 body of POST /pws/claim/start.
type startReply struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// MQTT is the broker block of the poll success body.
type mqttReply struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	ClientID string `json:"client_id"`
	Password string `json:"password"`
}

// pollReply is the union of the poll bodies: the 200 credentials, or the 400
// error with an optional new interval.
type pollReply struct {
	DeviceID string `json:"device_id"`
	// UserID is the owner's numeric account id. A server that predates
	// contract v2 does not send it; 0 then keeps the device on the v1 topics.
	UserID            int64     `json:"user_id"`
	MQTT              mqttReply `json:"mqtt"`
	CredentialVersion int       `json:"credential_version"`
	Error             string    `json:"error"`
	Interval          int       `json:"interval"`
}

type client struct {
	base string
	http *http.Client
}

func (c *client) post(ctx context.Context, path string, in, out any) (status int, retryAfter time.Duration, err error) {
	body, err := json.Marshal(in)
	if err != nil {
		return 0, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// Do not wrap err: a url.Error quotes the URL, which is harmless,
		// but keeping the message short keeps it readable in the UI.
		return 0, 0, fmt.Errorf("%w: %s", ErrUnavailable, shortErr(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, 0, fmt.Errorf("%w: %s", ErrUnavailable, shortErr(err))
	}
	if n, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && n > 0 {
		retryAfter = time.Duration(n) * time.Second
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		// A body that is not the expected shape leaves out untouched; the
		// status code alone decides what to do.
		if err := json.Unmarshal(raw, out); err != nil && status200(resp.StatusCode) {
			return resp.StatusCode, retryAfter, fmt.Errorf("%w: unreadable reply", ErrUnavailable)
		}
	}
	return resp.StatusCode, retryAfter, nil
}

func status200(code int) bool { return code == http.StatusOK }

func shortErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

// start asks for a claim code.
func (c *client) start(ctx context.Context, req startRequest) (startReply, time.Duration, error) {
	// One value so both the 200 body and the error body parse.
	var both struct {
		startReply
		Error string `json:"error"`
	}
	status, retry, err := c.post(ctx, "/pws/claim/start", req, &both)
	rep := both.startReply
	if err != nil {
		return rep, 0, err
	}
	switch {
	case status == http.StatusOK:
		if rep.DeviceCode == "" || rep.UserCode == "" || rep.ExpiresIn <= 0 {
			return rep, 0, fmt.Errorf("%w: incomplete start reply", ErrRejected)
		}
		if rep.Interval <= 0 {
			rep.Interval = DefaultInterval
		}
		return rep, 0, nil
	case status == http.StatusConflict && both.Error == "already_claimed":
		return rep, 0, ErrAlreadyClaimed
	case status == http.StatusTooManyRequests:
		return rep, retry, ErrRateLimited
	case status >= 500:
		return rep, 0, fmt.Errorf("%w: status %d", ErrUnavailable, status)
	default:
		return rep, 0, fmt.Errorf("%w: status %d %s", ErrRejected, status, strings.TrimSpace(both.Error))
	}
}

// pollKind is how one poll answer is classified.
type pollKind int

const (
	pollOK pollKind = iota
	pollPending
	pollSlowDown
	pollExpired
	pollDenied
	// pollRetry is a transient failure (network, 5xx, 429): keep polling.
	pollRetry
	// pollFatal is a refusal that will not improve (for example a 422).
	pollFatal
)

// poll asks once whether the code was approved.
func (c *client) poll(ctx context.Context, deviceCode string) (pollKind, pollReply, time.Duration) {
	var rep pollReply
	status, retry, err := c.post(ctx, "/pws/claim/poll", map[string]string{"device_code": deviceCode}, &rep)
	switch {
	case err != nil:
		return pollRetry, rep, 0
	case status == http.StatusOK:
		if rep.DeviceID == "" || rep.MQTT.Password == "" || rep.MQTT.URL == "" {
			return pollFatal, rep, 0
		}
		return pollOK, rep, 0
	case status == http.StatusBadRequest:
		switch rep.Error {
		case errPending:
			return pollPending, rep, 0
		case errSlowDown:
			return pollSlowDown, rep, 0
		case errExpired:
			return pollExpired, rep, 0
		case errAccessDeny:
			return pollDenied, rep, 0
		}
		return pollFatal, rep, 0
	case status == http.StatusTooManyRequests, status >= 500:
		return pollRetry, rep, retry
	default:
		return pollFatal, rep, 0
	}
}

// ErrCredentialRejected means the server did not accept the gateway's
// credential (unlinked, rotated or unknown). It says nothing about the owner.
var ErrCredentialRejected = errors.New("claim: the server rejected this gateway's credential")

// identityReply is the 200 body of GET /pws/device/identity.
type identityReply struct {
	DeviceID string `json:"device_id"`
	UserID   int64  `json:"user_id"`
}

// identity asks the server who owns the gateway, authenticating with the
// gateway's own MQTT credential (HTTP Basic over TLS).
func (c *client) identity(ctx context.Context, deviceID, password string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/pws/device/identity", nil)
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(deviceID, password)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrUnavailable, shortErr(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrUnavailable, shortErr(err))
	}
	switch resp.StatusCode {
	case http.StatusOK:
		var rep identityReply
		if err := json.Unmarshal(raw, &rep); err != nil || rep.UserID <= 0 || rep.DeviceID != deviceID {
			return 0, fmt.Errorf("%w: unreadable reply", ErrUnavailable)
		}
		return rep.UserID, nil
	case http.StatusUnauthorized:
		return 0, ErrCredentialRejected
	default:
		return 0, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
}

// Identity returns the numeric account id of the account that owns this
// gateway, from GET /pws/device/identity.
func (m *Manager) Identity(ctx context.Context, deviceID, password string) (int64, error) {
	return m.api.identity(ctx, deviceID, password)
}
