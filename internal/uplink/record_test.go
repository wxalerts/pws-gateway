package uplink

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/model"
)

func gauge(v float64) *contract.Gauge { return &contract.Gauge{Avg: &v} }

func twoSensors(start time.Time) []contract.Bucket {
	alt, h1, h2 := 31.0, 4.5, 10.0
	return []contract.Bucket{{Start: start, Stations: []contract.Station{
		{Key: "A:1:", N: 5, Meta: &contract.StationMeta{AltM: &alt, WindHeightM: &h1},
			F:   contract.Fields{TempC: gauge(18), HumidityPct: gauge(60), WindSpeedMS: gauge(2), BatteryOK: &contract.Flag{Last: 1}},
			Raw: json.RawMessage(`{"mic":"CRC"}`)},
		{Key: "B:2:", N: 3, Meta: &contract.StationMeta{AltM: &alt, WindHeightM: &h2},
			F: contract.Fields{TempC: gauge(19), WindSpeedMS: gauge(5), UVIndex: gauge(3), BatteryOK: &contract.Flag{Last: 0}}},
	}}}
}

func TestCombineAmbiguousGroupsAreLeftOut(t *testing.T) {
	start := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	rec := combineBuckets(config.Config{}, twoSensors(start), start.Add(10*time.Minute), true, true)
	if len(rec) != 1 {
		t.Fatalf("got %d records, want 1", len(rec))
	}
	r := rec[0]
	// temperature and wind come from two sensors and have no owner mapping: left out.
	if r.F.TempC != nil || r.F.WindSpeedMS != nil {
		t.Errorf("ambiguous groups must be omitted: %+v", r.F)
	}
	// humidity only on A, uv only on B: each is the only candidate.
	if r.F.HumidityPct == nil || r.F.UVIndex == nil {
		t.Errorf("single-candidate groups must be supplied: %+v", r.F)
	}
	if r.N != 8 || !r.Live || !r.Synced {
		t.Errorf("n=%d live=%v synced=%v", r.N, r.Live, r.Synced)
	}
	if r.F.BatteryOK == nil || r.F.BatteryOK.Last != 0 {
		t.Errorf("battery must be the worst case of the contributors: %+v", r.F.BatteryOK)
	}
	if len(r.Raw) != 1 || r.Src["humidity_pct"] != "A:1:" || r.Src["uv_index"] != "B:2:" {
		t.Errorf("raw=%v src=%v", r.Raw, r.Src)
	}
}

func TestCombineOwnerMapping(t *testing.T) {
	start := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	cfg := config.Config{Composite: map[string]string{"temperature": "B:2:", "wind": "A:1:"}}
	r := combineBuckets(cfg, twoSensors(start), start.Add(10*time.Minute), true, true)[0]
	if *r.F.TempC.Avg != 19 || *r.F.WindSpeedMS.Avg != 2 {
		t.Fatalf("owner mapping ignored: temp %v wind %v", *r.F.TempC.Avg, *r.F.WindSpeedMS.Avg)
	}
	if r.Meta == nil || r.Meta.WindHeightM == nil || *r.Meta.WindHeightM != 4.5 {
		t.Errorf("wind height must come from the wind source: %+v", r.Meta)
	}
}

func TestCombineLiveFlag(t *testing.T) {
	start := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	cfg := config.Config{Composite: map[string]string{"temperature": "A:1:", "wind": "A:1:"}}
	b := twoSensors(start)
	for _, c := range []struct {
		name   string
		now    time.Time
		normal bool
		want   bool
	}{
		{"fresh", start.Add(10 * time.Minute), true, true},
		{"old backlog", start.Add(6 * time.Hour), true, false},
		{"resync", start.Add(10 * time.Minute), false, false},
	} {
		if got := combineBuckets(cfg, b, c.now, c.normal, true)[0].Live; got != c.want {
			t.Errorf("%s: live = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCombineEmptyBucketSkipped(t *testing.T) {
	start := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	if got := combineBuckets(config.Config{}, []contract.Bucket{{Start: start, Stations: []contract.Station{}}}, start, true, true); len(got) != 0 {
		t.Fatalf("got %d records for a bucket with no station", len(got))
	}
}

func v2ObsTopic() string {
	return contract.Scope{DeviceID: testDevice, UserID: 42}.Topic(contract.KindObs)
}

// TestV2UplinkPublishesOnV2Topics: a device with a user id publishes the
// combined record on V2/{user}/PWS/{device}/obs, and none on the v1 topic.
func TestV2UplinkPublishesOnV2Topics(t *testing.T) {
	b := newBroker(t)
	r := newRig(t, time.Date(2026, 11, 16, 14, 1, 0, 0, time.UTC))
	r.writeBuckets(time.Date(2026, 11, 16, 13, 45, 0, 0, time.UTC), 3)
	u := r.connected(b, func(o *Options) { o.UserID = 42 })

	if n, err := u.obsCycle(context.Background()); err != nil || n != 1 {
		t.Fatalf("obsCycle = %d, %v", n, err)
	}
	waitFor(t, 10*time.Second, "v2 obs", func() bool { return b.count(v2ObsTopic()) == 1 })
	if b.count(obsTopic()) != 0 {
		t.Fatal("a V2 device must not publish on the v1 obs topic")
	}
	var o contract.Obs2
	if err := contract.Unmarshal(b.messages(v2ObsTopic())[0].Payload, &o); err != nil {
		t.Fatal(err)
	}
	if o.V != 2 || len(o.Buckets) != 3 || o.Buckets[0].F.TempC == nil {
		t.Fatalf("unexpected v2 payload: %+v", o)
	}
	if !o.Buckets[0].Start.Before(o.Buckets[1].Start) {
		t.Error("records must be oldest first")
	}
	// The status message is on the V2 tree too.
	waitFor(t, 5*time.Second, "v2 status", func() bool {
		return b.count(contract.Scope{DeviceID: testDevice, UserID: 42}.Topic(contract.KindStatus)) > 0
	})
}

func TestV1UplinkUnchangedWithoutUserID(t *testing.T) {
	b := newBroker(t)
	r := newRig(t, time.Date(2026, 11, 16, 14, 1, 0, 0, time.UTC))
	r.writeBuckets(time.Date(2026, 11, 16, 13, 50, 0, 0, time.UTC), 2)
	u := r.connected(b, nil)
	if _, err := u.obsCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "v1 obs", func() bool { return b.count(obsTopic()) == 1 })
	if o := decodeObs(t, b.messages(obsTopic())[0].Payload); o.V != 0 || len(o.Buckets[0].Stations) != 1 {
		t.Fatalf("v1 shape changed: %+v", o)
	}
}

var _ = model.FieldTempC

// denyHook refuses topics under a prefix, as a broker whose V2 rules are not in
// place yet would: writes only, or subscriptions as well.
type denyHook struct {
	mqtt.HookBase
	prefix    string
	subscribe bool
}

func (h *denyHook) ID() string { return "deny-v2" }
func (h *denyHook) Provides(b byte) bool {
	return b == mqtt.OnACLCheck || b == mqtt.OnConnectAuthenticate
}
func (h *denyHook) OnConnectAuthenticate(*mqtt.Client, packets.Packet) bool { return true }
func (h *denyHook) OnACLCheck(_ *mqtt.Client, topic string, write bool) bool {
	if !strings.HasPrefix(topic, h.prefix) {
		return true
	}
	return !write && !h.subscribe
}

func newDenyingBroker(t *testing.T, subscribe bool) *testBroker {
	t.Helper()
	return newBrokerWith(t, &denyHook{prefix: "V2/", subscribe: subscribe})
}

// TestFallsBackToV1WhenV2PublishIsRefused: the broker refuses writes on the V2
// tree. The device switches to the v1 topics, drops what it had queued for V2
// and rebuilds those buckets in the v1 shape, so nothing is lost.
func TestFallsBackToV1WhenV2PublishIsRefused(t *testing.T) {
	b := newDenyingBroker(t, false)
	r := newRig(t, time.Date(2026, 11, 16, 14, 1, 0, 0, time.UTC))
	r.writeBuckets(time.Date(2026, 11, 16, 13, 45, 0, 0, time.UTC), 3)
	u := r.uplink(func(o *Options) { o.BrokerURL = b.url(); o.TLS = nil; o.UserID = 42 })
	// A V2 observation message is queued before the device ever connects.
	if n, err := u.obsCycle(context.Background()); err != nil || n != 1 {
		t.Fatalf("obsCycle = %d, %v", n, err)
	}
	r.run(u)

	waitFor(t, 15*time.Second, "fallback", func() bool { return u.v1Fallback.Load() })
	waitFor(t, 15*time.Second, "reconnected", func() bool { return u.connected.Load() })
	if _, err := u.obsCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "v1 obs", func() bool { return b.count(obsTopic()) >= 1 })
	if got := b.count(v2ObsTopic()); got != 0 {
		t.Fatalf("%d messages reached the refused V2 tree", got)
	}
	o := decodeObs(t, b.messages(obsTopic())[0].Payload)
	if o.V != 0 || len(o.Buckets) != 3 {
		t.Fatalf("the dropped V2 buckets must be rebuilt as v1: v=%d buckets=%d", o.V, len(o.Buckets))
	}
	if u.scope().V2() {
		t.Fatal("scope must be v1 after the fallback")
	}
	// A restart soon after starts on v1 without trying V2 again.
	again := r.uplink(func(o *Options) { o.UserID = 42 })
	if again.scope().V2() {
		t.Fatal("a recent refusal for the same user id must start on v1")
	}
	// A different owner is tried on V2.
	other := r.uplink(func(o *Options) { o.UserID = 43 })
	if !other.scope().V2() {
		t.Fatal("a new user id must try V2")
	}
}

// TestFallsBackToV1WhenTheCommandSubscriptionIsRefused covers the broker that
// refuses the V2 subscription too.
func TestFallsBackToV1WhenTheCommandSubscriptionIsRefused(t *testing.T) {
	b := newDenyingBroker(t, true)
	r := newRig(t, time.Date(2026, 11, 16, 14, 1, 0, 0, time.UTC))
	u := r.connected(b, func(o *Options) { o.UserID = 42 })
	waitFor(t, 15*time.Second, "fallback", func() bool { return u.v1Fallback.Load() })
	waitFor(t, 15*time.Second, "v1 status", func() bool {
		return b.count(contract.Scope{DeviceID: testDevice}.Topic(contract.KindStatus)) > 0 && u.connected.Load()
	})
}
