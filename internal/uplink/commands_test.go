package uplink

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
	"github.com/wxalerts/pws-gateway/keys"
)

func (r *rig) reboots() int {
	n := 0
	r.plat.Set(func(p *mock.Platform) { n = p.RebootCalls })
	return n
}

func cmdTopic(typ string) string { return contract.CommandTopic(testDevice, typ) }

func (r *rig) acks(id string) []json.RawMessage {
	var out []json.RawMessage
	for _, it := range r.outbox(contract.AckTopic(testDevice, id)) {
		out = append(out, it.Payload)
	}
	return out
}

func decodeAck(t *testing.T, data []byte) contract.Ack {
	t.Helper()
	if err := contract.Validate(contract.KindAck, data); err != nil {
		t.Fatalf("ack does not validate: %v\n%s", err, data)
	}
	var a contract.Ack
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

// vectorCommand returns a command from the published signing test vectors.
func vectorCommand(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../schemas/examples/signing/ed25519.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Vectors []struct {
			Name    string         `json:"name"`
			Command map[string]any `json:"command"`
			Sig     string         `json:"sig"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, v := range doc.Vectors {
		if v.Name == name {
			v.Command["sig"] = v.Sig
			return mustJSON(t, v.Command)
		}
	}
	t.Fatalf("no vector %q", name)
	return nil
}

// TestSignedRebootFromTestVectorIsAckedOverMQTT drives the published test
// vector through a real broker connection.
func TestSignedRebootFromTestVectorIsAckedOverMQTT(t *testing.T) {
	b := newBroker(t)
	r := newRig(t, t0) // the vectors are valid at 2026-11-16T14:30:00Z
	r.plat.Set(func(p *mock.Platform) { p.RebootErr = nil })
	r.connected(b, nil)

	b.publish(t, cmdTopic("reboot"), vectorCommand(t, "reboot"))
	ackTopic := contract.AckTopic(testDevice, "c_02")
	waitFor(t, 5*time.Second, "ack at the subscriber", func() bool { return b.count(ackTopic) == 1 })
	a := decodeAck(t, b.messages(ackTopic)[0].Payload)
	if !a.OK || a.ID != "c_02" || a.Type != contract.CmdReboot || a.Error != nil {
		t.Fatalf("ack: %+v", a)
	}
	if n := r.reboots(); n != 1 {
		t.Fatalf("Reboot called %d times", n)
	}
}

func TestRebootUnsupportedBecomesFailedAck(t *testing.T) {
	r := newRig(t, t0) // mock Reboot returns ErrUnsupported by default
	u := r.uplink(nil)
	u.handleCommand(context.Background(), cmdTopic("reboot"), signedCmd(t, "c_u", "reboot", t0.Add(time.Hour), map[string]any{}))
	acks := r.acks("c_u")
	if len(acks) != 1 {
		t.Fatalf("acks = %d", len(acks))
	}
	if a := decodeAck(t, acks[0]); a.OK || a.Error == nil || !strings.Contains(*a.Error, "not supported") {
		t.Fatalf("ack: %+v", a)
	}
}

func TestRejectedCommandsAreIgnoredWithoutAck(t *testing.T) {
	r := newRig(t, t0)
	r.plat.Set(func(p *mock.Platform) { p.RebootErr = nil }) // a wrongly accepted reboot would show in RebootCalls
	u := r.uplink(nil)
	ctx := context.Background()
	exp := t0.Add(time.Hour)

	good := signedCmd(t, "c_ok", "reboot", exp, map[string]any{})
	var tampered contract.Command
	if err := json.Unmarshal(good, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.ID = "c_tampered" // body changed after signing
	otherDevice := contract.Command{ID: "c_other", Type: "reboot", DeviceID: "d_other", Exp: exp, Body: json.RawMessage(`{}`)}
	if err := contract.SignCommand(testKey(t), &otherDevice); err != nil {
		t.Fatal(err)
	}
	wrongKey := &contract.Command{ID: "c_wrongkey", Type: "reboot", DeviceID: testDevice, Exp: exp, Body: json.RawMessage(`{}`)}
	_, other, _ := ed25519.GenerateKey(nil)
	if err := contract.SignCommand(other, wrongKey); err != nil {
		t.Fatal(err)
	}
	unsigned := mustJSON(t, map[string]any{"id": "c_unsigned", "type": "reboot", "device_id": testDevice, "exp": exp.Format(time.RFC3339), "body": map[string]any{}})

	cases := []struct {
		name, topic string
		payload     []byte
	}{
		{"unsigned", cmdTopic("reboot"), unsigned},
		{"tampered", cmdTopic("reboot"), mustJSON(t, tampered)},
		{"wrong key", cmdTopic("reboot"), mustJSON(t, wrongKey)},
		{"other device", cmdTopic("reboot"), mustJSON(t, otherDevice)},
		{"expired", cmdTopic("reboot"), signedCmd(t, "c_exp", "reboot", t0.Add(-time.Second), map[string]any{})},
		{"type differs from topic", cmdTopic("bundle"), signedCmd(t, "c_mismatch", "reboot", exp, map[string]any{})},
		{"shell topic", cmdTopic("shell"), signedCmd(t, "c_shell", "reboot", exp, map[string]any{})},
		{"shell type", cmdTopic("shell"), mustJSON(t, map[string]any{"id": "c_sh2", "type": "shell", "device_id": testDevice, "exp": exp.Format(time.RFC3339), "body": map[string]any{"cmd": "id"}, "sig": strings.Repeat("A", 86) + "=="})},
		{"not json", cmdTopic("reboot"), []byte("reboot now")},
		{"foreign topic", "pws/d_other/cmd/reboot", good},
	}
	for _, c := range cases {
		u.handleCommand(ctx, c.topic, c.payload)
	}
	if r.plat.RebootCalls != 0 {
		t.Fatalf("a rejected command rebooted the device %d times", r.plat.RebootCalls)
	}
	if items, _ := r.store.Pending(ctx, 100); len(items) != 0 {
		t.Fatalf("rejected commands produced %d outbox entries (acks)", len(items))
	}

	// A valid command works once, and its replay is ignored.
	u.handleCommand(ctx, cmdTopic("reboot"), good)
	u.handleCommand(ctx, cmdTopic("reboot"), good)
	if r.plat.RebootCalls != 1 {
		t.Fatalf("Reboot calls = %d, want exactly 1 (replay must be ignored)", r.plat.RebootCalls)
	}
	if n := len(r.acks("c_ok")); n != 1 {
		t.Fatalf("acks for the replayed id = %d, want 1", n)
	}

	// The replay cache is persisted: a restarted uplink still refuses it.
	u2 := r.uplink(nil)
	u2.handleCommand(ctx, cmdTopic("reboot"), good)
	if r.plat.RebootCalls != 1 {
		t.Fatal("replay accepted after a restart")
	}
}

func TestNoKeyPinnedIgnoresEverything(t *testing.T) {
	r := newRig(t, t0)
	r.plat.Set(func(p *mock.Platform) { p.RebootErr = nil })
	u := r.uplink(func(o *Options) { o.CommandKey = nil })
	u.handleCommand(context.Background(), cmdTopic("reboot"), signedCmd(t, "c_nk", "reboot", t0.Add(time.Hour), map[string]any{}))
	if r.plat.RebootCalls != 0 || len(r.acks("c_nk")) != 0 {
		t.Fatal("command acted on without a pinned key")
	}
}

func TestConfigCommandApplied(t *testing.T) {
	b := newBroker(t)
	r := newRig(t, t0)
	r.connected(b, nil)
	body := map[string]any{"history": map[string]any{"cadence_minutes": 30}}
	b.publish(t, cmdTopic("config"), signedCmd(t, "c_cfg", "config", t0.Add(time.Hour), body))
	ackTopic := contract.AckTopic(testDevice, "c_cfg")
	waitFor(t, 5*time.Second, "config ack", func() bool { return b.count(ackTopic) == 1 })
	a := decodeAck(t, b.messages(ackTopic)[0].Payload)
	if !a.OK || !strings.Contains(string(a.Result), "history") {
		t.Fatalf("ack: %+v %s", a, a.Result)
	}
	if got := r.cfg.Get().History.CadenceMinutes; got != 30 {
		t.Fatalf("cadence = %d, config not applied", got)
	}
}

func TestConfigCommandValidationFailureIsAckedAndNotApplied(t *testing.T) {
	r := newRig(t, t0)
	u := r.uplink(nil)
	ctx := context.Background()
	for id, body := range map[string]map[string]any{
		"c_bad1": {"history": map[string]any{"cadence_minutes": 1}},          // below the minimum
		"c_bad2": {"shell": map[string]any{"cmd": "id"}},                     // unknown section
		"c_bad3": {"history": map[string]any{"cadence_minutes": 20, "x": 1}}, // unknown field
		"c_bad4": {"uplink": map[string]any{"broker_url": "http://x"}},       // not an MQTT scheme
		"c_bad5": {},                                                         // empty patch
	} {
		u.handleCommand(ctx, cmdTopic("config"), signedCmd(t, id, "config", t0.Add(time.Hour), body))
		acks := r.acks(id)
		if len(acks) != 1 {
			t.Fatalf("%s: acks = %d", id, len(acks))
		}
		if a := decodeAck(t, acks[0]); a.OK || a.Error == nil {
			t.Fatalf("%s acked as success: %+v", id, a)
		}
	}
	if r.cfg.Get().History.CadenceMinutes != 15 || r.cfg.Get().Uplink.BrokerURL != "mqtts://mqtt.wxalerts.org:8883" {
		t.Fatalf("a rejected patch changed the config: %+v", r.cfg.Get())
	}
}

func TestStubCommandsAckNotImplemented(t *testing.T) {
	r := newRig(t, t0)
	u := r.uplink(nil)
	ctx := context.Background()
	for _, c := range []struct {
		typ  string
		body any
		want string
	}{
		{contract.CmdBundle, map[string]any{}, errNotImplemented},
		{contract.CmdUpdateCheck, map[string]any{}, errNotImplemented},
		{contract.CmdDiag, map[string]any{"action": "rediscover"}, errNotImplemented},
		{contract.CmdCapture, map[string]any{"duration_s": 5}, errCaptureV2},
	} {
		id := "c_" + strings.ReplaceAll(c.typ, "-", "_")
		u.handleCommand(ctx, cmdTopic(c.typ), signedCmd(t, id, c.typ, t0.Add(time.Hour), c.body))
		acks := r.acks(id)
		if len(acks) != 1 {
			t.Fatalf("%s: acks = %d", c.typ, len(acks))
		}
		a := decodeAck(t, acks[0])
		if a.OK || a.Error == nil || *a.Error != c.want || a.Type != c.typ {
			t.Fatalf("%s: ack %+v", c.typ, a)
		}
	}
}

func TestResyncReenqueuesRange(t *testing.T) {
	r := newRig(t, time.Date(2026, 11, 16, 14, 1, 0, 0, time.UTC))
	first := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	r.writeBuckets(first, 24)
	u := r.uplink(nil)
	ctx := context.Background()
	if _, err := u.obsCycle(ctx); err != nil {
		t.Fatal(err)
	}
	for _, it := range r.outbox(obsTopic()) { // pretend everything was delivered
		fatalIf(t, r.store.Ack(ctx, it.Seq))
	}
	st, _ := u.loadState(ctx)

	from, to := first.Add(time.Hour), first.Add(90*time.Minute)
	u.handleCommand(ctx, cmdTopic("resync"), signedCmd(t, "c_rs", "resync", t0.Add(time.Hour), map[string]any{
		"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339),
	}))
	a := decodeAck(t, r.acks("c_rs")[0])
	if !a.OK || !strings.Contains(string(a.Result), `"buckets":7`) {
		t.Fatalf("ack: %+v %s", a, a.Result)
	}
	var starts []time.Time
	var seqs []uint64
	for _, it := range r.outbox(obsTopic()) {
		o := decodeObs(t, it.Payload)
		seqs = append(seqs, o.Seq)
		for _, bk := range o.Buckets {
			starts = append(starts, bk.Start)
		}
	}
	if len(starts) != 7 || !starts[0].Equal(from) || !starts[6].Equal(to) {
		t.Fatalf("resent buckets: %v", starts)
	}
	if seqs[0] <= st.Seq {
		t.Fatalf("resync reused seq %d (cursor was %d)", seqs[0], st.Seq)
	}
	after, _ := u.loadState(ctx)
	if !after.BuiltThrough.Equal(st.BuiltThrough) {
		t.Fatalf("resync moved the cursor: %v -> %v", st.BuiltThrough, after.BuiltThrough)
	}
}

// TestCommandSetIsClosed asserts the structural no-shell guarantee: the
// dispatch table is exactly the contract's seven types, there is no shell or
// install type, and the diag allow list is exactly the contract's four
// actions.
func TestCommandSetIsClosed(t *testing.T) {
	want := []string{"config", "reboot", "bundle", "update-check", "resync", "diag", "capture"}
	if !slices.Equal(slices.Sorted(slices.Values(commandTypes)), slices.Sorted(slices.Values(want))) {
		t.Fatalf("command types = %v", commandTypes)
	}
	for _, bad := range []string{"shell", "exec", "install", "update", "script", "file"} {
		if slices.Contains(commandTypes, bad) {
			t.Fatalf("%q must never be a command type", bad)
		}
	}
	wantDiag := []string{"restart_service", "rediscover", "connectivity_test", "clear_outbox"}
	if !slices.Equal(slices.Sorted(slices.Values(diagActions)), slices.Sorted(slices.Values(wantDiag))) {
		t.Fatalf("diag actions = %v", diagActions)
	}
	// An action off the list is refused even with a valid signature, and the
	// handler is never reached.
	r := newRig(t, t0)
	called := false
	u := r.uplink(func(o *Options) {
		o.DiagHandler = func(context.Context, contract.DiagBody) (map[string]any, error) { called = true; return nil, nil }
	})
	_, err := u.cmdDiag(context.Background(), json.RawMessage(`{"action":"sh -c id"}`))
	if err == nil || called {
		t.Fatalf("off-list diag action reached the handler: %v", err)
	}
	if _, err := u.cmdDiag(context.Background(), json.RawMessage(`{"action":"rediscover"}`)); err != nil || !called {
		t.Fatalf("allowed action: %v called=%v", err, called)
	}
}

func TestPinnedKeys(t *testing.T) {
	raw, err := keys.FS.ReadFile(keys.CommandSigningTest)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ParsePublicKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Equal(testKey(t).Public()) {
		t.Fatal("keys/command-signing.test.pub is not the public half of the published test seed")
	}
	dir := t.TempDir()
	if _, _, err := LoadCommandKey(dir); !errors.Is(err, ErrNoCommandKey) {
		t.Fatalf("no key anywhere: %v", err)
	}
	if err := os.MkdirAll(dir+"/keys", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/keys/command-signing.pub", raw, 0o600); err != nil {
		t.Fatal(err)
	}
	k, src, err := LoadCommandKey(dir)
	if err != nil || !k.Equal(pub) || !strings.Contains(src, "command-signing.pub") {
		t.Fatalf("data dir key: %v %q %v", k, src, err)
	}
	if _, err := ParsePublicKey([]byte("not a key")); err == nil {
		t.Fatal("garbage parsed as a key")
	}
}
