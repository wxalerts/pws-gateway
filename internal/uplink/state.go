package uplink

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/store"
)

const (
	stateKey  = "uplink_state"
	replayKey = "uplink_replay"
	// replayCap bounds the persisted command id cache.
	replayCap = 1024
)

// state is the obs cursor, kept in the store kv so it survives restarts.
type state struct {
	// Seq is the last obs seq handed out. The next message uses Seq+1.
	Seq uint64 `json:"seq"`
	// BuiltThrough is the end of the last bucket that went into an obs
	// message. The next cycle starts here.
	BuiltThrough time.Time `json:"built_through"`
}

func (u *Uplink) loadState(ctx context.Context) (state, error) {
	var st state
	v, ok, err := u.opts.Store.Get(ctx, stateKey)
	if err != nil || !ok {
		return st, err
	}
	if err := json.Unmarshal([]byte(v), &st); err != nil {
		return state{}, fmt.Errorf("uplink: corrupt %s: %w", stateKey, err)
	}
	return st, nil
}

func (u *Uplink) saveState(ctx context.Context, st state) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return u.opts.Store.Set(ctx, stateKey, string(b))
}

// reconcile repairs the cursor after a crash between "message enqueued" and
// "cursor saved": any obs message still in the outbox with a seq beyond the
// cursor is adopted, so the next cycle neither reuses its seq nor rebuilds its
// buckets. Messages already delivered are not in the outbox and need no
// repair, because the cursor is saved right after the enqueue.
func (u *Uplink) reconcile(ctx context.Context) error {
	u.buildMu.Lock()
	defer u.buildMu.Unlock()
	st, err := u.loadState(ctx)
	if err != nil {
		return err
	}
	items, err := u.opts.Store.Pending(ctx, 100000)
	if err != nil {
		return err
	}
	changed := false
	for _, it := range items {
		if it.Topic != contract.Topic(u.opts.DeviceID, contract.KindObs) {
			continue
		}
		var m contract.Obs
		if err := json.Unmarshal(it.Payload, &m); err != nil || len(m.Buckets) == 0 {
			continue
		}
		if m.Seq > st.Seq {
			st.Seq = m.Seq
			changed = true
		}
		end := m.Buckets[len(m.Buckets)-1].Start.Add(time.Duration(m.IntervalS) * time.Second)
		if end.After(st.BuiltThrough) {
			st.BuiltThrough = end
			changed = true
		}
	}
	if changed {
		u.log.Info("adopted unsent obs messages after restart", "seq", st.Seq)
		return u.saveState(ctx, st)
	}
	return nil
}

// kvReplayCache is a contract.ReplayCache persisted in the store kv, so a
// captured command cannot be replayed by restarting the device within its
// validity window.
type kvReplayCache struct {
	store *store.Store
	now   func() time.Time
	mu    sync.Mutex
}

// Seen implements contract.ReplayCache.
func (c *kvReplayCache) Seen(id string, exp time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx := context.Background()
	ids := map[string]time.Time{}
	v, ok, err := c.store.Get(ctx, replayKey)
	if err != nil {
		return false, err
	}
	if ok {
		if err := json.Unmarshal([]byte(v), &ids); err != nil {
			ids = map[string]time.Time{} // a corrupt cache must not lock out commands forever
		}
	}
	now := c.now()
	for k, e := range ids {
		if !e.After(now) {
			delete(ids, k)
		}
	}
	if _, dup := ids[id]; dup {
		return true, nil
	}
	if len(ids) >= replayCap {
		// Refuse rather than evict: evicting would reopen a replay window.
		return false, fmt.Errorf("replay cache full (%d ids)", len(ids))
	}
	ids[id] = exp
	b, err := json.Marshal(ids)
	if err != nil {
		return false, err
	}
	return false, c.store.Set(ctx, replayKey, string(b))
}
