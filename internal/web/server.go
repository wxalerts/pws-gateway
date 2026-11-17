package web

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/wxalerts/pws-gateway/internal/auth"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/logging"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

// Limits and timeouts.
const (
	MaxBodyBytes      = 1 << 20
	ReadHeaderTimeout = 10 * time.Second
	IdleTimeout       = 120 * time.Second
	ShutdownGrace     = 10 * time.Second
	// SessionCookie is the cookie that carries the session id.
	SessionCookie = "pwsgw_session"
	// CSRFHeader and CSRFValue must accompany every mutating request.
	CSRFHeader = "X-Requested-With"
	CSRFValue  = "pwsgw"
)

// ContentSecurityPolicy is sent on every response. The UI has no inline
// scripts or styles, so nothing needs 'unsafe-inline'.
const ContentSecurityPolicy = "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// Options wires the server to the rest of the daemon. Platform, Config, Auth
// and Sessions are required; everything else has a working default.
type Options struct {
	Platform platform.Platform
	Config   *config.Store
	Auth     *auth.Store
	Sessions *auth.Sessions
	Limiter  *auth.Limiter
	Auditor  auth.Auditor
	Logs     *logging.Ring
	Stations StationSource
	Status   StatusSource
	Readings ReadingsSource
	// Claim links the gateway to a WxAlerts account. nil means unavailable.
	Claim ClaimSource
	// Relay reports the device-owned relays. nil means none.
	Relay RelaySource
	// Dist is the UI bundle rooted at index.html. nil serves the placeholder.
	Dist   fs.FS
	Logger *slog.Logger
	// Version, Commit and BuildDate feed /api/version and /api/health.
	Version, Commit, BuildDate string
	// Now and ConfirmWindow exist for tests.
	Now           func() time.Time
	ConfirmWindow time.Duration
}

// Server is the HTTP handler plus the state its handlers share.
type Server struct {
	opts Options
	log  *slog.Logger
	now  func() time.Time
	mux  *http.ServeMux
}

// New validates opts, fills defaults and builds the router.
func New(opts Options) (*Server, error) {
	if opts.Platform == nil || opts.Config == nil || opts.Auth == nil || opts.Sessions == nil {
		return nil, errors.New("web: Platform, Config, Auth and Sessions are required")
	}
	if opts.Limiter == nil {
		opts.Limiter = auth.NewLimiter(auth.LimiterOptions{})
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Auditor.Logger == nil {
		opts.Auditor.Logger = opts.Logger
	}
	if opts.Stations == nil {
		opts.Stations = NopStations{}
	}
	if opts.Status == nil {
		opts.Status = NopStatus{Version: opts.Version}
	}
	if opts.Readings == nil {
		opts.Readings = NopReadings{}
	}
	if opts.Relay == nil {
		opts.Relay = NopRelay{}
	}
	if opts.Claim == nil {
		opts.Claim = NopClaim{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ConfirmWindow <= 0 {
		opts.ConfirmWindow = config.DefaultConfirmWindow
	}
	s := &Server{opts: opts, log: opts.Logger, now: opts.Now, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

// Handler returns the full middleware chain.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = s.limitBody(h)
	h = s.csrf(h)
	h = s.securityHeaders(h)
	h = s.accessLog(h)
	return h
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/setup", s.handleSetupStatus)
	m.HandleFunc("POST /api/setup", s.handleSetupCreate)
	m.HandleFunc("GET /api/session", s.handleSessionGet)
	m.HandleFunc("POST /api/session", s.handleLogin)
	m.HandleFunc("DELETE /api/session", s.handleLogout)
	m.HandleFunc("POST /api/session/password", s.authed(s.handlePassword))

	m.HandleFunc("GET /api/health", s.authed(s.handleHealth))
	m.HandleFunc("GET /api/stations", s.authed(s.handleStations))
	m.HandleFunc("PUT /api/stations/{key...}", s.authed(s.handleStationPut))
	m.HandleFunc("GET /api/radios", s.authed(s.handleRadios))
	m.HandleFunc("PUT /api/radios/{serial}", s.authed(s.handleRadioPut))
	m.HandleFunc("GET /api/config", s.authed(s.handleConfigGet))
	m.HandleFunc("POST /api/config/apply", s.authed(s.handleConfigApply))
	m.HandleFunc("POST /api/config/confirm", s.authed(s.handleConfigConfirm))
	m.HandleFunc("GET /api/config/pending", s.authed(s.handleConfigPending))
	m.HandleFunc("PUT /api/config/{section}", s.authed(s.handleConfigPut))
	m.HandleFunc("GET /api/uplink", s.authed(s.handleUplink))
	m.HandleFunc("GET /api/relay", s.authed(s.handleRelay))
	m.HandleFunc("PUT /api/relay/cwop/passcode", s.authed(s.handleCWOPPasscodePut))
	m.HandleFunc("DELETE /api/relay/cwop/passcode", s.authed(s.handleCWOPPasscodeDelete))
	m.HandleFunc("GET /api/claim", s.authed(s.handleClaim))
	m.HandleFunc("POST /api/claim/start", s.authed(s.handleClaimStart))
	m.HandleFunc("POST /api/claim/cancel", s.authed(s.handleClaimCancel))
	m.HandleFunc("POST /api/claim/unlink", s.authed(s.handleClaimUnlink))
	m.HandleFunc("GET /api/update", s.authed(s.handleUpdate))
	m.HandleFunc("POST /api/update/check", s.authed(s.handleNotImplemented))
	m.HandleFunc("POST /api/update/apply", s.authed(s.handleNotImplemented))
	m.HandleFunc("GET /api/logs", s.authed(s.handleLogs))
	m.HandleFunc("GET /api/readings", s.authed(s.handleReadings))
	m.HandleFunc("GET /api/version", s.authed(s.handleVersion))
	m.HandleFunc("/api/", s.handleAPINotFound)

	m.HandleFunc("/", s.handleSPA)
}

// ---- JSON helpers ---------------------------------------------------------

type errorBody struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("web: write response", "err", err)
	}
}

func writeErr(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, errorBody{Error: code, Detail: detail})
}

// decodeBody reads a JSON object from the request. It answers the client and
// returns false on failure.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "request body exceeds 1 MB")
			return false
		}
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be valid JSON")
		return false
	}
	return true
}

// ---- middleware -----------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		s.log.Debug("http request",
			"method", r.Method, "path", r.URL.Path, "status", sw.status,
			"bytes", sw.bytes, "dur_ms", s.now().Sub(start).Milliseconds(), "remote", remoteAddr(r))
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("Strict-Transport-Security", "max-age=63072000")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// csrf rejects mutating requests that a cross-site page could have forged.
// A custom header cannot be set by a plain HTML form or a simple cross-origin
// fetch without a CORS preflight, which this server never approves. The fetch
// metadata or Origin check is a second, independent layer.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get(CSRFHeader) != CSRFValue || !sameSite(r) {
			writeErr(w, http.StatusForbidden, "csrf", "cross-site request rejected")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameSite(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && u.Host != "" && u.Host == r.Host
	}
	return false
}

// ---- authentication plumbing ---------------------------------------------

type ctxKey struct{}

func usernameFrom(ctx context.Context) string {
	if u, ok := ctx.Value(ctxKey{}).(string); ok {
		return u
	}
	return ""
}

func remoteAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) currentSession(r *http.Request) (auth.Session, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return auth.Session{}, false
	}
	return s.opts.Sessions.Get(c.Value)
}

func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.currentSession(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthenticated", "")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess.Username)))
	}
}

func (s *Server) setSessionCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: id, Path: "/",
		MaxAge: int(auth.DefaultSessionMaxLifetime / time.Second),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

// ---- serving --------------------------------------------------------------

// RedirectHandler answers every request with a 301 to the HTTPS address.
// httpsPort is the port of the HTTPS listener; 443 is left out of the URL.
func RedirectHandler(httpsPort string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		target := url.URL{Scheme: "https", Host: host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
		if httpsPort != "" && httpsPort != "443" {
			target.Host = net.JoinHostPort(host, httpsPort)
		} else if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
			target.Host = "[" + host + "]"
		}
		http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
	})
}

// Serve runs the HTTPS server on httpsLn and, when redirectLn is not nil, the
// plain HTTP redirector. It blocks until ctx is canceled, then drains for up
// to ShutdownGrace. tlsCert is served by the HTTPS listener; a nil value makes
// Serve speak plain HTTP on httpsLn, which only tests use.
func (s *Server) Serve(ctx context.Context, httpsLn, redirectLn net.Listener, tlsCert *tls.Certificate) error {
	main := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: ReadHeaderTimeout,
		IdleTimeout:       IdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	if tlsCert != nil {
		main.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*tlsCert}}
	}
	servers := []*http.Server{main}
	errc := make(chan error, 2)
	go func() {
		if tlsCert != nil {
			errc <- main.ServeTLS(httpsLn, "", "")
		} else {
			errc <- main.Serve(httpsLn)
		}
	}()
	if redirectLn != nil {
		port := ""
		if _, p, err := net.SplitHostPort(httpsLn.Addr().String()); err == nil {
			port = p
		}
		red := &http.Server{
			Handler:           RedirectHandler(port),
			ReadHeaderTimeout: ReadHeaderTimeout,
			IdleTimeout:       IdleTimeout,
		}
		servers = append(servers, red)
		go func() { errc <- red.Serve(redirectLn) }()
	}

	var first error
	select {
	case <-ctx.Done():
	case first = <-errc:
	}
	sctx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(sctx); err != nil && first == nil {
			first = err
		}
	}
	if errors.Is(first, http.ErrServerClosed) {
		first = nil
	}
	return first
}
