package uplink

import (
	"context"
	"errors"
	"time"
)

// pump publishes the outbox, oldest first, at no more than one message per
// ReplayInterval, and deletes each entry only after the broker answers
// PUBACK. It wakes on a new enqueue, on connection up and every 30 seconds
// (to retry after a failure).
func (u *Uplink) pump(ctx context.Context) {
	retry := time.NewTicker(checkEvery)
	defer retry.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-u.kick:
		case <-retry.C:
		}
		u.drain(ctx, &last)
	}
}

// drain publishes until the outbox is empty, the connection drops or a
// publish fails. last is the real (not injected) time of the previous publish.
func (u *Uplink) drain(ctx context.Context, last *time.Time) {
	for ctx.Err() == nil && u.connected.Load() {
		items, err := u.opts.Store.Pending(ctx, pendingBatch)
		if err != nil {
			u.log.Error("read outbox failed", "err", err)
			return
		}
		if len(items) == 0 {
			return
		}
		for _, it := range items {
			if wait := u.opts.ReplayInterval - time.Since(*last); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
			if !u.connected.Load() {
				return
			}
			err := u.publishOutbox(ctx, it.Topic, it.Payload)
			*last = time.Now()
			if err != nil {
				u.log.Warn("publish failed, will retry", "topic", it.Topic, "outbox_seq", it.Seq, "err", err)
				if ferr := u.opts.Store.Fail(ctx, it.Seq, err); ferr != nil {
					u.log.Error("record outbox failure", "err", ferr)
				}
				return
			}
			if err := u.opts.Store.Ack(ctx, it.Seq); err != nil {
				u.log.Error("delete delivered outbox entry", "outbox_seq", it.Seq, "err", err)
				return
			}
		}
	}
}

func (u *Uplink) publishOutbox(ctx context.Context, topic string, payload []byte) error {
	u.mu.Lock()
	cm := u.cm
	u.mu.Unlock()
	if cm == nil {
		return errNotConnected
	}
	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	return u.publishVia(pctx, cm, topic, payload, false)
}

var errNotConnected = errors.New("uplink: not connected")
