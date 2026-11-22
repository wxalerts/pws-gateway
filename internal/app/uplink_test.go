package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
	"github.com/wxalerts/pws-gateway/internal/uplink"
)

func newUplinkApp(t *testing.T, mod func(*config.Config), deps func(*Deps)) (*App, string, *bytes.Buffer) {
	t.Helper()
	cs, dir := newConfig(t)
	if mod != nil {
		if _, err := cs.Update(func(c *config.Config) error { mod(c); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	d := Deps{
		DataDir: dir, Config: cs, Platform: mock.New(),
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Now:    func() time.Time { return time.Date(2026, 11, 16, 17, 20, 0, 0, time.UTC) },
	}
	if deps != nil {
		deps(&d)
	}
	a, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.store.Close() })
	return a, dir, &logs
}

func TestUnclaimedDeviceDoesNotStartUplink(t *testing.T) {
	a, _, logs := newUplinkApp(t, nil, nil)
	if a.Uplink() != nil {
		t.Fatal("uplink started without a device id")
	}
	if got := strings.Count(logs.String(), "unclaimed"); got != 1 {
		t.Fatalf("unclaimed logged %d times: %s", got, logs.String())
	}
	if st := a.Status().Uplink(context.Background()); st.Connected || st.DeviceID != "" {
		t.Fatalf("status: %+v", st)
	}
}

func TestMissingCredentialFileLeavesDeviceUnclaimed(t *testing.T) {
	a, _, logs := newUplinkApp(t, func(c *config.Config) { c.Uplink.DeviceID = "d_8f3a" }, nil)
	if a.Uplink() != nil || !strings.Contains(logs.String(), "credential file not found") {
		t.Fatalf("uplink=%v logs=%s", a.Uplink(), logs.String())
	}
}

func TestClaimedDeviceBuildsUplink(t *testing.T) {
	a, dir, logs := newUplinkApp(t, func(c *config.Config) { c.Uplink.DeviceID = "d_8f3a" }, nil)
	_ = a // the credential file is written after the first build, so rebuild
	if err := os.WriteFile(filepath.Join(dir, "uplink.credential"), []byte(`{"username":"d_8f3a","password":"s3cret-pw"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a2, err := New(Deps{DataDir: dir, Config: a.deps.Config, Platform: mock.New(), Logger: slog.New(slog.NewTextHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a2.store.Close() })
	u := a2.Uplink()
	if u == nil {
		t.Fatalf("uplink not built: %s", logs.String())
	}
	if st := a2.Status().Uplink(context.Background()); st.DeviceID != "d_8f3a" || st.Broker == "" || st.Connected {
		t.Fatalf("status: %+v", st)
	}
	if up := a2.Status().Update(context.Background()); up.State != "idle" {
		t.Fatalf("update: %+v", up)
	}
	if strings.Contains(logs.String(), "s3cret-pw") {
		t.Fatal("credential in the logs")
	}
}

func TestDryRunNeedsNoCredentialAndNeverConnects(t *testing.T) {
	a, _, _ := newUplinkApp(t,
		func(c *config.Config) { c.Uplink.DeviceID = "d_8f3a"; c.Uplink.BrokerURL = "mqtt://127.0.0.1:1" },
		func(d *Deps) { d.UplinkDryRun = true })
	u := a.Uplink()
	if u == nil {
		t.Fatal("dry run did not build the uplink")
	}
	if st := u.Status(); !st.DryRun || st.Connected {
		t.Fatalf("status: %+v", st)
	}
}

// identityServer answers GET /pws/device/identity like the API: Basic auth with
// the device id and password, the user id it is told to report.
func identityServer(t *testing.T, device, password string, user *int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if r.URL.Path != "/pws/device/identity" || !ok || u != device || p != password {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_credential"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"device_id":%q,"user_id":%d,"credential_version":1}`, device, *user)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestIdentityRefreshMovesAnOldGatewayToV2: a credential with no user id (a
// gateway claimed before contract v2) learns it from the server, keeps every
// other credential field, and follows an owner change.
func TestIdentityRefreshMovesAnOldGatewayToV2(t *testing.T) {
	var user int64 = 42
	srv := identityServer(t, "d_8f3a", "s3cret-pw", &user)
	a, dir, _ := newUplinkApp(t, func(c *config.Config) { c.Uplink.DeviceID = "d_8f3a" },
		func(d *Deps) { d.ClaimAPI = srv.URL })
	path := filepath.Join(dir, "uplink.credential")
	if err := os.WriteFile(path, []byte(`{"username":"d_8f3a","password":"s3cret-pw","client_cert_file":"c.pem","client_key_file":"k.pem"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if !a.refreshIdentity(ctx) {
		t.Fatal("the first refresh must report a change")
	}
	cred, err := uplink.LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if cred.UserID != 42 || cred.Password != "s3cret-pw" || cred.ClientCertFile != "c.pem" {
		t.Fatalf("credential after refresh: %+v", cred)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if a.refreshIdentity(ctx) {
		t.Fatal("an unchanged id must not report a change")
	}
	user = 43 // the gateway now belongs to someone else
	if !a.refreshIdentity(ctx) {
		t.Fatal("a new owner must be picked up")
	}
	if cred, _ = uplink.LoadCredentials(path); cred.UserID != 43 {
		t.Fatalf("user id %d", cred.UserID)
	}
}

func TestIdentityRefreshChangesNothingWhenRejectedOrUnreachable(t *testing.T) {
	var user int64 = 42
	srv := identityServer(t, "d_8f3a", "right-password", &user)
	a, dir, _ := newUplinkApp(t, func(c *config.Config) { c.Uplink.DeviceID = "d_8f3a" },
		func(d *Deps) { d.ClaimAPI = srv.URL })
	path := filepath.Join(dir, "uplink.credential")
	const old = `{"username":"d_8f3a","password":"wrong-password","user_id":7}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if a.refreshIdentity(context.Background()) {
		t.Fatal("a rejected credential must not change the file")
	}
	srv.Close()
	if a.refreshIdentity(context.Background()) {
		t.Fatal("an unreachable server must not change the file")
	}
	if got, _ := os.ReadFile(path); string(got) != old {
		t.Fatalf("file changed: %s", got)
	}
}
