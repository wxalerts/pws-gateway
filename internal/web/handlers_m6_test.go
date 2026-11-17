package web

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/netcfg"
	"github.com/wxalerts/pws-gateway/internal/platform/fake"
	"github.com/wxalerts/pws-gateway/internal/update"
)

func TestNetworkAPIWithoutManagerIsUnavailable(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login()
	if r := e.do("GET", "/api/network", "", c); r.Code != 503 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if r := e.do("GET", "/api/network", "", nil); r.Code != 401 {
		t.Fatalf("unauthenticated: %d", r.Code)
	}
}

func TestNetworkCommitConfirmOverHTTP(t *testing.T) {
	e := newEnv(t, nil)
	nm := fake.NewNet()
	e.srv.opts.Network = netcfg.New(netcfg.Options{NM: nm, DataDir: t.TempDir(), Now: func() time.Time { return *e.clock }})
	c := e.login()

	var st netcfg.State
	e.do("GET", "/api/network", "", c).json(&st)
	if len(st.Connections) != 1 || st.Hostname != "pws" {
		t.Fatalf("state = %+v", st)
	}

	body := `{"connections":[{"name":"wired","type":"ethernet","interface":"eth0","autoconnect":true,` +
		`"ipv4":{"method":"manual","address":"192.168.1.99/24","gateway":"192.168.1.1","dns_search":["lab.example"]}}]}`
	var ap map[string]any
	e.do("POST", "/api/network/apply", body, c).json(&ap)
	want := e.clock.Add(90 * time.Second).UTC().Format(time.RFC3339)
	if ap["applied"] != true || ap["confirm_deadline"] != want {
		t.Fatalf("apply = %v, want deadline %s", ap, want)
	}
	if r := e.do("POST", "/api/network/apply", body, c); r.Code != 409 {
		t.Fatalf("second apply: %d", r.Code)
	}
	e.do("GET", "/api/network", "", c).json(&st)
	if st.Pending == nil {
		t.Fatal("pending missing from state")
	}
	if r := e.do("POST", "/api/network/confirm", "", c); r.Code != 200 {
		t.Fatalf("confirm: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/api/network/confirm", "", c); r.Code != 409 {
		t.Fatalf("confirm twice: %d", r.Code)
	}

	// Not confirmed in time: reverted, and a late confirm says so.
	if r := e.do("POST", "/api/network/apply", strings.Replace(body, "192.168.1.99", "192.168.1.98", 1), c); r.Code != 200 {
		t.Fatalf("apply: %d %s", r.Code, r.Body)
	}
	*e.clock = e.clock.Add(95 * time.Second)
	if r := e.do("POST", "/api/network/confirm", "", c); r.Code != 409 && r.Code != 410 {
		t.Fatalf("late confirm: %d %s", r.Code, r.Body)
	}
	st = netcfg.State{}
	e.do("GET", "/api/network", "", c).json(&st)
	if st.Pending != nil || st.Connections[0].IPv4.Address != "192.168.1.99/24" {
		t.Fatalf("late change not reverted to the confirmed one: %+v", st.Connections[0])
	}

	if r := e.do("POST", "/api/network/apply", `{"connections":[{"name":"wired","type":"ethernet","ipv4":{"method":"manual","address":"nope"}}]}`, c); r.Code != 422 {
		t.Fatalf("invalid: %d %s", r.Code, r.Body)
	}
	if r := e.do("DELETE", "/api/network/connections/wired", "", c); r.Code != 409 {
		t.Fatalf("delete active: %d", r.Code)
	}
	if r := e.do("GET", "/api/network/wifi", "", c); r.Code != 200 || !strings.Contains(r.Body.String(), "HomeNet") {
		t.Fatalf("scan: %d %s", r.Code, r.Body)
	}
}

type fakeUpdates struct {
	mu    sync.Mutex
	st    update.State
	calls []bool
	err   error
}

func (f *fakeUpdates) Status() update.State { f.mu.Lock(); defer f.mu.Unlock(); return f.st }
func (f *fakeUpdates) StartCheck(force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, force)
	return f.err
}

func TestUpdateAPIWithUpdater(t *testing.T) {
	e := newEnv(t, nil)
	target := "1.3.0"
	fu := &fakeUpdates{st: update.State{
		State: update.StateDeferred, Current: "1.2.3", Target: &target, TS: *e.clock,
		DeferKind: update.DeferWindow, Detail: "waiting for the update window 02:00-05:00", OSDeferred: true,
	}}
	e.srv.opts.Updates = fu
	c := e.login()
	var v map[string]any
	e.do("GET", "/api/update", "", c).json(&v)
	if v["state"] != "deferred" || v["defer_kind"] != "window" || v["target"] != "1.3.0" || v["window"] != "02:00-05:00" ||
		v["enabled"] != true || v["os_deferred"] != true {
		t.Fatalf("update = %v", v)
	}
	if r := e.do("POST", "/api/update/check", "", c); r.Code != 202 {
		t.Fatalf("check: %d", r.Code)
	}
	if r := e.do("POST", "/api/update/apply", "", c); r.Code != 202 {
		t.Fatalf("apply: %d", r.Code)
	}
	if len(fu.calls) != 2 || fu.calls[0] || !fu.calls[1] {
		t.Fatalf("force flags = %v", fu.calls)
	}
	fu.err = ErrUpdateBusy
	if r := e.do("POST", "/api/update/check", "", c); r.Code != 409 {
		t.Fatalf("busy: %d", r.Code)
	}
}
