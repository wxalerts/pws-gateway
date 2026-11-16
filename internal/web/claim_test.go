package web

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/claim"
	"github.com/wxalerts/pws-gateway/internal/claim/claimtest"
)

// newClaimEnv wires a real claim manager, talking to the fake WxAlerts API,
// into the web server.
func newClaimEnv(t *testing.T) (*env, func(), *claimtest.Fake) {
	t.Helper()
	e := newEnv(t, nil)
	api := claimtest.New(t)
	api.Interval = 1
	m, err := claim.New(claim.Options{
		DataDir: t.TempDir(), Config: e.cfg, Platform: e.plat, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIBase: api.URL, TimeUnit: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.srv.opts.Claim = NewClaimSource(m)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	stop := func() { cancel(); <-done }
	t.Cleanup(stop)
	return e, stop, api
}

type claimJSON struct {
	State           string  `json:"state"`
	UserCode        *string `json:"user_code"`
	VerificationURI *string `json:"verification_uri"`
	ExpiresAt       *string `json:"expires_at"`
	DeviceID        *string `json:"device_id"`
	Error           *string `json:"error"`
}

func waitClaim(t *testing.T, cookieGet func() claimJSON, want string) claimJSON {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := cookieGet(); s.State == want {
			return s
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("claim never reached %s: %+v", want, cookieGet())
	return claimJSON{}
}

func TestClaimEndpointsRequireSessionAndCSRF(t *testing.T) {
	e, _, _ := newClaimEnv(t)
	for _, c := range [][2]string{{"GET", "/api/claim"}, {"POST", "/api/claim/start"}, {"POST", "/api/claim/cancel"}, {"POST", "/api/claim/unlink"}} {
		if r := e.do(c[0], c[1], "", nil); r.Code != 401 {
			t.Errorf("%s %s without a session = %d", c[0], c[1], r.Code)
		}
	}
	cookie := e.login()
	if r := e.do("POST", "/api/claim/start", "", cookie, CSRFHeader, ""); r.Code != 403 {
		t.Errorf("start without the CSRF header = %d", r.Code)
	}
	if r := e.do("POST", "/api/claim/unlink", "", cookie, CSRFHeader, ""); r.Code != 403 {
		t.Errorf("unlink without the CSRF header = %d", r.Code)
	}
}

func TestClaimPendingSlowDownSuccess(t *testing.T) {
	e, _, api := newClaimEnv(t)
	cookie := e.login()
	var st claimJSON
	e.do("GET", "/api/claim", "", cookie).json(&st)
	if st.State != "unclaimed" || st.UserCode != nil || st.DeviceID != nil {
		t.Fatalf("initial = %+v", st)
	}

	api.Pending()
	api.SlowDown(3)
	api.Approve("d_abcdefghij", "web-test-password-1")
	r := e.do("POST", "/api/claim/start", "", cookie)
	if r.Code != 200 {
		t.Fatalf("start = %d %s", r.Code, r.Body)
	}
	r.json(&st)
	if st.State != "pending" || st.UserCode == nil || *st.UserCode != "K7MP-2XQD" || st.ExpiresAt == nil ||
		st.VerificationURI == nil || *st.VerificationURI != "https://wxalerts.org/pws/claim" {
		t.Fatalf("pending = %+v", st)
	}
	get := func() claimJSON {
		var s claimJSON
		e.do("GET", "/api/claim", "", cookie).json(&s)
		return s
	}
	done := waitClaim(t, get, "claimed")
	if done.DeviceID == nil || *done.DeviceID != "d_abcdefghij" || done.UserCode != nil {
		t.Fatalf("claimed = %+v", done)
	}
	body := e.do("GET", "/api/claim", "", cookie).Body.String()
	if strings.Contains(body, "web-test-password-1") || strings.Contains(body, "device-code") {
		t.Fatalf("claim API leaks a secret: %s", body)
	}
	if got := e.cfg.Get().Uplink; got.DeviceID != "d_abcdefghij" || got.BrokerURL != "wss://pws-mqtt.wxalerts.org/mqtt" {
		t.Fatalf("uplink config = %+v", got)
	}
	// /api/uplink now reports the device id from the config.
	var up struct {
		DeviceID string `json:"device_id"`
	}
	e.do("GET", "/api/uplink", "", cookie).json(&up)
	if up.DeviceID != "d_abcdefghij" {
		t.Fatalf("uplink = %+v", up)
	}
	if r := e.do("POST", "/api/claim/start", "", cookie); r.Code != 409 || !strings.Contains(r.Body.String(), "already_linked") {
		t.Fatalf("start while linked = %d %s", r.Code, r.Body)
	}

	r = e.do("POST", "/api/claim/unlink", "", cookie)
	if r.Code != 200 {
		t.Fatalf("unlink = %d %s", r.Code, r.Body)
	}
	r.json(&st)
	if st.State != "unclaimed" || e.cfg.Get().Uplink.DeviceID != "" {
		t.Fatalf("after unlink: %+v cfg=%+v", st, e.cfg.Get().Uplink)
	}
}

func TestClaimExpiryAndDenial(t *testing.T) {
	e, _, api := newClaimEnv(t)
	cookie := e.login()
	get := func() claimJSON {
		var s claimJSON
		e.do("GET", "/api/claim", "", cookie).json(&s)
		return s
	}

	api.Expired()
	if r := e.do("POST", "/api/claim/start", "", cookie); r.Code != 200 {
		t.Fatalf("start = %d", r.Code)
	}
	if s := waitClaim(t, get, "unclaimed"); s.Error == nil || *s.Error != "expired" {
		t.Fatalf("expired = %+v", s)
	}

	api.Denied()
	if r := e.do("POST", "/api/claim/start", "", cookie); r.Code != 200 {
		t.Fatalf("start = %d", r.Code)
	}
	if s := waitClaim(t, get, "unclaimed"); s.Error == nil || *s.Error != "denied" {
		t.Fatalf("denied = %+v", s)
	}
}

func TestClaimStartOverAndCancel(t *testing.T) {
	e, _, _ := newClaimEnv(t)
	cookie := e.login()
	for range 2 {
		if r := e.do("POST", "/api/claim/start", "", cookie); r.Code != 200 {
			t.Fatalf("start = %d %s", r.Code, r.Body)
		}
	}
	var st claimJSON
	e.do("POST", "/api/claim/cancel", "", cookie).json(&st)
	if st.State != "unclaimed" || st.UserCode != nil {
		t.Fatalf("cancel = %+v", st)
	}
}

func TestClaimStartErrorsMapToStatuses(t *testing.T) {
	e, _, api := newClaimEnv(t)
	cookie := e.login()
	cases := []struct {
		status int
		code   string
		want   int
		body   string
	}{
		{409, "already_claimed", 409, "linked_on_server"},
		{429, "rate_limited", 429, "rate_limited"},
		{503, "unavailable", 502, "unreachable"},
	}
	for _, c := range cases {
		api.StartStatus, api.StartError = c.status, c.code
		r := e.do("POST", "/api/claim/start", "", cookie)
		if r.Code != c.want || !strings.Contains(r.Body.String(), c.body) {
			t.Errorf("server %d: got %d %s", c.status, r.Code, r.Body)
		}
	}
}
