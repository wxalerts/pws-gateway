package uplink

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/wxalerts/pws-gateway/internal/collector"
	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/model"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
	"github.com/wxalerts/pws-gateway/internal/store"
)

const (
	testDevice  = "d_8f3a"
	realCapture = "../../testdata/real/acurite-433-20261116.jsonl"
	stationKey  = "Acurite-5n1:4026:A"
)

var t0 = time.Date(2026, 11, 16, 14, 30, 0, 0, time.UTC)

// fakeClock is a settable time source shared by the uplink under test.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func (c *fakeClock) Advance(d time.Duration) { c.Set(c.Now().Add(d)) }

// testKey returns the published test signing key (seed in
// schemas/examples/signing/ed25519.json).
func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed, err := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// received is one message the test broker's inline subscriber saw.
type received struct {
	Topic   string
	Payload []byte
	Retain  bool
	At      time.Time
}

type testBroker struct {
	srv  *mqtt.Server
	addr string
	mu   sync.Mutex
	got  []received
}

// newBroker starts an in-process MQTT 5 broker on a free loopback port that
// records every publish, retained or not.
func newBroker(t *testing.T) *testBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	srv := mqtt.New(&mqtt.Options{InlineClient: true, Logger: quietLogger()})
	if err := srv.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.AddListener(listeners.NewTCP(listeners.Config{ID: "t", Address: addr})); err != nil {
		t.Fatal(err)
	}
	b := &testBroker{srv: srv, addr: addr}
	for i, filter := range []string{"pws/#", "V2/#"} {
		if err := srv.Subscribe(filter, i+1, func(_ *mqtt.Client, _ packets.Subscription, pk packets.Packet) {
			b.mu.Lock()
			b.got = append(b.got, received{Topic: pk.TopicName, Payload: append([]byte(nil), pk.Payload...), Retain: pk.FixedHeader.Retain, At: time.Now()})
			b.mu.Unlock()
		}); err != nil {
			t.Fatal(err)
		}
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	// Serve starts listeners asynchronously; wait until the port accepts.
	waitFor(t, 5*time.Second, "broker listening", func() bool {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	})
	return b
}

func (b *testBroker) url() string { return "mqtt://" + b.addr }

func (b *testBroker) messages(topic string) []received {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []received
	for _, m := range b.got {
		if m.Topic == topic {
			out = append(out, m)
		}
	}
	return out
}

func (b *testBroker) count(topic string) int { return len(b.messages(topic)) }

func (b *testBroker) retained(topic string) []packets.Packet { return b.srv.Topics.Messages(topic) }

// publish sends a message as the server would.
func (b *testBroker) publish(t *testing.T, topic string, payload []byte) {
	t.Helper()
	if err := b.srv.Publish(topic, payload, false, 1); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

// rig is a store, config, mock platform and clock wired into an Uplink.
type rig struct {
	t     *testing.T
	store *store.Store
	cfg   *config.Store
	plat  *mock.Platform
	clock *fakeClock
	opts  Options
}

func newRig(t *testing.T, start time.Time) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cs := config.NewStore(t.TempDir())
	if _, err := cs.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Update(func(c *config.Config) error {
		c.Stations[stationKey] = config.Station{Selected: true, Name: "Roof", AltM: 31, WindHeightM: 4.5}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, store: st, cfg: cs, plat: mock.New(), clock: newClock(start)}
	r.opts = Options{
		DeviceID: testDevice, BrokerURL: "mqtt://127.0.0.1:1", Credentials: Credentials{Username: testDevice, Password: "s3cret-pw"},
		Config: cs, Store: st, Platform: r.plat, Now: r.clock.Now, Logger: quietLogger(),
		CommandKey:     testKey(t).Public().(ed25519.PublicKey),
		ReplayInterval: 5 * time.Millisecond,
	}
	return r
}

func (r *rig) uplink(mod func(*Options)) *Uplink {
	r.t.Helper()
	o := r.opts
	if mod != nil {
		mod(&o)
	}
	u, err := New(o)
	if err != nil {
		r.t.Fatal(err)
	}
	return u
}

// connected returns an uplink running against b, stopped at test end.
func (r *rig) connected(b *testBroker, mod func(*Options)) *Uplink {
	r.t.Helper()
	u := r.uplink(func(o *Options) {
		o.BrokerURL = b.url()
		o.TLS = nil
		if mod != nil {
			mod(o)
		}
	})
	return r.run(u)
}

func (r *rig) run(u *Uplink) *Uplink {
	r.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := u.Run(ctx); err != nil {
			r.t.Errorf("Run: %v", err)
		}
	}()
	r.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			r.t.Error("uplink did not stop")
		}
	})
	waitFor(r.t, 10*time.Second, "uplink connected", func() bool { return u.connected.Load() })
	return u
}

// writeBuckets stores one gauge bucket per 5 minutes for the test station,
// n buckets starting at first.
func (r *rig) writeBuckets(first time.Time, n int) {
	r.t.Helper()
	var bs []model.Bucket
	for i := range n {
		bs = append(bs, model.Bucket{
			Start: first.Add(time.Duration(i) * BucketWidth), StationKey: stationKey, N: 7,
			Fields: map[string]model.Aggregate{
				model.FieldTempC: {Kind: model.KindGauge, N: 7, Avg: 18 + float64(i%5)/10, Min: 17.5, Max: 19},
			},
		})
	}
	if err := r.store.WriteRollups(context.Background(), bs); err != nil {
		r.t.Fatal(err)
	}
}

// replayCapture runs the real recording through a collector pipeline that
// writes into the rig's store, and returns the pipeline.
func (r *rig) replayCapture() *collector.Pipeline {
	r.t.Helper()
	w := store.NewWriter(r.store, store.WriterOptions{AllowShortInterval: true})
	p := collector.New(collector.Config{Store: r.store, Writer: w, TickEvery: -1, Logger: quietLogger()})
	ctx := context.Background()
	src := collector.NewReplayFile(ctx, &rtl433.Replay{Radio: "radio0", Location: time.UTC}, realCapture)
	if err := p.Run(ctx, src); err != nil {
		r.t.Fatal(err)
	}
	if err := src.Err(); err != nil {
		r.t.Fatal(err)
	}
	return p
}

// outboxTopics returns the pending outbox entries on one topic.
func (r *rig) outbox(topic string) []store.OutboxItem {
	r.t.Helper()
	items, err := r.store.Pending(context.Background(), 100000)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []store.OutboxItem
	for _, it := range items {
		if it.Topic == topic {
			out = append(out, it)
		}
	}
	return out
}

func obsTopic() string { return contract.Topic(testDevice, contract.KindObs) }

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeObs(t *testing.T, data []byte) contract.Obs {
	t.Helper()
	if err := contract.Validate(contract.KindObs, data); err != nil {
		t.Fatalf("obs does not validate: %v\n%s", err, data)
	}
	var o contract.Obs
	if err := json.Unmarshal(data, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

// signed builds a command signed with the test key.
func signedCmd(t *testing.T, id, typ string, exp time.Time, body any) []byte {
	t.Helper()
	c := &contract.Command{ID: id, Type: typ, DeviceID: testDevice, Exp: exp, Body: mustJSON(t, body)}
	if err := contract.SignCommand(testKey(t), c); err != nil {
		t.Fatal(err)
	}
	return mustJSON(t, c)
}

func fatalIf(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }
