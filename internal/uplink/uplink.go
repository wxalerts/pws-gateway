package uplink

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/wxalerts/pws-gateway/internal/collector"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/model"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/store"
	"github.com/wxalerts/pws-gateway/internal/version"
)

// Limits from the contract's outbox and replay policy.
const (
	// BucketWidth is the aggregation bucket, and obs interval_s.
	BucketWidth = 5 * time.Minute
	// NormalBuckets is how many buckets one message carries in normal
	// operation (one 15 minute window).
	NormalBuckets = 3
	// MaxReplayBuckets is the most buckets in one message on a backlog.
	MaxReplayBuckets = 20
	// ReplayInterval is the minimum gap between outbox publishes.
	ReplayInterval = time.Second
	// TimeUnsyncedAfter is how long the clock may stay unsynced after start
	// before a time_unsynced event is published.
	TimeUnsyncedAfter = 10 * time.Minute

	// settle keeps the newest closed bucket out of a message until the
	// pipeline tick and the rollup flush have surely seen it.
	settle = time.Minute
	// firstHistory bounds the backfill of a device that never uplinked.
	firstHistory = 24 * time.Hour
	// maxResync bounds a resync range to the outbox retention.
	maxResync = 14 * 24 * time.Hour

	publishTimeout = 30 * time.Second
	checkEvery     = 30 * time.Second
	keepAlive      = 60
	maxCommandSize = contract.MaxMessageBytes
	pendingBatch   = 50
)

// Collector is what the uplink needs from the collector pipeline.
type Collector interface {
	// Readings returns recent readings of one station at or after since,
	// used for the raw passthrough and the signal block.
	Readings(key string, since time.Time) []model.Reading
	// Status reports the collector counters.
	Status() collector.Status
}

// Options configures an Uplink. Zero values take the defaults noted.
type Options struct {
	DeviceID string
	// BrokerURL is mqtts://host:port (or mqtt:// for tests). Not needed in
	// DryRun.
	BrokerURL   string
	Credentials Credentials
	// CAFile is an optional pinned CA bundle added to the system roots.
	CAFile string
	// TLS overrides the TLS configuration entirely (tests).
	TLS *tls.Config

	// CommandKey is the pinned command-signing public key. nil means every
	// command is ignored.
	CommandKey ed25519.PublicKey

	Config    *config.Store
	Store     *store.Store
	Platform  platform.Platform
	Collector Collector
	// SDRs lists the radios for tel. nil sends none.
	SDRs func() []contract.SDR
	// FlushRollups persists closed buckets before a cycle reads them.
	FlushRollups func(context.Context) error
	// UpdateState returns the updater's state for the retained update
	// message. nil reports idle.
	UpdateState func() contract.Update
	// UpdateCheck runs the update-check command. nil answers not_implemented.
	UpdateCheck func(ctx context.Context) (map[string]any, error)
	// DiagHandler runs an allow-listed diag action. nil answers not_implemented.
	DiagHandler func(ctx context.Context, body contract.DiagBody) (map[string]any, error)

	// Now is the clock. Default time.Now.
	Now    func() time.Time
	Logger *slog.Logger
	// DryRun builds and enqueues messages but never connects.
	DryRun bool

	// Test hooks and tuning. Zero values take the contract defaults.
	ReplayInterval time.Duration
	Prune          store.PrunePolicy
	Rand           func() float64
}

// Status is the live uplink state for the web layer.
type Status struct {
	Connected   bool
	Broker      string
	DeviceID    string
	LastPublish *time.Time
	DryRun      bool
}

// Uplink is the MQTT client and scheduler. Build it with New and start it
// with Run.
type Uplink struct {
	opts Options
	log  *slog.Logger
	now  func() time.Time

	connected atomic.Bool
	kick      chan struct{}
	cmds      chan queuedCommand

	mu          sync.Mutex // guards the fields below
	cm          *autopaho.ConnectionManager
	lastPublish time.Time
	baseCtx     context.Context

	buildMu sync.Mutex // serializes obs building and the uplink_state key

	gateMu         sync.Mutex // guards the time gate fields
	unsyncedSince  time.Time
	unsyncedEvent  bool
	wasSynced      bool
	overflowActive bool
	lastEmit       map[string]time.Time
	prevReadings   int64
	havePrev       bool

	replay *kvReplayCache
	// afterEnqueue is a test hook: a non-nil error stops obsCycle right after
	// a message is enqueued, as a crash would.
	afterEnqueue func() error
	warnedNoKey  atomic.Bool
}

type queuedCommand struct {
	topic   string
	payload []byte
}

// New validates the options and builds an Uplink. It starts nothing.
func New(opts Options) (*Uplink, error) {
	if opts.DeviceID == "" || opts.Store == nil || opts.Config == nil || opts.Platform == nil {
		return nil, errors.New("uplink: DeviceID, Store, Config and Platform are required")
	}
	if !opts.DryRun && opts.BrokerURL == "" {
		return nil, errors.New("uplink: BrokerURL is required unless DryRun is set")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.ReplayInterval <= 0 {
		opts.ReplayInterval = ReplayInterval
	}
	if opts.Rand == nil {
		opts.Rand = rand.Float64
	}
	u := &Uplink{
		opts:     opts,
		log:      opts.Logger.With("component", "uplink"),
		now:      opts.Now,
		kick:     make(chan struct{}, 1),
		cmds:     make(chan queuedCommand, 16),
		lastEmit: map[string]time.Time{},
		baseCtx:  context.Background(),
		replay:   &kvReplayCache{store: opts.Store, now: opts.Now},
	}
	u.unsyncedSince = u.now()
	return u, nil
}

// Status reports the connection state.
func (u *Uplink) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := Status{Connected: u.connected.Load(), Broker: u.opts.BrokerURL, DeviceID: u.opts.DeviceID, DryRun: u.opts.DryRun}
	if !u.lastPublish.IsZero() {
		t := u.lastPublish
		st.LastPublish = &t
	}
	return st
}

// UpdateMessage is the retained update payload: the updater's state, or idle
// when no updater is wired.
func (u *Uplink) UpdateMessage() contract.Update {
	v := contract.Version
	if u.opts.UpdateState != nil {
		m := u.opts.UpdateState()
		m.V, m.DeviceID = &v, u.opts.DeviceID
		return m
	}
	return contract.Update{
		V: &v, DeviceID: u.opts.DeviceID,
		Current: version.Version, State: contract.UpdateIdle,
		TS: u.now().UTC().Truncate(time.Second),
	}
}

// PublishUpdate publishes the current update state as the retained update
// message. While disconnected it does nothing: the state goes out with the
// retained messages when the connection comes up.
func (u *Uplink) PublishUpdate(ctx context.Context) {
	if !u.connected.Load() {
		return
	}
	u.mu.Lock()
	cm := u.cm
	u.mu.Unlock()
	if cm == nil {
		return
	}
	upd := u.UpdateMessage()
	data, err := contract.Marshal(&upd)
	if err != nil {
		u.log.Error("build update message", "err", err)
		return
	}
	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if err := u.publishVia(pctx, cm, contract.Topic(u.opts.DeviceID, contract.KindUpdate), data, true); err != nil {
		u.log.Warn("publish update failed", "err", err)
	}
}

func (u *Uplink) wake() {
	select {
	case u.kick <- struct{}{}:
	default:
	}
}

// Run connects (unless DryRun) and runs the scheduler, outbox pump and
// command worker until ctx ends. It returns after a clean disconnect.
func (u *Uplink) Run(ctx context.Context) error {
	u.mu.Lock()
	u.baseCtx = ctx
	u.mu.Unlock()
	if err := u.reconcile(ctx); err != nil {
		return fmt.Errorf("uplink: reconcile outbox: %w", err)
	}
	if u.opts.CommandKey == nil && !u.opts.DryRun {
		u.log.Info("no command-signing key pinned, every command will be ignored")
	}

	var wg sync.WaitGroup
	if !u.opts.DryRun {
		if err := u.connect(ctx); err != nil {
			return err
		}
		wg.Go(func() { u.pump(ctx) })
		wg.Go(func() { u.commandWorker(ctx) })
	}
	if u.opts.DryRun {
		// A bench run wants to see payload sizes now, not in up to 15 minutes.
		u.cycle(ctx)
	}
	u.schedule(ctx)
	wg.Wait()
	if !u.opts.DryRun {
		u.shutdown()
	}
	return nil
}

func (u *Uplink) connect(ctx context.Context) error {
	srv, err := url.Parse(u.opts.BrokerURL)
	if err != nil {
		return fmt.Errorf("uplink: broker url: %w", err)
	}
	tlsCfg := u.opts.TLS
	if tlsCfg == nil {
		if tlsCfg, err = TLSConfig(srv.Hostname(), u.opts.Credentials, u.opts.CAFile); err != nil {
			return err
		}
	}
	offline, err := contract.Marshal(statusPtr(contract.StatusOffline))
	if err != nil {
		return err
	}
	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{srv},
		TlsCfg:                        tlsCfg,
		KeepAlive:                     keepAlive,
		CleanStartOnInitialConnection: true,
		ConnectUsername:               u.opts.Credentials.Username,
		ConnectPassword:               []byte(u.opts.Credentials.Password),
		ReconnectBackoff:              func(attempt int) time.Duration { return Backoff(attempt, u.opts.Rand) },
		ConnectTimeout:                15 * time.Second,
		WillMessage: &paho.WillMessage{
			Topic: contract.Topic(u.opts.DeviceID, contract.KindStatus), Payload: offline, QoS: 1, Retain: true,
		},
		OnConnectionUp: u.onConnectionUp,
		OnConnectionDown: func() bool {
			u.connected.Store(false)
			u.log.Warn("broker connection lost")
			return true
		},
		OnConnectError: func(err error) { u.log.Warn("broker connect failed", "err", err) },
		ClientConfig: paho.ClientConfig{
			ClientID: u.opts.DeviceID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					u.onMessage(pr.Packet.Topic, pr.Packet.Payload)
					return true, nil
				},
			},
			OnClientError: func(err error) { u.log.Warn("mqtt client error", "err", err) },
		},
	}
	// The connection outlives ctx so shutdown can still publish the final
	// offline status; shutdown closes it with Disconnect.
	cm, err := autopaho.NewConnection(context.WithoutCancel(ctx), cfg)
	if err != nil {
		return fmt.Errorf("uplink: start connection: %w", err)
	}
	u.mu.Lock()
	u.cm = cm
	u.mu.Unlock()
	return nil
}

func statusPtr(s contract.Status) *contract.Status { return &s }

// onConnectionUp runs inside the client and must not block, so the retained
// messages and the subscription go from a goroutine.
func (u *Uplink) onConnectionUp(cm *autopaho.ConnectionManager, _ *paho.Connack) {
	go func() {
		ctx, cancel := context.WithTimeout(u.base(), publishTimeout)
		defer cancel()
		online, err := contract.Marshal(statusPtr(contract.StatusOnline))
		if err == nil {
			err = u.publishVia(ctx, cm, contract.Topic(u.opts.DeviceID, contract.KindStatus), online, true)
		}
		if err != nil {
			u.log.Warn("publish online status failed", "err", err)
			return
		}
		upd := u.UpdateMessage()
		if data, merr := contract.Marshal(&upd); merr != nil {
			u.log.Error("build update message", "err", merr)
		} else if perr := u.publishVia(ctx, cm, contract.Topic(u.opts.DeviceID, contract.KindUpdate), data, true); perr != nil {
			u.log.Warn("publish update failed", "err", perr)
		}
		_, err = cm.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{
			{Topic: contract.CommandFilter(u.opts.DeviceID), QoS: 1},
		}})
		if err != nil {
			u.log.Warn("subscribe to commands failed", "err", err)
			return
		}
		u.connected.Store(true)
		u.log.Info("connected to broker", "broker", u.opts.BrokerURL)
		u.wake()
	}()
}

func (u *Uplink) base() context.Context {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.baseCtx
}

func (u *Uplink) publishVia(ctx context.Context, cm *autopaho.ConnectionManager, topic string, payload []byte, retain bool) error {
	resp, err := cm.Publish(ctx, &paho.Publish{Topic: topic, Payload: payload, QoS: 1, Retain: retain})
	if err != nil {
		return err
	}
	if resp != nil && resp.ReasonCode >= 0x80 {
		return fmt.Errorf("broker refused publish to %s: reason 0x%02x", topic, resp.ReasonCode)
	}
	u.mu.Lock()
	u.lastPublish = u.now()
	u.mu.Unlock()
	return nil
}

// shutdown announces offline (a clean DISCONNECT does not fire the LWT) and
// closes the connection.
func (u *Uplink) shutdown() {
	u.mu.Lock()
	cm := u.cm
	u.mu.Unlock()
	if cm == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if u.connected.Load() {
		if off, err := contract.Marshal(statusPtr(contract.StatusOffline)); err == nil {
			if err := u.publishVia(ctx, cm, contract.Topic(u.opts.DeviceID, contract.KindStatus), off, true); err != nil {
				u.log.Debug("publish offline status failed", "err", err)
			}
		}
	}
	if err := cm.Disconnect(ctx); err != nil {
		u.log.Debug("disconnect", "err", err)
	}
	u.connected.Store(false)
}

// schedule runs the 15 minute cycle at this device's offset and the 30 second
// housekeeping tick until ctx ends.
func (u *Uplink) schedule(ctx context.Context) {
	tick := time.NewTicker(checkEvery)
	defer tick.Stop()
	next := NextSend(u.opts.DeviceID, u.now())
	for {
		timer := time.NewTimer(max(next.Sub(u.now()), 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-tick.C:
			timer.Stop()
			u.checkTimeGate(ctx)
		case <-timer.C:
			u.cycle(ctx)
			next = NextSend(u.opts.DeviceID, u.now())
		}
	}
}

// cycle is one 15 minute round: obs, then tel, then prune.
func (u *Uplink) cycle(ctx context.Context) {
	if _, err := u.obsCycle(ctx); err != nil {
		u.log.Error("obs cycle failed", "err", err)
	}
	if err := u.telCycle(ctx); err != nil {
		u.log.Error("tel cycle failed", "err", err)
	}
	u.prune(ctx)
}

// enqueue stores a message in the outbox and wakes the pump. The outbox is
// the only way a message reaches the broker.
func (u *Uplink) enqueue(ctx context.Context, topic string, payload []byte) error {
	seq, err := u.opts.Store.Enqueue(ctx, topic, payload, u.now())
	if err != nil {
		return err
	}
	if u.opts.DryRun {
		u.log.Info("dry run: message enqueued", "topic", topic, "bytes", len(payload), "outbox_seq", seq)
	}
	return nil
}

// publishMessage marshals, validates and enqueues a non-obs message.
func (u *Uplink) publishMessage(ctx context.Context, topic string, m contract.Message) error {
	data, err := contract.Marshal(m)
	if err != nil {
		return err
	}
	if err := u.enqueue(ctx, topic, data); err != nil {
		return err
	}
	u.wake()
	return nil
}

// Emit queues an event message. detail may be nil.
func (u *Uplink) Emit(ctx context.Context, typ string, detail map[string]any) {
	raw := json.RawMessage(`{}`)
	if len(detail) > 0 {
		b, err := json.Marshal(detail)
		if err != nil {
			u.log.Error("event detail", "type", typ, "err", err)
			return
		}
		raw = b
	}
	ev := &contract.Event{V: contract.Version, DeviceID: u.opts.DeviceID, TS: u.now().UTC().Truncate(time.Second), Type: typ, Detail: raw}
	if err := u.publishMessage(ctx, contract.Topic(u.opts.DeviceID, contract.KindEvent), ev); err != nil {
		u.log.Error("queue event failed", "type", typ, "err", err)
	}
}

// EmitThrottled is Emit with a minimum gap per key, for events a failing
// subsystem would repeat every few seconds (sdr_missing).
func (u *Uplink) EmitThrottled(ctx context.Context, typ, key string, every time.Duration, detail map[string]any) {
	id := typ + "/" + key
	u.gateMu.Lock()
	last, ok := u.lastEmit[id]
	if ok && u.now().Sub(last) < every {
		u.gateMu.Unlock()
		return
	}
	u.lastEmit[id] = u.now()
	u.gateMu.Unlock()
	u.Emit(ctx, typ, detail)
}

// HandleCollectorEvent turns pipeline events into contract events. Only
// stations the owner selected are reported.
func (u *Uplink) HandleCollectorEvent(e collector.Event) {
	if e.Station == nil {
		return
	}
	var typ string
	switch e.Type {
	case collector.EventSensorFound:
		typ = contract.EventSensorFound
	case collector.EventSensorLost:
		typ = contract.EventSensorLost
	default:
		return
	}
	if !u.opts.Config.Get().Stations[e.Station.Key].Selected {
		return
	}
	ctx, cancel := context.WithTimeout(u.base(), 10*time.Second)
	defer cancel()
	u.Emit(ctx, typ, map[string]any{"key": e.Station.Key})
}

// prune applies the outbox policy and publishes one outbox_overflow event per
// overflow episode: the first prune that drops anything starts an episode,
// and it ends at the first prune that drops nothing and is back under the cap.
func (u *Uplink) prune(ctx context.Context) {
	res, err := u.opts.Store.Prune(ctx, u.now(), u.opts.Prune)
	if err != nil {
		u.log.Error("outbox prune failed", "err", err)
		return
	}
	dropped := res.DroppedByAge + res.DroppedEvents + res.DroppedObs
	u.gateMu.Lock()
	start := dropped > 0 && !u.overflowActive
	switch {
	case dropped > 0:
		u.overflowActive = true
	case !res.OverCap:
		u.overflowActive = false
	}
	u.gateMu.Unlock()
	if start {
		u.log.Warn("outbox overflow, oldest entries dropped", "events", res.DroppedEvents, "obs", res.DroppedObs, "aged", res.DroppedByAge)
		u.Emit(ctx, contract.EventOutboxOverflow, map[string]any{
			"dropped": dropped, "dropped_obs": res.DroppedObs,
		})
	}
}
