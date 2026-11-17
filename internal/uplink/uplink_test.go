package uplink

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/collector"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/model"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/platform/mock"
)

func TestConnectPublishesOnlineAndSetsLWT(t *testing.T) {
	b := newBroker(t)
	r := newRig(t, t0)
	u := r.connected(b, nil)
	statusTopic := contract.Topic(testDevice, contract.KindStatus)

	waitFor(t, 5*time.Second, "retained online", func() bool { return len(b.retained(statusTopic)) == 1 })
	ret := b.retained(statusTopic)[0]
	if string(ret.Payload) != `"online"` || !ret.FixedHeader.Retain {
		t.Fatalf("retained status = %q retain=%v", ret.Payload, ret.FixedHeader.Retain)
	}
	if err := contract.Validate(contract.KindStatus, ret.Payload); err != nil {
		t.Fatal(err)
	}
	// The retained update message is idle, from the version package.
	waitFor(t, 5*time.Second, "retained update", func() bool {
		return len(b.retained(contract.Topic(testDevice, contract.KindUpdate))) == 1
	})
	upd := b.retained(contract.Topic(testDevice, contract.KindUpdate))[0].Payload
	if err := contract.Validate(contract.KindUpdate, upd); err != nil {
		t.Fatalf("update: %v\n%s", err, upd)
	}
	if !strings.Contains(string(upd), `"state":"idle"`) {
		t.Fatalf("update is not idle: %s", upd)
	}

	// Client id is the device id and the only subscription is cmd/#.
	cl, ok := b.srv.Clients.Get(testDevice)
	if !ok {
		t.Fatal("no client with the device id")
	}
	subs := cl.State.Subscriptions.GetAll()
	if len(subs) != 1 {
		t.Fatalf("subscriptions: %v", subs)
	}
	if _, ok := subs[contract.CommandFilter(testDevice)]; !ok {
		t.Fatalf("not subscribed to cmd/#: %v", subs)
	}
	if st := u.Status(); !st.Connected || st.DeviceID != testDevice || st.Broker != b.url() {
		t.Fatalf("status: %+v", st)
	}

	// An unclean disconnect makes the broker publish the retained LWT.
	cl.Stop(errors.New("test: cut the line"))
	// The client reconnects at once and overwrites the retained state, so the
	// LWT is observed in the recorded stream, not in the retained store.
	waitFor(t, 5*time.Second, "retained offline publish from the LWT", func() bool {
		for _, m := range b.messages(statusTopic) {
			if string(m.Payload) == `"offline"` && m.Retain {
				return true
			}
		}
		return false
	})
	// And the client comes back by itself and republishes online.
	waitFor(t, 20*time.Second, "reconnect with online again", func() bool {
		m := b.retained(statusTopic)
		return len(m) == 1 && string(m[0].Payload) == `"online"`
	})
}

func TestObsFromRealCaptureReachesSubscriber(t *testing.T) {
	b := newBroker(t)
	r := newRig(t, t0)
	p := r.replayCapture()
	// Three 5 minute boundaries (16:55, 17:00, 17:05) have passed.
	r.clock.Set(time.Date(2026, 11, 16, 17, 6, 30, 0, time.UTC))
	u := r.connected(b, func(o *Options) { o.Collector = p })

	n, err := u.obsCycle(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("obsCycle = %d, %v; want one message", n, err)
	}
	waitFor(t, 5*time.Second, "obs at the subscriber", func() bool { return b.count(obsTopic()) == 1 })

	o := decodeObs(t, b.messages(obsTopic())[0].Payload)
	if o.DeviceID != testDevice || o.Seq != 1 || o.IntervalS != 300 || o.V != contract.Version {
		t.Fatalf("header: %+v", o)
	}
	if !o.Sent.Equal(r.clock.Now().Truncate(time.Second)) {
		t.Fatalf("sent = %v", o.Sent)
	}
	var starts []string
	for _, bk := range o.Buckets {
		starts = append(starts, bk.Start.Format("15:04"))
	}
	if !slices.Equal(starts, []string{"16:50", "16:55", "17:00"}) {
		t.Fatalf("bucket starts = %v, want the three closed buckets", starts)
	}
	for _, bk := range o.Buckets {
		if len(bk.Stations) != 1 || bk.Stations[0].Key != stationKey {
			t.Fatalf("only the selected station may appear: %+v", bk.Stations)
		}
		st := bk.Stations[0]
		if st.Meta == nil || *st.Meta.AltM != 31 || *st.Meta.WindHeightM != 4.5 {
			t.Fatalf("meta: %+v", st.Meta)
		}
		if st.Sig == nil || st.Sig.RSSIdB == nil || st.Sig.SNRdB == nil {
			t.Fatalf("sig missing: %+v", st.Sig)
		}
		if st.F.TempC == nil || st.F.TempC.Avg == nil {
			t.Fatalf("temp missing: %+v", st.F)
		}
		if st.N < 1 {
			t.Fatalf("n = %d", st.N)
		}
	}
	// Raw passthrough: the 5n1 reports fields with no canonical mapping.
	var rawSeen bool
	for _, bk := range o.Buckets {
		rawSeen = rawSeen || len(bk.Stations[0].Raw) > 2
	}
	if !rawSeen {
		t.Fatal("no raw passthrough in any bucket")
	}
	waitFor(t, 5*time.Second, "outbox drained after PUBACK", func() bool { return len(r.outbox(obsTopic())) == 0 })
	if st := u.Status(); st.LastPublish == nil {
		t.Fatal("last publish not recorded")
	}

	// A second cycle with nothing new sends nothing.
	if n, err := u.obsCycle(context.Background()); err != nil || n != 0 {
		t.Fatalf("second cycle = %d, %v", n, err)
	}
}

// TestUnselectedStationsNeverSent: the owner's selection is the filter.
func TestUnselectedStationsNeverSent(t *testing.T) {
	r := newRig(t, time.Date(2026, 11, 16, 17, 6, 30, 0, time.UTC))
	r.writeBuckets(time.Date(2026, 11, 16, 16, 50, 0, 0, time.UTC), 3)
	if _, err := r.cfg.Update(func(c *config.Config) error {
		s := c.Stations[stationKey]
		s.Selected = false
		c.Stations[stationKey] = s
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	u := r.uplink(nil)
	if n, err := u.obsCycle(context.Background()); err != nil || n != 0 {
		t.Fatalf("obsCycle = %d, %v", n, err)
	}
	if got := r.outbox(obsTopic()); len(got) != 0 {
		t.Fatalf("outbox has %d obs", len(got))
	}
}

// TestOutboxFirstSurvivesCrash is the outbox-first rule: the message is in the
// SQLite outbox before anything is published, so a crash after the enqueue
// (the hook stops the cycle at that exact point) loses nothing. After a
// restart the same message, with the same (device_id, seq), is delivered once
// and the buckets are not built a second time.
func TestOutboxFirstSurvivesCrash(t *testing.T) {
	b := newBroker(t)
	r := newRig(t, time.Date(2026, 11, 16, 17, 6, 30, 0, time.UTC))
	r.writeBuckets(time.Date(2026, 11, 16, 16, 50, 0, 0, time.UTC), 3)

	crashing := r.uplink(nil) // never connected: nothing can have been published
	crashing.afterEnqueue = func() error { return errors.New("simulated crash") }
	if _, err := crashing.obsCycle(context.Background()); err == nil {
		t.Fatal("hook did not stop the cycle")
	}
	if b.count(obsTopic()) != 0 {
		t.Fatal("message published before the crash")
	}
	queued := r.outbox(obsTopic())
	if len(queued) != 1 {
		t.Fatalf("outbox holds %d obs messages after the crash, want 1", len(queued))
	}
	before := decodeObs(t, queued[0].Payload)
	st, err := crashing.loadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Seq != 0 {
		t.Fatalf("state advanced to seq %d although the crash came first", st.Seq)
	}

	// Restart: a new Uplink on the same database.
	restarted := r.connected(b, nil)
	waitFor(t, 5*time.Second, "queued obs delivered", func() bool { return b.count(obsTopic()) == 1 })
	after := decodeObs(t, b.messages(obsTopic())[0].Payload)
	if after.Seq != before.Seq || after.DeviceID != before.DeviceID {
		t.Fatalf("dedupe key changed: (%s,%d) -> (%s,%d)", before.DeviceID, before.Seq, after.DeviceID, after.Seq)
	}
	if len(after.Buckets) != 3 {
		t.Fatalf("buckets = %d", len(after.Buckets))
	}
	waitFor(t, 5*time.Second, "outbox empty", func() bool { return len(r.outbox(obsTopic())) == 0 })

	// The cursor adopted the queued message: nothing is rebuilt or resent.
	if n, err := restarted.obsCycle(context.Background()); err != nil || n != 0 {
		t.Fatalf("cycle after restart = %d, %v; want 0", n, err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := b.count(obsTopic()); got != 1 {
		t.Fatalf("obs delivered %d times, want exactly once", got)
	}
	// The next real message continues the sequence.
	r.writeBuckets(time.Date(2026, 11, 16, 17, 5, 0, 0, time.UTC), 3)
	r.clock.Set(time.Date(2026, 11, 16, 17, 21, 30, 0, time.UTC))
	if n, err := restarted.obsCycle(context.Background()); err != nil || n != 1 {
		t.Fatalf("next cycle = %d, %v", n, err)
	}
	waitFor(t, 5*time.Second, "second obs", func() bool { return b.count(obsTopic()) == 2 })
	if seq := decodeObs(t, b.messages(obsTopic())[1].Payload).Seq; seq != before.Seq+1 {
		t.Fatalf("second seq = %d, want %d", seq, before.Seq+1)
	}
}

// TestOutageThenReplay covers a 2 hour broker outage: the cycles keep
// enqueueing, the outbox holds every bucket, and on reconnect the messages go
// out in strictly increasing seq order.
func TestOutageThenReplay(t *testing.T) {
	b := newBroker(t)
	r := newRig(t, time.Date(2026, 11, 16, 12, 6, 30, 0, time.UTC))
	first := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	r.writeBuckets(first, 24) // 2 hours of 5 minute buckets
	u := r.uplink(nil)        // offline: never connected during the outage

	for range 8 { // eight 15 minute cycles
		r.clock.Advance(15 * time.Minute)
		if _, err := u.obsCycle(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	items := r.outbox(obsTopic())
	var starts []time.Time
	var lastSeq uint64
	for _, it := range items {
		o := decodeObs(t, it.Payload)
		if o.Seq <= lastSeq {
			t.Fatalf("seq not strictly increasing: %d after %d", o.Seq, lastSeq)
		}
		lastSeq = o.Seq
		if len(o.Buckets) > MaxReplayBuckets {
			t.Fatalf("%d buckets in one message", len(o.Buckets))
		}
		for _, bk := range o.Buckets {
			starts = append(starts, bk.Start)
		}
	}
	if len(starts) != 24 {
		t.Fatalf("outbox holds %d buckets, want all 24", len(starts))
	}
	for i, s := range starts {
		if want := first.Add(time.Duration(i) * BucketWidth); !s.Equal(want) {
			t.Fatalf("bucket %d starts %v want %v", i, s, want)
		}
	}

	r.connected(b, nil)
	waitFor(t, 10*time.Second, "replay", func() bool { return b.count(obsTopic()) == len(items) })
	var prev uint64
	for _, m := range b.messages(obsTopic()) {
		if o := decodeObs(t, m.Payload); o.Seq <= prev {
			t.Fatalf("delivered out of order: %d after %d", o.Seq, prev)
		} else {
			prev = o.Seq
		}
	}
}

// TestBacklogReplayLimits: a backlog goes out at most 20 buckets per message
// and at most one message per second.
func TestBacklogReplayLimits(t *testing.T) {
	b := newBroker(t)
	r := newRig(t, time.Date(2026, 11, 16, 14, 1, 0, 0, time.UTC))
	r.writeBuckets(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC), 45)
	u := r.connected(b, func(o *Options) { o.ReplayInterval = 0 }) // 0 selects the 1 s default

	n, err := u.obsCycle(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("obsCycle = %d, %v; want 3 messages (20+20+5)", n, err)
	}
	waitFor(t, 15*time.Second, "three obs", func() bool { return b.count(obsTopic()) == 3 })
	msgs := b.messages(obsTopic())
	var sizes []int
	for i, m := range msgs {
		o := decodeObs(t, m.Payload)
		sizes = append(sizes, len(o.Buckets))
		if o.Seq != uint64(i+1) {
			t.Fatalf("message %d has seq %d", i, o.Seq)
		}
		if i > 0 {
			if gap := m.At.Sub(msgs[i-1].At); gap < 900*time.Millisecond {
				t.Fatalf("messages %d and %d arrived %v apart, want at least 1 s", i-1, i, gap)
			}
		}
	}
	if !slices.Equal(sizes, []int{20, 20, 5}) {
		t.Fatalf("buckets per message = %v", sizes)
	}
}

// TestSplitAt64KB: a backlog that does not fit one message is split at bucket
// boundaries, every message under 64 KB, and no bucket is lost or reordered.
func TestSplitAt64KB(t *testing.T) {
	r := newRig(t, time.Date(2026, 11, 16, 14, 1, 0, 0, time.UTC))
	first := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	r.writeBuckets(first, 20)
	u := r.uplink(func(o *Options) { o.Collector = bigRaw{} })

	n, err := u.obsCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Fatalf("20 buckets with 4 KB of raw each went out in %d message", n)
	}
	var total int
	var next time.Time
	for _, it := range r.outbox(obsTopic()) {
		if len(it.Payload) >= contract.MaxMessageBytes {
			t.Fatalf("payload of %d bytes is not under 64 KB", len(it.Payload))
		}
		o := decodeObs(t, it.Payload)
		for _, bk := range o.Buckets {
			if !next.IsZero() && !bk.Start.Equal(next) {
				t.Fatalf("bucket %v follows %v", bk.Start, next)
			}
			next = bk.Start.Add(BucketWidth)
			total++
		}
	}
	if total != 20 {
		t.Fatalf("%d buckets across messages, want 20", total)
	}
}

func TestSingleOversizedBucketDropsRaw(t *testing.T) {
	r := newRig(t, time.Date(2026, 11, 16, 14, 1, 0, 0, time.UTC))
	r.writeBuckets(time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), 1)
	u := r.uplink(func(o *Options) { o.Collector = bigRaw{kb: 80} })
	if n, err := u.obsCycle(context.Background()); err != nil || n != 1 {
		t.Fatalf("obsCycle = %d, %v", n, err)
	}
	o := decodeObs(t, r.outbox(obsTopic())[0].Payload)
	if o.Buckets[0].Stations[0].Raw != nil {
		t.Fatal("raw kept in a message that cannot fit")
	}
}

func TestTimeGateHoldsObsUntilSynced(t *testing.T) {
	r := newRig(t, time.Date(2026, 11, 16, 14, 1, 0, 0, time.UTC))
	r.writeBuckets(time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC), 6)
	r.plat.Set(func(p *mock.Platform) { p.TimeSyncValue.Synced = false })
	u := r.uplink(nil)
	ctx := context.Background()
	eventTopic := contract.Topic(testDevice, contract.KindEvent)

	for range 3 {
		if n, err := u.obsCycle(ctx); err != nil || n != 0 {
			t.Fatalf("obs published while unsynced: %d, %v", n, err)
		}
		r.clock.Advance(4 * time.Minute)
	}
	if got := r.outbox(eventTopic); len(got) != 0 {
		t.Fatalf("event before the 10 minute mark: %d", len(got))
	}
	// Past 10 minutes since start and still unsynced: one event, once.
	r.clock.Advance(time.Minute)
	for range 3 {
		u.checkTimeGate(ctx)
	}
	evs := r.outbox(eventTopic)
	if len(evs) != 1 || !strings.Contains(string(evs[0].Payload), `"type":"time_unsynced"`) {
		t.Fatalf("want one time_unsynced event, got %d", len(evs))
	}
	if err := contract.Validate(contract.KindEvent, evs[0].Payload); err != nil {
		t.Fatal(err)
	}
	if len(r.outbox(obsTopic())) != 0 {
		t.Fatal("obs queued while unsynced")
	}

	// After sync the buffered buckets go out.
	r.plat.Set(func(p *mock.Platform) { p.TimeSyncValue.Synced = true })
	n, err := u.obsCycle(ctx)
	if err != nil || n == 0 {
		t.Fatalf("after sync: %d, %v", n, err)
	}
	buckets := 0
	for _, it := range r.outbox(obsTopic()) {
		buckets += len(decodeObs(t, it.Payload).Buckets)
	}
	if buckets != 6 {
		t.Fatalf("buckets after sync = %d, want 6", buckets)
	}
}

func TestOverflowEventOncePerEpisode(t *testing.T) {
	r := newRig(t, t0)
	u := r.uplink(func(o *Options) { o.Prune.MaxBytes = 3000 })
	ctx := context.Background()
	eventTopic := contract.Topic(testDevice, contract.KindEvent)
	pad := strings.Repeat("x", 700)
	// Old, droppable entries: five tel-like messages created two days ago.
	for range 5 {
		if _, err := r.store.Enqueue(ctx, contract.Topic(testDevice, contract.KindTel), []byte(pad), t0.Add(-48*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	u.prune(ctx)
	u.prune(ctx)
	u.prune(ctx)
	count := func() int {
		n := 0
		for _, it := range r.outbox(eventTopic) {
			if strings.Contains(string(it.Payload), "outbox_overflow") {
				n++
			}
		}
		return n
	}
	if got := count(); got != 1 {
		t.Fatalf("outbox_overflow events = %d, want 1 for one episode", got)
	}
	// Episode over (nothing dropped, under the cap), then a new overflow.
	for range 6 {
		if _, err := r.store.Enqueue(ctx, contract.Topic(testDevice, contract.KindTel), []byte(pad), t0.Add(-48*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	u.prune(ctx)
	if got := count(); got != 2 {
		t.Fatalf("second episode events = %d, want 2", got)
	}
}

func TestDryRunBuildsButNeverConnects(t *testing.T) {
	r := newRig(t, time.Date(2026, 11, 16, 14, 1, 0, 0, time.UTC))
	r.writeBuckets(time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC), 3)
	u := r.uplink(func(o *Options) { o.DryRun = true; o.BrokerURL = "" })
	if n, err := u.obsCycle(context.Background()); err != nil || n != 1 {
		t.Fatalf("obsCycle = %d, %v", n, err)
	}
	if err := u.telCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := u.Status(); st.Connected || !st.DryRun {
		t.Fatalf("status: %+v", st)
	}
	if len(r.outbox(obsTopic())) != 1 || len(r.outbox(contract.Topic(testDevice, contract.KindTel))) != 1 {
		t.Fatal("dry run did not enqueue obs and tel")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := u.Run(ctx); err != nil {
		t.Fatalf("dry run Run: %v", err)
	}
	if u.cm != nil {
		t.Fatal("dry run created a connection")
	}
}

func TestTelValidatesAndCarriesPlatformData(t *testing.T) {
	r := newRig(t, t0)
	one := 433.92
	dec := 258
	u := r.uplink(func(o *Options) {
		o.SDRs = func() []contract.SDR {
			return []contract.SDR{{RadioID: "r_01", Driver: contract.DriverRTL433, BandMHz: &one, Decoders: &dec}}
		}
		o.Collector = counter{n: 10}
	})
	ctx := context.Background()
	if err := u.telCycle(ctx); err != nil { // first tel: no packet baseline
		t.Fatal(err)
	}
	u.opts.Collector = counter{n: 52}
	tel := u.buildTel(ctx)
	if tel.SDR[0].Packets15m == nil || *tel.SDR[0].Packets15m != 42 {
		t.Fatalf("packets_15m = %v", tel.SDR[0].Packets15m)
	}
	if tel.Net.Link != "ethernet" || !tel.Time.Synced || tel.Time.Src != "ntp" || tel.UptimeS != 86400 {
		t.Fatalf("tel: %+v", tel)
	}
	if _, err := contract.Marshal(tel); err != nil {
		t.Fatal(err)
	}
	items := r.outbox(contract.Topic(testDevice, contract.KindTel))
	if len(items) != 1 {
		t.Fatalf("tel in outbox: %d", len(items))
	}
	if err := contract.Validate(contract.KindTel, items[0].Payload); err != nil {
		t.Fatal(err)
	}
}

func TestLinkFromInterface(t *testing.T) {
	for name, want := range map[string]string{
		"eth0": "ethernet", "end0": "ethernet", "enp3s0": "ethernet",
		"wlan0": "wifi", "wlp2s0": "wifi", "wwan0": "lte", "ppp0": "lte", "usb0": "lte",
		"tun0": "unknown", "": "unknown",
	} {
		if got := LinkFromInterface(name); got != want {
			t.Errorf("%q = %q, want %q", name, got, want)
		}
	}
}

func TestCredentialsNeverLeak(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cred.json"
	if err := writeFile(path, `{"username":"d_8f3a","password":"s3cret-pw"}`); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%v %+v %#v", c, c, c), fmt.Sprint(c.LogValue())} {
		if strings.Contains(s, "s3cret-pw") {
			t.Fatalf("password leaked: %s", s)
		}
	}
	for _, bad := range []string{`{"username":"a"}`, `{"username":"a","password":"b","client_cert_file":"x"}`, `{"username":"a","password":"b","extra":1}`, `nope s3cret`} {
		if err := writeFile(path, bad); err != nil {
			t.Fatal(err)
		}
		_, err := LoadCredentials(path)
		if !errors.Is(err, ErrCredentials) {
			t.Fatalf("%s: %v", bad, err)
		}
		if strings.Contains(err.Error(), "s3cret") {
			t.Fatalf("error leaks content: %v", err)
		}
	}
}

// bigRaw is a Collector whose readings carry a large raw passthrough.
type bigRaw struct{ kb int }

func (b bigRaw) Readings(_ string, since time.Time) []model.Reading {
	kb := b.kb
	if kb == 0 {
		kb = 4
	}
	var out []model.Reading
	for i := range 24 {
		at := since.Truncate(BucketWidth).Add(time.Duration(i)*BucketWidth + time.Minute)
		out = append(out, model.Reading{Time: at, Raw: map[string]any{"blob": strings.Repeat("y", kb*1024)}})
	}
	return out
}

func (bigRaw) Status() collector.Status { return collector.Status{} }

type counter struct{ n int64 }

func (counter) Readings(string, time.Time) []model.Reading { return nil }
func (c counter) Status() collector.Status                 { return collector.Status{Readings: c.n} }

func TestTelModemAndGPSPresentOnlyWhenAttached(t *testing.T) {
	r := newRig(t, t0)
	u := r.uplink(nil)
	ctx := context.Background()

	tel := u.buildTel(ctx)
	if tel.Modem != nil || tel.GPS != nil {
		t.Fatalf("no hardware: modem=%v gps=%v", tel.Modem, tel.GPS)
	}
	raw, err := contract.Marshal(tel)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"modem"`) || strings.Contains(string(raw), `"gps"`) {
		t.Fatalf("tel has modem or gps keys with no hardware: %s", raw)
	}

	rssi, lat, lon, alt := -83.0, 30.6321, -87.0398, 31.4
	r.plat.Set(func(p *mock.Platform) {
		p.ModemValue = &platform.ModemInfo{Carrier: "AT&T", MCCMNC: "310410", Reg: "home", Tech: "lte", RSSIdBm: &rssi}
		p.GPSValue = &platform.GPSInfo{Fix: 3, Sats: 9, Lat: &lat, Lon: &lon, AltM: &alt}
		p.TimeSyncValue.Ref = "gps"
	})
	tel = u.buildTel(ctx)
	if tel.Modem == nil || tel.Modem.Carrier != "AT&T" || tel.Modem.MCCMNC != "310410" || tel.Modem.Reg != "home" ||
		tel.Modem.Tech != "lte" || tel.Modem.RSSIdBm == nil || *tel.Modem.RSSIdBm != -83 {
		t.Fatalf("modem = %+v", tel.Modem)
	}
	if tel.GPS == nil || *tel.GPS.Fix != 3 || *tel.GPS.Sats != 9 || *tel.GPS.Lat != lat || *tel.GPS.Lon != lon || *tel.GPS.AltM != alt {
		t.Fatalf("gps = %+v", tel.GPS)
	}
	if tel.Time.Src != "gps" || !tel.Time.Synced {
		t.Fatalf("time = %+v", tel.Time)
	}
	raw, err = contract.Marshal(tel) // validates against tel.schema.json
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Validate(contract.KindTel, raw); err != nil {
		t.Fatal(err)
	}
}

func TestTelTimeSourceFromRTCWhenUnsynced(t *testing.T) {
	r := newRig(t, t0)
	u := r.uplink(nil)
	ctx := context.Background()
	r.plat.Set(func(p *mock.Platform) {
		p.TimeSyncValue.Synced = false
		p.RTCValue = &platform.RTCInfo{Time: t0}
	})
	if tel := u.buildTel(ctx); tel.Time.Src != "rtc" || tel.Time.Synced {
		t.Fatalf("healthy RTC, unsynced: %+v", tel.Time)
	}
	r.plat.Set(func(p *mock.Platform) { p.RTCValue = &platform.RTCInfo{Time: t0, OscillatorStopped: true} })
	if tel := u.buildTel(ctx); tel.Time.Src != "none" || tel.Time.Synced {
		t.Fatalf("stopped oscillator must not be a source: %+v", tel.Time)
	}
}

func TestTelLinkIsLTEOnWWAN(t *testing.T) {
	r := newRig(t, t0)
	u := r.uplink(nil)
	r.plat.Set(func(p *mock.Platform) { p.RouteIface = "wwan0" })
	if tel := u.buildTel(context.Background()); tel.Net.Link != "lte" {
		t.Fatalf("link = %q", tel.Net.Link)
	}
}
