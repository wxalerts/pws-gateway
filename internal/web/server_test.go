package web

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wxalerts/pws-gateway/internal/auth"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/logging"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
)

const goodPW = "a long enough password"

type fakeStations struct{ list []StationInfo }

func (f fakeStations) Stations(context.Context) ([]StationInfo, error) { return f.list, nil }

type env struct {
	t     *testing.T
	srv   *Server
	h     http.Handler
	plat  *mock.Platform
	cfg   *config.Store
	clock *time.Time
	log   *bytes.Buffer
}

func newEnv(t *testing.T, dist fstest.MapFS, stations ...StationInfo) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := config.NewStore(dir)
	if _, err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	as, err := auth.NewStore(dir, auth.WithParams(auth.TestParams))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ring := logging.NewRing(50)
	plat := mock.New()
	e := &env{t: t, plat: plat, cfg: cfg, clock: &now, log: logBuf}
	opts := Options{
		Platform: plat, Config: cfg, Auth: as,
		Sessions: auth.NewSessions(dir, auth.SessionsOptions{}),
		Limiter:  auth.NewLimiter(auth.LimiterOptions{Threshold: 3, Now: func() time.Time { return *e.clock }}),
		Logs:     ring, Logger: logger, Stations: fakeStations{stations},
		Version: "1.2.3", Commit: "abc", BuildDate: "2026-01-01T00:00:00Z",
		Now: func() time.Time { return *e.clock },
	}
	if dist != nil {
		opts.Dist = dist
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ring.Write([]byte("line one\nline two\n")) //nolint:errcheck // test fixture
	e.srv, e.h = srv, srv.Handler()
	return e
}

type resp struct {
	*httptest.ResponseRecorder
}

func (r resp) json(v any) {
	if err := json.Unmarshal(r.Body.Bytes(), v); err != nil {
		panic(err.Error() + ": " + r.Body.String())
	}
}

func (e *env) do(method, path, body string, cookie *http.Cookie, hdr ...string) resp {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(CSRFHeader, CSRFValue)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return resp{rec}
}

func sessionCookie(t *testing.T, r resp) *http.Cookie {
	t.Helper()
	for _, c := range r.Result().Cookies() {
		if c.Name == SessionCookie && c.Value != "" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func (e *env) login() *http.Cookie {
	e.t.Helper()
	r := e.do("POST", "/api/setup", `{"username":"admin","password":"`+goodPW+`"}`, nil)
	if r.Code != 200 {
		e.t.Fatalf("setup: %d %s", r.Code, r.Body)
	}
	return sessionCookie(e.t, r)
}

func TestSetupLoginAuthenticatedCall(t *testing.T) {
	e := newEnv(t, nil)
	var st struct {
		NeedsSetup bool `json:"needs_setup"`
	}
	e.do("GET", "/api/setup", "", nil).json(&st)
	if !st.NeedsSetup {
		t.Fatal("expected needs_setup")
	}
	if r := e.do("POST", "/api/setup", `{"username":"admin","password":"short"}`, nil); r.Code != 400 || !strings.Contains(r.Body.String(), "weak_password") {
		t.Fatalf("weak setup: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/api/session", `{"username":"admin","password":"`+goodPW+`"}`, nil); r.Code != 409 {
		t.Fatalf("login before setup: %d", r.Code)
	}

	c := e.login()
	if r := e.do("POST", "/api/setup", `{"username":"x","password":"`+goodPW+`"}`, nil); r.Code != 409 {
		t.Fatalf("second setup: %d", r.Code)
	}
	var sess struct {
		Authenticated bool   `json:"authenticated"`
		Username      string `json:"username"`
	}
	e.do("GET", "/api/session", "", c).json(&sess)
	if !sess.Authenticated || sess.Username != "admin" {
		t.Fatalf("session = %+v", sess)
	}
	e.do("GET", "/api/session", "", nil).json(&sess)
	if sess.Authenticated {
		t.Fatal("anonymous session reported authenticated")
	}

	// Logout invalidates, then log back in.
	if r := e.do("DELETE", "/api/session", "", c); r.Code != 200 {
		t.Fatal(r.Code)
	}
	if r := e.do("GET", "/api/version", "", c); r.Code != 401 {
		t.Fatalf("after logout: %d", r.Code)
	}
	r := e.do("POST", "/api/session", `{"username":"admin","password":"`+goodPW+`"}`, nil)
	if r.Code != 200 {
		t.Fatalf("login: %d %s", r.Code, r.Body)
	}
	c = sessionCookie(t, r)
	var v map[string]string
	if r := e.do("GET", "/api/version", "", c); r.Code != 200 {
		t.Fatal(r.Code)
	} else {
		r.json(&v)
	}
	if v["version"] != "1.2.3" || v["commit"] != "abc" {
		t.Fatalf("version = %v", v)
	}
	if !strings.Contains(e.log.String(), "event=login_success") || !strings.Contains(e.log.String(), "event=setup_created") {
		t.Fatalf("audit lines missing:\n%s", e.log)
	}
}

func TestLoginFailureAndLockout(t *testing.T) {
	e := newEnv(t, nil)
	e.login()
	bad := `{"username":"admin","password":"wrong wrong wrong"}`
	for i := 0; i < 3; i++ {
		r := e.do("POST", "/api/session", bad, nil)
		if r.Code != 401 || !strings.Contains(r.Body.String(), "invalid_credentials") {
			t.Fatalf("attempt %d: %d %s", i, r.Code, r.Body)
		}
	}
	r := e.do("POST", "/api/session", `{"username":"admin","password":"`+goodPW+`"}`, nil)
	if r.Code != 429 || r.Header().Get("Retry-After") == "" {
		t.Fatalf("locked login: %d %v", r.Code, r.Header())
	}
	*e.clock = e.clock.Add(time.Minute)
	if r := e.do("POST", "/api/session", `{"username":"admin","password":"`+goodPW+`"}`, nil); r.Code != 200 {
		t.Fatalf("after lockout: %d", r.Code)
	}
}

func TestPasswordChange(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login()
	if r := e.do("POST", "/api/session/password", `{"current_password":"nope nope nope","new_password":"another good password"}`, c); r.Code != 403 {
		t.Fatalf("wrong current: %d", r.Code)
	}
	if r := e.do("POST", "/api/session/password", `{"current_password":"`+goodPW+`","new_password":"short"}`, c); r.Code != 400 {
		t.Fatalf("weak new: %d", r.Code)
	}
	r := e.do("POST", "/api/session/password", `{"current_password":"`+goodPW+`","new_password":"another good password"}`, c)
	if r.Code != 200 {
		t.Fatalf("change: %d %s", r.Code, r.Body)
	}
	if r := e.do("GET", "/api/version", "", c); r.Code != 401 {
		t.Fatalf("old session survived: %d", r.Code)
	}
	if r := e.do("GET", "/api/version", "", sessionCookie(t, r)); r.Code != 200 {
		t.Fatalf("new session: %d", r.Code)
	}
	if r := e.do("POST", "/api/session", `{"username":"admin","password":"another good password"}`, nil); r.Code != 200 {
		t.Fatalf("login with new password: %d", r.Code)
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t, nil)
	body := `{"username":"admin","password":"` + goodPW + `"}`
	cases := []struct {
		name string
		hdr  []string
		want int
	}{
		{"missing header", []string{CSRFHeader, ""}, 403},
		{"wrong header value", []string{CSRFHeader, "x"}, 403},
		{"cross-site", []string{"Sec-Fetch-Site", "cross-site"}, 403},
		{"same-site is not same-origin", []string{"Sec-Fetch-Site", "same-site"}, 403},
		{"no fetch metadata or origin", []string{"Sec-Fetch-Site", ""}, 403},
		{"foreign origin", []string{"Sec-Fetch-Site", "", "Origin", "https://evil.example"}, 403},
		{"matching origin", []string{"Sec-Fetch-Site", "", "Origin", "https://example.com"}, 200},
		{"user-initiated none", []string{"Sec-Fetch-Site", "none"}, 200},
	}
	for _, c := range cases {
		e2 := newEnv(t, nil)
		r := e2.do("POST", "/api/setup", body, nil, c.hdr...)
		if r.Code != c.want {
			t.Errorf("%s: %d %s", c.name, r.Code, r.Body)
		}
		if c.want == 403 {
			var b errorBody
			r.json(&b)
			if b.Error != "csrf" {
				t.Errorf("%s: body %+v", c.name, b)
			}
		}
	}
	// Safe methods are exempt.
	if r := e.do("GET", "/api/setup", "", nil, CSRFHeader, "", "Sec-Fetch-Site", "cross-site"); r.Code != 200 {
		t.Fatalf("GET exempt: %d", r.Code)
	}
}

func TestUnauthenticatedShape(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"/api/health", "/api/stations", "/api/radios", "/api/config", "/api/config/pending", "/api/uplink", "/api/update", "/api/logs", "/api/readings?station=x", "/api/version"} {
		r := e.do("GET", p, "", nil)
		var b errorBody
		r.json(&b)
		if r.Code != 401 || b.Error != "unauthenticated" || !strings.HasPrefix(r.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s: %d %s", p, r.Code, r.Body)
		}
	}
	if r := e.do("PUT", "/api/stations/x", `{}`, nil); r.Code != 401 {
		t.Errorf("PUT station: %d", r.Code)
	}
	c := e.login()
	r := e.do("GET", "/api/nope", "", c)
	var b errorBody
	r.json(&b)
	if r.Code != 404 || b.Error != "not_found" {
		t.Fatalf("unknown api: %d %s", r.Code, r.Body)
	}
}

func TestCookieFlagsAndHeaders(t *testing.T) {
	e := newEnv(t, nil)
	r := e.do("POST", "/api/setup", `{"username":"admin","password":"`+goodPW+`"}`, nil)
	sc := r.Header().Get("Set-Cookie")
	for _, want := range []string{SessionCookie + "=", "Secure", "HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(sc, want) {
			t.Errorf("Set-Cookie %q lacks %q", sc, want)
		}
	}
	h := r.Header()
	if h.Get("Content-Security-Policy") != ContentSecurityPolicy || !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", h.Get("Content-Security-Policy"))
	}
	if h.Get("Strict-Transport-Security") == "" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("security headers = %v", h)
	}
	if !strings.Contains(e.log.String(), "http request") {
		t.Error("no access log at debug level")
	}
}

func TestBodyLimit(t *testing.T) {
	e := newEnv(t, nil)
	big := `{"username":"` + strings.Repeat("a", MaxBodyBytes+10) + `"}`
	if r := e.do("POST", "/api/setup", big, nil); r.Code != 413 {
		t.Fatalf("big body: %d", r.Code)
	}
	if r := e.do("POST", "/api/setup", `{`, nil); r.Code != 400 {
		t.Fatalf("bad json: %d", r.Code)
	}
}

func TestHealth(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login()
	var h map[string]any
	r := e.do("GET", "/api/health", "", c)
	r.json(&h)
	if r.Code != 200 || h["fw"] != "1.2.3" || h["cpu_temp_c"].(float64) != 48.2 || h["usb_resets"].(float64) != 0 {
		t.Fatalf("health: %d %v", r.Code, h)
	}
	board := h["board"].(map[string]any)
	if board["tier"] != "pi4" || board["soc"] != "bcm2711" {
		t.Fatalf("board = %v", board)
	}
	for _, k := range []string{"throttled", "time", "outbox", "mem_free_mb", "uptime_s", "rootfs_used_pct", "tmpfs_used_pct"} {
		if _, ok := h[k]; !ok {
			t.Errorf("health lacks %s", k)
		}
	}
	e.plat.Set(func(p *mock.Platform) {
		p.HealthValue.Throttled = platform.DecodeThrottled(platform.BitUnderVoltageNow | platform.BitUnderVoltageOccurred)
		p.HealthValue.CPUTempKnown = false
	})
	e.do("GET", "/api/health", "", c).json(&h)
	if h["cpu_temp_c"] != nil || h["throttled"].(map[string]any)["under_voltage_now"] != true {
		t.Fatalf("health 2: %v", h)
	}
}

func TestStationsMergeAndPut(t *testing.T) {
	ch := 1
	e := newEnv(t, nil,
		StationInfo{Key: "Acurite-5n1/1/12", Model: "Acurite-5n1", ID: 12, Channel: &ch, LastSeen: time.Unix(0, 0), Fields: []string{"temp_c"}, NSightings: 7},
		StationInfo{Key: "Fine/2", Model: "Fine", ID: 2},
	)
	c := e.login()
	var list []map[string]any
	e.do("GET", "/api/stations", "", c).json(&list)
	if len(list) != 2 || list[0]["selected"] != false || list[0]["alt_m"] != nil || list[0]["n_sightings"].(float64) != 7 {
		t.Fatalf("list = %v", list)
	}

	r := e.do("PUT", "/api/stations/Acurite-5n1/1/12", `{"selected":true,"name":"Roof","alt_m":12.5,"wind_height_m":null}`, c)
	if r.Code != 200 {
		t.Fatalf("put: %d %s", r.Code, r.Body)
	}
	got := e.cfg.Get().Stations["Acurite-5n1/1/12"]
	if !got.Selected || got.Name != "Roof" || got.AltM != 12.5 {
		t.Fatalf("config = %+v", got)
	}
	// Selecting another station keeps the first one selected.
	if r := e.do("PUT", "/api/stations/Fine/2", `{"selected":true}`, c); r.Code != 200 {
		t.Fatalf("put 2: %d %s", r.Code, r.Body)
	}
	st := e.cfg.Get().Stations
	if !st["Acurite-5n1/1/12"].Selected || !st["Fine/2"].Selected || st["Acurite-5n1/1/12"].Name != "Roof" {
		t.Fatalf("stations = %+v", st)
	}
	e.do("GET", "/api/stations", "", c).json(&list)
	if list[0]["name"] != "Roof" || list[0]["alt_m"].(float64) != 12.5 {
		t.Fatalf("merged = %v", list[0])
	}
	if r := e.do("PUT", "/api/stations/ghost", `{"selected":true}`, c); r.Code != 404 {
		t.Fatalf("unknown station: %d", r.Code)
	}
	if r := e.do("PUT", "/api/stations/Fine/2", `{"wind_height_m":500}`, c); r.Code != 422 {
		t.Fatalf("invalid height: %d %s", r.Code, r.Body)
	}
}

func TestStationFieldSelectionAPI(t *testing.T) {
	e := newEnv(t, nil,
		StationInfo{Key: "Acurite-609TXC:224:", Model: "Acurite-609TXC", ID: 224, LastSeen: time.Unix(0, 0),
			Fields: []string{"temp_c", "humidity_pct", "battery_ok"}, RawFields: []string{"status", "mic"}},
	)
	c := e.login()
	const path = "/api/stations/Acurite-609TXC:224:"
	var list []map[string]any
	e.do("GET", "/api/stations", "", c).json(&list)
	row := list[0]
	strs := func(v any) []string {
		var out []string
		for _, x := range v.([]any) {
			out = append(out, x.(string))
		}
		return out
	}
	if got := strs(row["fields"]); !slices.Equal(got, []string{"temp_c", "humidity_pct"}) {
		t.Fatalf("fields = %v", got)
	}
	if got := strs(row["fields_available"]); !slices.Equal(got, []string{"temp_c", "humidity_pct", "battery_ok"}) {
		t.Fatalf("available = %v", got)
	}
	if !slices.Equal(strs(row["fields_default"]), strs(row["fields"])) || !slices.Equal(strs(row["raw_fields"]), []string{"status", "mic"}) || row["include_raw"] != false {
		t.Fatalf("row = %v", row)
	}

	// Selecting ticks the defaults and turns raw on without any overrides.
	if r := e.do("PUT", path, `{"selected":true}`, c); r.Code != 200 {
		t.Fatalf("select: %d %s", r.Code, r.Body)
	}
	st := e.cfg.Get().Stations["Acurite-609TXC:224:"]
	if st.Fields != nil || !st.RawIncluded() {
		t.Fatalf("after select: %+v", st)
	}

	// Partial update: drop humidity, add battery.
	var got map[string]any
	r := e.do("PUT", path, `{"fields":{"humidity_pct":false,"battery_ok":true},"include_raw":false}`, c)
	if r.Code != 200 {
		t.Fatalf("fields put: %d %s", r.Code, r.Body)
	}
	r.json(&got)
	if f := strs(got["fields"]); !slices.Equal(f, []string{"temp_c", "battery_ok"}) || got["include_raw"] != false {
		t.Fatalf("put response = %v", got)
	}
	// A second partial update keeps the first one's overrides.
	e.do("PUT", path, `{"fields":{"temp_c":false}}`, c)
	st = e.cfg.Get().Stations["Acurite-609TXC:224:"]
	if st.Fields["humidity_pct"] || !st.Fields["battery_ok"] || st.Fields["temp_c"] {
		t.Fatalf("merge lost overrides: %+v", st.Fields)
	}
	// null resets to the model default and the default raw rule.
	e.do("PUT", path, `{"fields":null,"include_raw":null}`, c)
	st = e.cfg.Get().Stations["Acurite-609TXC:224:"]
	if st.Fields != nil || st.IncludeRaw != nil {
		t.Fatalf("reset: %+v", st)
	}
	if r := e.do("PUT", path, `{"fields":{"nonsense":true}}`, c); r.Code != 422 {
		t.Fatalf("unknown field: %d %s", r.Code, r.Body)
	}
	if r := e.do("PUT", path, `{"fields":["temp_c"]}`, c); r.Code != 400 {
		t.Fatalf("bad shape: %d", r.Code)
	}
}

func TestRadiosMerge(t *testing.T) {
	e := newEnv(t, nil)
	e.plat.Set(func(p *mock.Platform) {
		p.USB = []platform.USBDevice{
			{Bus: 1, Dev: 4, VendorID: 0x0bda, ProductID: 0x2838, Serial: "00000001", Product: "RTL2838UHIDIR"},
			{Bus: 1, Dev: 5, VendorID: 0x0bda, ProductID: 0x2838, Serial: "00000002"},
			{Bus: 1, Dev: 2, VendorID: 0x1d6b, ProductID: 0x0002, Serial: "hub"},
		}
	})
	c := e.login()
	if _, err := e.cfg.Apply("radios", json.RawMessage(`[{"serial":"00000002","role":"disabled","band_mhz":915},{"serial":"gone","role":"rtl_433","band_mhz":433.92}]`)); err != nil {
		t.Fatal(err)
	}
	var list []radioView
	e.do("GET", "/api/radios", "", c).json(&list)
	if len(list) != 3 {
		t.Fatalf("radios = %+v", list)
	}
	if list[0].Serial != "00000001" || list[0].Role != "rtl_433" || list[0].BandMHz != 433.92 || list[0].Product != "RTL2838UHIDIR" || list[0].Bus != "001:004" || list[0].Status != "unknown" {
		t.Fatalf("radio 0 = %+v", list[0])
	}
	if list[1].Serial != "00000002" || list[1].Role != "disabled" || list[1].BandMHz != 915 || list[1].Status != "idle" || list[1].Product == "" {
		t.Fatalf("radio 1 = %+v", list[1])
	}
	if list[2].Serial != "gone" || list[2].Status != "missing" {
		t.Fatalf("radio 2 = %+v", list[2])
	}

	r := e.do("PUT", "/api/radios/00000001", `{"role":"rtl_433","band_mhz":915}`, c)
	var one radioView
	r.json(&one)
	if r.Code != 200 || one.BandMHz != 915 {
		t.Fatalf("put: %d %s", r.Code, r.Body)
	}
	if got := e.cfg.Get().Radios; len(got) != 3 {
		t.Fatalf("config radios = %+v", got)
	}
	for body, want := range map[string]int{
		`{"role":"scanner","band_mhz":915}`: 400,
		`{"role":"rtl_433","band_mhz":868}`: 400,
	} {
		if r := e.do("PUT", "/api/radios/00000001", body, c); r.Code != want {
			t.Errorf("%s: %d", body, r.Code)
		}
	}
	if r := e.do("PUT", "/api/radios/missing", `{"role":"rtl_433","band_mhz":915}`, c); r.Code != 404 {
		t.Fatalf("unknown radio: %d", r.Code)
	}
}

func TestConfigAndCommitConfirm(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login()
	var cfg map[string]any
	e.do("GET", "/api/config", "", c).json(&cfg)
	if _, ok := cfg["uplink"]; !ok || cfg["pending_confirm"] != nil {
		t.Fatalf("config = %v", cfg)
	}
	if r := e.do("PUT", "/api/config/history", `{"cadence_minutes":30}`, c); r.Code != 200 || e.cfg.Get().History.CadenceMinutes != 30 {
		t.Fatalf("put history: %d %s", r.Code, r.Body)
	}
	if r := e.do("PUT", "/api/config/history", `{"cadence_minutes":1}`, c); r.Code != 422 {
		t.Fatalf("invalid: %d", r.Code)
	}
	if r := e.do("PUT", "/api/config/history", `{"bogus":1}`, c); r.Code != 400 {
		t.Fatalf("unknown field: %d", r.Code)
	}
	if r := e.do("PUT", "/api/config/nosuch", `{}`, c); r.Code != 404 {
		t.Fatalf("unknown section: %d", r.Code)
	}
	if r := e.do("POST", "/api/config/apply", "", c); r.Code != 409 {
		t.Fatalf("apply with nothing staged: %d", r.Code)
	}
	if r := e.do("PUT", "/api/config/network", `{"pending":{"mode":"static","address":"192.168.1.5/24","gateway":"192.168.1.1"}}`, c); r.Code != 200 {
		t.Fatalf("stage: %d %s", r.Code, r.Body)
	}
	r := e.do("POST", "/api/config/apply", "", c)
	var raw map[string]any
	r.json(&raw)
	if r.Code != 200 || raw["applied"] != true || raw["pending_confirm"] != true || raw["confirm_deadline"] == "" {
		t.Fatalf("apply: %d %s", r.Code, r.Body)
	}
	var pend map[string]any
	e.do("GET", "/api/config/pending", "", c).json(&pend)
	if pend["pending_confirm"] != true {
		t.Fatalf("pending = %v", pend)
	}
	if r := e.do("POST", "/api/config/apply", "", c); r.Code != 409 {
		t.Fatalf("double apply: %d", r.Code)
	}
	if r := e.do("POST", "/api/config/confirm", "", c); r.Code != 200 {
		t.Fatalf("confirm: %d %s", r.Code, r.Body)
	}
	if got := e.cfg.Get().Network; got.Current.Mode != "static" || got.Pending != nil || got.ConfirmDeadline != nil {
		t.Fatalf("network = %+v", got)
	}
	if r := e.do("POST", "/api/config/confirm", "", c); r.Code != 409 {
		t.Fatalf("confirm twice: %d", r.Code)
	}

	// An unconfirmed change rolls back after the deadline.
	e.do("PUT", "/api/config/network", `{"pending":{"mode":"dhcp"}}`, c)
	e.do("POST", "/api/config/apply", "", c)
	*e.clock = e.clock.Add(config.DefaultConfirmWindow + time.Second)
	e.do("GET", "/api/config/pending", "", c).json(&pend)
	if pend["pending_confirm"] != false {
		t.Fatalf("pending after deadline = %v", pend)
	}
	if got := e.cfg.Get().Network; got.Current.Mode != "static" || got.Pending != nil {
		t.Fatalf("rollback failed: %+v", got)
	}
}

func TestSimpleEndpoints(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login()
	var up map[string]any
	e.do("GET", "/api/uplink", "", c).json(&up)
	if up["connected"] != false || up["broker"] == "" || up["last_publish"] != nil {
		t.Fatalf("uplink = %v", up)
	}
	var us map[string]any
	e.do("GET", "/api/update", "", c).json(&us)
	if us["state"] != "idle" || us["current"] != "1.2.3" {
		t.Fatalf("update = %v", us)
	}
	for _, p := range []string{"/api/update/check", "/api/update/apply"} {
		r := e.do("POST", p, "", c)
		if r.Code != 501 || !strings.Contains(r.Body.String(), "not_implemented") {
			t.Errorf("%s: %d %s", p, r.Code, r.Body)
		}
	}
	r := e.do("GET", "/api/logs?lines=1", "", c)
	if r.Code != 200 || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/plain") || r.Body.String() != "line two\n" {
		t.Fatalf("logs: %d %q", r.Code, r.Body)
	}
	if r := e.do("GET", "/api/logs?lines=x", "", c); r.Code != 400 {
		t.Fatalf("bad lines: %d", r.Code)
	}
	if r := e.do("GET", "/api/readings?station=a", "", c); r.Body.String() != "[]\n" {
		t.Fatalf("readings: %q", r.Body)
	}
	if r := e.do("GET", "/api/readings?station=a&since=yesterday", "", c); r.Code != 400 {
		t.Fatalf("bad since: %d", r.Code)
	}
	if r := e.do("GET", "/api/readings", "", c); r.Code != 400 {
		t.Fatalf("no station: %d", r.Code)
	}
}

func TestSPA(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":        {Data: []byte("<html>app</html>")},
		"assets/app-abc.js": {Data: []byte("console.log(1)")},
		"favicon.svg":       {Data: []byte("<svg/>")},
	}
	e := newEnv(t, dist)
	for _, p := range []string{"/", "/index.html", "/stations", "/stations/Acurite-5n1.12", "/some/deep/route"} {
		r := e.do("GET", p, "", nil)
		if r.Code != 200 || !strings.Contains(r.Body.String(), "app") || r.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d cc=%q body=%q", p, r.Code, r.Header().Get("Cache-Control"), r.Body)
		}
	}
	r := e.do("GET", "/assets/app-abc.js", "", nil)
	if r.Code != 200 || r.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("asset: %d %q", r.Code, r.Header().Get("Cache-Control"))
	}
	if r := e.do("GET", "/assets/missing.js", "", nil); r.Code != 404 {
		t.Errorf("missing asset: %d", r.Code)
	}
	if r := e.do("GET", "/favicon.svg", "", nil); r.Code != 200 || r.Header().Get("Cache-Control") == "public, max-age=31536000, immutable" {
		t.Errorf("favicon: %d %q", r.Code, r.Header().Get("Cache-Control"))
	}
	// The SPA fallback never shadows the API.
	if r := e.do("GET", "/api/zzz", "", nil); r.Code != 404 || !strings.Contains(r.Body.String(), "not_found") {
		t.Errorf("api 404: %d %s", r.Code, r.Body)
	}
}

func TestPlaceholderWhenNoIndex(t *testing.T) {
	for name, dist := range map[string]fstest.MapFS{"nil": nil, "empty": {".keep": {}}} {
		e := newEnv(t, dist)
		r := e.do("GET", "/anything", "", nil)
		if r.Code != 200 || !strings.Contains(r.Body.String(), "not built") || !strings.Contains(r.Body.String(), "CONTRIBUTING.md") {
			t.Errorf("%s: %d %s", name, r.Code, r.Body)
		}
		if !strings.HasPrefix(r.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: content type %q", name, r.Header().Get("Content-Type"))
		}
	}
}

func TestRedirectHandler(t *testing.T) {
	cases := []struct{ host, port, want string }{
		{"pwsgw.local", "443", "https://pwsgw.local/a?b=1"},
		{"pwsgw.local:80", "8443", "https://pwsgw.local:8443/a?b=1"},
		{"192.168.1.5:8080", "8443", "https://192.168.1.5:8443/a?b=1"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "http://"+c.host+"/a?b=1", nil)
		rec := httptest.NewRecorder()
		RedirectHandler(c.port).ServeHTTP(rec, req)
		if rec.Code != 301 || rec.Header().Get("Location") != c.want {
			t.Errorf("%s: %d %q", c.host, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestEnsureCert(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	names := CertNames{DNS: []string{MDNSName, "host1"}, IPs: []net.IP{net.ParseIP("192.168.1.5")}}
	c, regen, err := EnsureCert(dir, names, now)
	if err != nil || !regen {
		t.Fatalf("first: regen=%v err=%v", regen, err)
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if leaf.NotAfter.Sub(leaf.NotBefore) < 9*365*24*time.Hour || len(leaf.DNSNames) != 2 || len(leaf.IPAddresses) != 1 {
		t.Fatalf("leaf = %v %v %v", leaf.NotAfter, leaf.DNSNames, leaf.IPAddresses)
	}
	if leaf.PublicKeyAlgorithm != x509.ECDSA {
		t.Fatal("not ECDSA")
	}
	if _, regen, _ := EnsureCert(dir, names, now); regen {
		t.Fatal("unchanged SAN set regenerated")
	}
	// Order does not matter.
	rev := CertNames{DNS: []string{"host1", MDNSName}, IPs: names.IPs}
	if _, regen, _ := EnsureCert(dir, rev, now); regen {
		t.Fatal("reordered SANs regenerated")
	}
	names.IPs = append(names.IPs, net.ParseIP("10.0.0.9"))
	if _, regen, _ := EnsureCert(dir, names, now); !regen {
		t.Fatal("new IP did not regenerate")
	}
	if _, regen, _ := EnsureCert(dir, names, now.Add(11*365*24*time.Hour)); !regen {
		t.Fatal("expired cert not regenerated")
	}
}

func TestServeRedirectAndShutdown(t *testing.T) {
	e := newEnv(t, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.srv.Serve(ctx, ln, rl, nil) }()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+rl.Addr().String()+"/x", nil)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if res.StatusCode != 301 || !strings.HasSuffix(res.Header.Get("Location"), ":"+port+"/x") {
		t.Fatalf("redirect: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no shutdown")
	}
}

func TestNewRequiresDeps(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected error")
	}
}
