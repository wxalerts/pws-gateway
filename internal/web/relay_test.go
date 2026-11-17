package web

import (
	"context"
	"strings"
	"testing"

	"github.com/wxalerts/pws-gateway/internal/relay/cwop"
)

type fakeRelay struct {
	pass    string
	cleared bool
}

func (f *fakeRelay) CWOP(context.Context) cwop.Status {
	return cwop.Status{State: "ok", PasscodeSet: f.pass != ""}
}
func (f *fakeRelay) SetCWOPPasscode(p string) error { f.pass = p; return nil }
func (f *fakeRelay) ClearCWOPPasscode() error       { f.pass, f.cleared = "", true; return nil }

func TestRelayEndpointsAndNoPasscodeLeak(t *testing.T) {
	e := newEnv(t, nil)
	fr := &fakeRelay{}
	e.srv.opts.Relay = fr
	if r := e.do("GET", "/api/relay", "", nil); r.Code != 401 {
		t.Fatalf("no session = %d", r.Code)
	}
	cookie := e.login()
	if r := e.do("PUT", "/api/relay/cwop/passcode", `{"passcode":"TOPSECRET99"}`, cookie, CSRFHeader, ""); r.Code != 403 {
		t.Fatalf("no CSRF header = %d", r.Code)
	}
	if r := e.do("PUT", "/api/relay/cwop/passcode", `{"passcode":"TOPSECRET99"}`, cookie); r.Code != 200 || fr.pass != "TOPSECRET99" {
		t.Fatalf("put = %d", r.Code)
	}
	r := e.do("GET", "/api/relay", "", cookie)
	if r.Code != 200 || strings.Contains(r.Body.String(), "TOPSECRET99") || !strings.Contains(r.Body.String(), `"passcode_set":true`) {
		t.Fatalf("get = %d %s", r.Code, r.Body)
	}
	if r := e.do("DELETE", "/api/relay/cwop/passcode", "", cookie); r.Code != 200 || !fr.cleared {
		t.Fatalf("delete = %d", r.Code)
	}
	// Settings go through the generic config section and are validated.
	if r := e.do("PUT", "/api/config/relay", `{"cwop":{"owner":"device","station":"A:1:2","id":"CW12","position_opt_in":false}}`, cookie); r.Code != 422 && r.Code != 400 {
		t.Fatalf("invalid relay config accepted: %d %s", r.Code, r.Body)
	}
	good := `{"cwop":{"owner":"device","station":"Acurite-5n1:1234:A","id":"CW1234","credential_file":"","server":"","position_opt_in":true,"lat":39.1,"lon":-94.5}}`
	if r := e.do("PUT", "/api/config/relay", good, cookie); r.Code != 200 {
		t.Fatalf("valid relay config = %d %s", r.Code, r.Body)
	}
	if got := e.cfg.Get().Relay.CWOP; got.ID != "CW1234" || !got.PositionOptIn {
		t.Fatalf("stored %+v", got)
	}
}
